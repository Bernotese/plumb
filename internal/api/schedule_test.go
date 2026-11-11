package api

// T-063: scheduled image builds with a set clock (D-201, D-210).

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/link/linktest"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) set(s string) {
	t, _ := time.Parse("2006-01-02 15:04", s)
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}
func (c *clock) opt() func(*plan.Config) { return func(cfg *plan.Config) { cfg.Now = c.Now } }

func scheduledImage(schedule string) []state.Doc {
	return []state.Doc{
		doc(state.KindBlock, "banner", `{"resources":[{"file":{"path":"/etc/motd","content":"hi"}}]}`),
		doc(state.KindImage, "web", `{"type":"vm","url":"https://images.invalid/u.img","sha256":"`+builtSHA+
			`","build":{"blocks":[{"use":"banner"}],"guest":{"disk":10,"bridge":"vmbr0"}},"schedule":`+schedule+`}`),
	}
}

// schedulePlans returns the Plans of origin build and trigger schedule.
func (e *env) schedulePlans() []store.Plan {
	e.t.Helper()
	all, err := e.st.ListPlans(e.ctx, "", 0, 200)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []store.Plan
	for _, p := range all {
		if p.Origin == plan.OriginBuild && strings.Contains(string(p.Images), `"trigger":"schedule"`) {
			out = append(out, p)
		}
	}
	return out
}

func (e *env) tick() {
	e.t.Helper()
	if err := e.eng.CheckSchedules(e.ctx); err != nil {
		e.t.Fatal(err)
	}
	e.eng.Wait()
}

func TestScheduledBuilds(t *testing.T) {
	c := &clock{}
	c.set("2026-11-11 10:00") // a Wednesday
	e := newEnv(t, false, withBuilds, c.opt())
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": scheduledImage(`{"every":"day","at":"03:00"}`)})
	// The approval started the schedule: today's 03:00 slot is in the past
	// and older than the start, so nothing runs (D-210).
	e.tick()
	if n := len(e.schedulePlans()); n != 0 {
		t.Fatalf("a new schedule built at once: %d plans", n)
	}
	code, body := adm.do("GET", "/api/v1/images/web", nil)
	if code != 200 || !strings.Contains(string(body), `"next_build_at":"2026-11-12T03:00:00Z"`) {
		t.Fatalf("next build: %d %s", code, body)
	}

	// The next slot: one build, approved by system, no revision.
	c.set("2026-11-12 03:01")
	e.tick()
	ps := e.schedulePlans()
	if len(ps) != 1 || ps[0].Status != store.PlanApproved || ps[0].ApprovedBy != plan.PrincipalSystem || ps[0].Author != plan.PrincipalSystem {
		t.Fatalf("scheduled plans: %+v", ps)
	}
	if v, _ := e.st.GetImageVersion(e.ctx, "web", 2); v.Status != store.ImageReady || v.Trigger != store.TriggerSchedule {
		t.Fatalf("v2 = %+v", v)
	}
	if head, _ := e.st.Head(e.ctx); head.PlanID == ps[0].ID {
		t.Fatal("a scheduled build committed a revision")
	}
	// No person read the Plan: its disk note is in the Run (T-061 review).
	if st := e.lastSteps(ps[0].ID)["image:base"]; !strings.Contains(st.Output, "thin-pool metadata use is unknown") {
		t.Fatalf("image:base of the scheduled build = %q", st.Output)
	}
	// The same slot again: nothing.
	e.tick()
	if n := len(e.schedulePlans()); n != 1 {
		t.Fatalf("the slot ran twice: %d", n)
	}

	// A long stop: three weeks of missed slots give one build.
	c.set("2026-12-03 12:00")
	e.tick()
	e.tick()
	if n := len(e.schedulePlans()); n != 2 {
		t.Fatalf("after a long stop: %d plans, want 2", n)
	}

	// A failed scheduled build is not tried again before the next slot.
	e.buildAgents(func(a *linktest.Agent) { a.FailSeal(wire.SealStepTrim) })
	c.set("2026-12-04 03:00")
	e.tick()
	ps = e.schedulePlans()
	if len(ps) != 3 {
		t.Fatalf("slot of 12-04: %d plans", len(ps))
	}
	if vs, _ := e.st.ImageVersions(e.ctx, "web"); vs[0].Status != store.ImageFailed {
		t.Fatalf("the build of 12-04 = %+v, want failed", vs[0])
	}
	c.set("2026-12-04 18:00")
	e.tick()
	if n := len(e.schedulePlans()); n != 3 {
		t.Fatalf("a failed build was tried again: %d plans", n)
	}
}

// A slot that meets a running build of the Image is skipped; a changed
// schedule starts with its next slot.
func TestScheduleSkipAndChange(t *testing.T) {
	c := &clock{}
	c.set("2026-11-11 10:00")
	e := newEnv(t, false, withBuilds, c.opt())
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": scheduledImage(`{"every":"day","at":"03:00"}`)})
	e.tick()
	// A build of the Image runs (a version in the state building).
	e.addVersion("web", store.ImageBuilding)
	c.set("2026-11-12 03:05")
	e.tick()
	if n := len(e.schedulePlans()); n != 0 {
		t.Fatalf("a slot during a build made %d plans", n)
	}
	slot, _ := e.st.ImageScheduleSlot(e.ctx, "web")
	if !slot.Equal(time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("the skipped slot is not used up: %v", slot)
	}
	// A changed schedule (weekly) starts with its next slot, not with the
	// past Sunday slot.
	c.set("2026-11-16 12:00") // Monday
	e.runPlanAnyStatus(adm, map[string]any{"reason": "weekly", "put": scheduledImage(`{"every":"week","weekday":"sun","at":"03:00"}`)})
	e.tick()
	if n := len(e.schedulePlans()); n != 0 {
		t.Fatalf("a changed schedule built at once: %d plans", n)
	}
}

// Review S1 of #138: a scheduled Plan whose system approval fails is stale
// at once; no token or person can approve it later.
func TestFailedSystemApprovalIsStale(t *testing.T) {
	c := &clock{}
	c.set("2026-11-11 10:00")
	e := newEnv(t, false, withBuilds, c.opt())
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": scheduledImage(`{"every":"day","at":"03:00"}`)})
	e.tick()
	// A build of the Image starts between the Plan and its approval.
	e.drv.Hook(fake.OpDiskSpace, func() { e.addVersion("web", store.ImageBuilding) })
	c.set("2026-11-12 03:01")
	if err := e.eng.CheckSchedules(e.ctx); err != nil {
		t.Fatal(err)
	}
	ps := e.schedulePlans()
	if len(ps) != 1 || ps[0].Status != store.PlanStale {
		t.Fatalf("scheduled plans: %+v", ps)
	}
	tok := e.token(TierApply, "disruptive")
	if code, body := tok.do("POST", "/api/v1/plans/"+itoa(ps[0].ID)+"/approve", nil); code != 409 {
		t.Fatalf("token approves the failed scheduled plan: %d %s", code, body)
	}
}
