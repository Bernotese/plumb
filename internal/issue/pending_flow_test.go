package issue_test

// T-056: pending Proxmox values, allow_restart and the state "restart
// needed", end to end with driver/fake (D-190, D-191, D-194, D-195).

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// guestTarget is web1 with a memory size and the restart setting.
func guestTarget(memory int, allow bool) state.Doc {
	a := "false"
	if allow {
		a = "true"
	}
	return doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","allow_restart":`+a+`,"guest":{"cores":2,"memory":`+itoa(memory)+
		`,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"hello web1"}}]}`)
}

// pendingEnv seeds web1 (memory 1024) with the setting.
func pendingEnv(t *testing.T, allow bool) (*env, driver.GuestID) {
	t.Helper()
	e := newEnv(t)
	e.agent("web1")
	put := []state.Doc{doc(state.KindImage, "ubuntu", image), doc(state.KindBlock, "motd", motd), guestTarget(1024, allow)}
	if run := e.approve(e.propose(plan.Proposal{Put: put}).ID); run.Status != "succeeded" {
		t.Fatalf("seed: %s", run.Status)
	}
	e.online("web1")
	b, err := e.st.GetBinding(e.ctx, state.KindTarget, "web1")
	if err != nil {
		t.Fatal(err)
	}
	return e, driver.GuestID(b.Ref)
}

func (e *env) guestChanges(p store.Plan) plan.ResourceChange {
	e.t.Helper()
	for _, rc := range changes(e.t, p)["web1"].Resources {
		if rc.Resource == wire.GuestConfigID {
			return rc
		}
	}
	return plan.ResourceChange{}
}

func (e *env) guestStep(run store.Run) store.Step {
	e.t.Helper()
	steps, _ := e.st.Steps(e.ctx, run.ID)
	for _, s := range steps {
		if s.Resource == wire.GuestConfigID {
			return s
		}
	}
	e.t.Fatalf("no guest step in run %d: %+v", run.ID, steps)
	return store.Step{}
}

func stepChanges(t *testing.T, s store.Step) []wire.Change {
	t.Helper()
	var ch []wire.Change
	if err := json.Unmarshal(s.Changes, &ch); err != nil {
		t.Fatal(err)
	}
	return ch
}

func find(ch []wire.Change, attr string) *wire.Change {
	for i := range ch {
		if ch[i].Attr == attr {
			return &ch[i]
		}
	}
	return nil
}

func (e *env) waiting() []store.Waiting {
	e.t.Helper()
	b, err := e.st.GetBinding(e.ctx, state.KindTarget, "web1")
	if err != nil {
		e.t.Fatal(err)
	}
	return b.Waiting
}

func (e *env) check() {
	e.t.Helper()
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		e.t.Fatal(err)
	}
}

// approveBelow approves with a token tier of max_risk reversible.
func (e *env) approveBelow(id int64) error {
	_, err := e.eng.Approve(e.ctx, id, "token:ci", func(p store.Plan) error {
		if wire.Risk(p.Risk).Exceeds(wire.RiskReversible) {
			return errors.New("above the token's max_risk")
		}
		return nil
	})
	e.eng.Wait()
	return err
}

func (e *env) eventsOf(typ, subject string) []map[string]any {
	evs, _ := e.st.EventsAfter(e.ctx, 0, 1000)
	var out []map[string]any
	for _, ev := range evs {
		if ev.Type == typ && ev.Subject == subject {
			var m map[string]any
			json.Unmarshal(ev.Data, &m)
			out = append(out, m)
		}
	}
	return out
}

func (e *env) systemComment(id int64) string {
	cs, _ := e.st.Comments(e.ctx, id)
	for _, c := range cs {
		if c.Author == "system" {
			return c.Body
		}
	}
	return ""
}

// An outside pending value is an Issue in one round; revert removes it
// with the risk reversible, no restart, no power action (D-190, D-191).
func TestPendingOutsideRevert(t *testing.T) {
	e, id := pendingEnv(t, false)
	e.drv.TamperPending(id, func(s *driver.GuestSpec) { s.MemoryMB = 256 })
	e.check()
	iss, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID)
	if err != nil || !strings.Contains(string(iss.Changes), `"pending":true`) || !strings.Contains(string(iss.Changes), `"from":256`) {
		t.Fatalf("pending issue: %+v %v", iss, err)
	}
	p, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	rc := e.guestChanges(p)
	if p.Risk != string(wire.RiskReversible) || find(rc.Changes, wire.AttrRestart) != nil {
		t.Fatalf("revert plan: risk %s, %+v", p.Risk, rc)
	}
	power := e.drv.Calls(fake.OpPower)
	if err := e.approveBelow(p.ID); err != nil {
		t.Fatalf("a reversible token cannot revert a pending value: %v", err)
	}
	if e.drv.Calls(fake.OpPower) != power {
		t.Error("the revert sent a power action")
	}
	st, _ := e.drv.Observe(e.ctx, id)
	if st.Pending != nil || st.Spec.MemoryMB != 1024 {
		t.Errorf("after revert: %+v", st)
	}
	e.check()
	if got, _ := e.st.GetIssue(e.ctx, iss.ID); got.Status != store.IssueResolved || e.systemComment(iss.ID) != "" {
		t.Errorf("issue %+v, comment %q", got, e.systemComment(iss.ID))
	}
}

// Review B1: when Proxmox keeps the pending value after a write of the
// running value, the revert step fails and stores no digest, so the poll
// still sees the pending drift.
func TestRevertKeepsPendingFails(t *testing.T) {
	e, id := pendingEnv(t, false)
	e.drv.SetKeepPending(true)
	e.drv.TamperPending(id, func(s *driver.GuestSpec) { s.MemoryMB = 256 })
	e.check()
	iss := e.openIssue("web1", wire.GuestConfigID)
	p, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	run := e.approve(p.ID)
	if s := e.guestStep(run); run.Status != "failed" || !strings.Contains(s.Output, "Proxmox kept the pending value of memory (256; the VM uses 1024)") {
		t.Fatalf("revert: %s %q", run.Status, s.Output)
	}
	if got, _ := e.st.GetIssue(e.ctx, iss.ID); got.Status != store.IssueOpen {
		t.Errorf("issue after the failed revert: %s", got.Status)
	}
	st, _ := e.drv.Observe(e.ctx, id)
	if b, _ := e.st.GetBinding(e.ctx, state.KindTarget, "web1"); b.Digest == st.Digest {
		t.Error("the failed step stored the digest")
	}
	e.check()
	if again, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID); err != nil || !strings.Contains(string(again.Changes), `"pending":true`) {
		t.Errorf("pending drift hidden: %+v %v", again, err)
	}
}

// A Plan that only changes allow_restart: one read change, no driver
// write, a reversible token can approve it (D-194).
func TestAllowRestartOnlyPlan(t *testing.T) {
	e, _ := pendingEnv(t, false)
	p := e.propose(plan.Proposal{Put: []state.Doc{guestTarget(1024, true)}})
	rc := e.guestChanges(p)
	if p.Risk != string(wire.RiskRead) || len(rc.Changes) != 1 || rc.Changes[0].Attr != plan.AttrAllowRestart ||
		rc.Changes[0].From != false || rc.Changes[0].To != true || len(p.HumanOnly) != 0 {
		t.Fatalf("plan: risk %s, %+v, human-only %v", p.Risk, rc, p.HumanOnly)
	}
	updates := e.drv.Calls(fake.OpUpdate)
	if err := e.approveBelow(p.ID); err != nil {
		t.Fatal(err)
	}
	if e.drv.Calls(fake.OpUpdate) != updates {
		t.Error("allow_restart reached the driver")
	}
	run, _ := e.st.RunOfPlan(e.ctx, p.ID)
	if run.Status != "succeeded" || find(stepChanges(t, e.guestStep(run)), plan.AttrAllowRestart) == nil {
		t.Errorf("run %s, step %+v", run.Status, e.guestStep(run))
	}
}

// allow_restart false: a memory change on a running guest waits; the
// state "restart needed" ends with a cold start outside, also when the
// digest does not move (D-195).
func TestWaitsWithoutRestart(t *testing.T) {
	e, id := pendingEnv(t, false)
	p := e.propose(plan.Proposal{Put: []state.Doc{guestTarget(2048, false)}})
	c := find(e.guestChanges(p).Changes, driver.AttrMemory)
	if c == nil || !c.WaitsForRestart || c.Risk != wire.RiskDisruptive || find(e.guestChanges(p).Changes, wire.AttrRestart) != nil {
		t.Fatalf("plan change: %+v", e.guestChanges(p))
	}
	if err := e.approveBelow(p.ID); err == nil {
		t.Fatal("a reversible token approved a write that waits")
	}
	power := e.drv.Calls(fake.OpPower)
	run := e.approve(p.ID)
	if run.Status != "succeeded" || run.WaitsForRestart != 1 || e.drv.Calls(fake.OpPower) != power {
		t.Fatalf("run %s waits %d, power calls %d", run.Status, run.WaitsForRestart, e.drv.Calls(fake.OpPower)-power)
	}
	step := e.guestStep(run)
	if c := find(stepChanges(t, step), driver.AttrMemory); step.Status != "applied" || c == nil || !c.WaitsForRestart ||
		!strings.Contains(step.Output, "memory: set in Proxmox, waits for a stop and start of the VM") {
		t.Errorf("step %s %q %+v", step.Status, step.Output, c)
	}
	w := e.waiting()
	if len(w) != 1 || w[0].Attr != "memory" || w[0].From != 1024 || w[0].To != 2048 || w[0].Since.IsZero() {
		t.Fatalf("waiting: %+v", w)
	}
	since := w[0].Since
	if l, _ := e.eng.Live(e.ctx, "web1"); len(l.Waiting) != 1 {
		t.Errorf("Live.Waiting: %+v", l.Waiting)
	}
	e.check()
	e.check()
	if all, _ := e.st.Issues(e.ctx, "web1"); len(all) != 0 {
		t.Errorf("a waiting value opened an Issue: %+v", all)
	}
	if w := e.waiting(); len(w) != 1 || !w[0].Since.Equal(since) {
		t.Errorf("since moved: %+v", w)
	}
	before := len(e.eventsOf("target.status", "target:web1"))
	e.drv.ColdRestart(id, true)
	e.check()
	if w := e.waiting(); len(w) != 0 {
		t.Errorf("after a cold start: %+v", w)
	}
	evs := e.eventsOf("target.status", "target:web1")
	if len(evs) != before+1 || evs[len(evs)-1]["waiting"] != float64(0) {
		t.Errorf("events: %+v", evs[before:])
	}
	if all, _ := e.st.Issues(e.ctx, "web1"); len(all) != 0 {
		t.Errorf("issues after the cold start: %+v", all)
	}
}

// allow_restart false: a value that applies live, or a stopped guest,
// waits for nothing; the stopped guest stays stopped.
func TestWaitsLiveAndStopped(t *testing.T) {
	e, id := pendingEnv(t, false)
	e.drv.SetHotplug(true)
	run := e.approve(e.propose(plan.Proposal{Put: []state.Doc{guestTarget(2048, false)}}).ID)
	if c := find(stepChanges(t, e.guestStep(run)), driver.AttrMemory); run.Status != "succeeded" || run.WaitsForRestart != 0 || c == nil || c.WaitsForRestart || len(e.waiting()) != 0 {
		t.Errorf("hotplug: run %s %d, %+v, %+v", run.Status, run.WaitsForRestart, c, e.waiting())
	}
	e.drv.SetHotplug(false)
	e.drv.StopOutside(id)
	power := e.drv.Calls(fake.OpPower)
	run = e.approve(e.propose(plan.Proposal{Put: []state.Doc{guestTarget(4096, false)}}).ID)
	st, _ := e.drv.Observe(e.ctx, id)
	if run.WaitsForRestart != 0 || st.Power != driver.PowerStopped || st.Spec.MemoryMB != 4096 || e.drv.Calls(fake.OpPower) != power {
		t.Errorf("stopped: run %s %d, %+v", run.Status, run.WaitsForRestart, st)
	}
}

// The step fails when Proxmox took neither value, and when a bridge
// change waits (with true too, and without a restart).
func TestWaitsFailures(t *testing.T) {
	e, id := pendingEnv(t, false)
	e.drv.Hook(fake.OpUpdate, func() {
		e.drv.Hook(fake.OpObserve, func() { e.drv.Tamper(id, func(s *driver.GuestSpec) { s.MemoryMB = 999 }) })
	})
	run := e.approve(e.propose(plan.Proposal{Put: []state.Doc{guestTarget(2048, false)}}).ID)
	if s := e.guestStep(run); run.Status != "failed" || !strings.Contains(s.Output, "Proxmox did not take the value of memory") {
		t.Errorf("not taken: %s %q", run.Status, s.Output)
	}
	e.drv.Tamper(id, func(s *driver.GuestSpec) { s.MemoryMB = 2048 })

	e2, _ := pendingEnv(t, true)
	e2.drv.SetBridgeStuck(true)
	bridge := doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","allow_restart":true,"guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr1"},"blocks":[{"use":"motd","params":{"text":"hello web1"}}]}`)
	power := e2.drv.Calls(fake.OpPower)
	run = e2.approve(e2.propose(plan.Proposal{Put: []state.Doc{bridge}}).ID)
	if s := e2.guestStep(run); run.Status != "failed" || !strings.Contains(s.Output, "did not apply the bridge change live") || e2.drv.Calls(fake.OpPower) != power {
		t.Errorf("stuck bridge: %s %q", run.Status, s.Output)
	}
}

// Re-enrollment with allow_restart false (D-194): a stopped guest is
// started (never restarted); a guest started after the Plan fails the
// step before any token or write (B1); a guest started between the check
// and the power action is not restarted, and the step says that the
// token is pending.
func TestReenrollWithoutRestart(t *testing.T) {
	e, id := pendingEnv(t, false)
	e.drv.StopOutside(id)
	boots := e.drv.Boots(id)
	power := e.drv.Calls(fake.OpPower)
	run := e.approve(e.propose(plan.Proposal{Reenroll: []string{"web1"}}).ID)
	if run.Status != "succeeded" || e.drv.Calls(fake.OpPower) != power+1 || e.drv.Boots(id) != boots+1 {
		t.Fatalf("stopped: run %s, power %d, boots %d", run.Status, e.drv.Calls(fake.OpPower)-power, e.drv.Boots(id)-boots)
	}

	// B1: planned while stopped, started before the Run.
	e.drv.StopOutside(id)
	p := e.propose(plan.Proposal{Reenroll: []string{"web1"}})
	e.drv.StartOutside(id)
	updates, power := e.drv.Calls(fake.OpUpdate), e.drv.Calls(fake.OpPower)
	run = e.approve(p.ID)
	if s := e.guestStep(run); run.Status != "failed" || !strings.Contains(s.Output, "the guest runs now, and this Target does not allow a restart") ||
		e.drv.Calls(fake.OpUpdate) != updates || e.drv.Calls(fake.OpPower) != power {
		t.Errorf("B1: %s %q, updates %d, power %d", run.Status, s.Output, e.drv.Calls(fake.OpUpdate)-updates, e.drv.Calls(fake.OpPower)-power)
	}

	// Started between the check and the power action.
	e.drv.StopOutside(id)
	p = e.propose(plan.Proposal{Reenroll: []string{"web1"}})
	e.drv.Hook(fake.OpUpdate, func() { e.drv.StartOutside(id) })
	boots = e.drv.Boots(id)
	run = e.approve(p.ID)
	if s := e.guestStep(run); run.Status != "failed" || !strings.Contains(s.Output, "started outside Plumb") || !strings.Contains(s.Output, "pending in smbios1") ||
		e.drv.Boots(id) != boots+1 {
		t.Errorf("started meanwhile: %s %q, boots %d (only the outside start)", run.Status, s.Output, e.drv.Boots(id)-boots)
	}
}

// Accept of a pending drift with false: nothing to write, the Issue is
// resolved with the system comment, the list has the value.
func TestAcceptPendingWaits(t *testing.T) {
	e, id := pendingEnv(t, false)
	e.drv.TamperPending(id, func(s *driver.GuestSpec) { s.MemoryMB = 512 })
	e.check()
	iss := e.openIssue("web1", wire.GuestConfigID)
	p, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionAccept, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	updates := e.drv.Calls(fake.OpUpdate)
	if run := e.approve(p.ID); run.Status != "succeeded" || e.drv.Calls(fake.OpUpdate) != updates {
		t.Fatalf("accept run %s, updates %d", run.Status, e.drv.Calls(fake.OpUpdate)-updates)
	}
	got, _ := e.st.GetIssue(e.ctx, iss.ID)
	if got.Status != store.IssueResolved || !strings.Contains(e.systemComment(iss.ID), "Resolved, but not active: memory 512 is set and waits for a restart of the VM. The VM uses 1024.") {
		t.Errorf("issue %s, comment %q", got.Status, e.systemComment(iss.ID))
	}
	if w := e.waiting(); len(w) != 1 || w[0].To != 512 {
		t.Errorf("waiting: %+v", w)
	}
}

// Real drift, then the desired value pending from outside: gone, with the
// comment and the count in the event. A revert that ends waiting: the
// same comment.
func TestResolvedWhileWaiting(t *testing.T) {
	e, id := pendingEnv(t, false)
	e.drv.Tamper(id, func(s *driver.GuestSpec) { s.Cores = 8 })
	e.check()
	iss := e.openIssue("web1", wire.GuestConfigID)
	e.drv.TamperPending(id, func(s *driver.GuestSpec) { s.Cores = 2 })
	e.check()
	if got, _ := e.st.GetIssue(e.ctx, iss.ID); got.Status != store.IssueResolved || !strings.Contains(e.systemComment(iss.ID), "cores 2 is set") {
		t.Errorf("gone: %+v %q", got, e.systemComment(iss.ID))
	}
	evs := e.eventsOf("issue.resolved", "issue:"+itoa(int(iss.ID)))
	if len(evs) != 1 || evs[0]["waits_for_restart"] != float64(1) || evs[0]["resolution"] != "gone" {
		t.Errorf("event: %+v", evs)
	}

	e.drv.Tamper(id, func(s *driver.GuestSpec) { s.MemoryMB = 512 })
	e.check()
	iss = e.openIssue("web1", wire.GuestConfigID)
	p, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	e.approve(p.ID)
	if c := e.systemComment(iss.ID); !strings.Contains(c, "memory 1024 is set") || !strings.Contains(c, "The VM uses 512.") {
		t.Errorf("revert comment: %q", c)
	}
}

// Write point (b): a failed Run writes the list too.
func TestFailedRunWritesWaiting(t *testing.T) {
	e, _ := pendingEnv(t, false)
	e.agents["web1"].FailApply("file:/etc/motd", "boom")
	put := doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":2048,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"changed"}}]}`)
	run := e.approve(e.propose(plan.Proposal{Put: []state.Doc{put}}).ID)
	if run.Status != "failed" || len(e.waiting()) != 1 || run.WaitsForRestart != 1 {
		t.Errorf("failed run %s, waits %d, list %+v", run.Status, run.WaitsForRestart, e.waiting())
	}
}

// false → true while a value waits: the Plan holds the setting, the
// waiting value and the restart; the Run restarts; the list is empty.
func TestSwitchOnRestarts(t *testing.T) {
	e, id := pendingEnv(t, false)
	e.approve(e.propose(plan.Proposal{Put: []state.Doc{guestTarget(2048, false)}}).ID)
	if len(e.waiting()) != 1 {
		t.Fatal("nothing waits")
	}
	p := e.propose(plan.Proposal{Put: []state.Doc{guestTarget(2048, true)}})
	ch := e.guestChanges(p).Changes
	a, m, r := find(ch, plan.AttrAllowRestart), find(ch, driver.AttrMemory), find(ch, wire.AttrRestart)
	if a == nil || a.Risk != wire.RiskRead || m == nil || m.Risk != wire.RiskRead || !m.WaitsForRestart || r == nil || r.Risk != wire.RiskDisruptive {
		t.Fatalf("plan: %+v", ch)
	}
	boots := e.drv.Boots(id)
	run := e.approve(p.ID)
	st, _ := e.drv.Observe(e.ctx, id)
	if run.Status != "succeeded" || e.drv.Boots(id) != boots+1 || st.Spec.MemoryMB != 2048 || len(e.waiting()) != 0 || run.WaitsForRestart != 0 {
		t.Errorf("run %s, boots %d, %+v, waiting %+v", run.Status, e.drv.Boots(id)-boots, st.Spec, e.waiting())
	}
}

// allow_restart true: one restart for a memory change on a running
// guest; none and no start on a stopped guest; one write and one power
// action for a re-enrollment with a change; a skipped restart when
// Proxmox applies live.
func TestAllowRestartRuns(t *testing.T) {
	e, id := pendingEnv(t, true)
	p := e.propose(plan.Proposal{Put: []state.Doc{guestTarget(2048, true)}})
	if find(e.guestChanges(p).Changes, wire.AttrRestart) == nil {
		t.Fatalf("no restart in the plan: %+v", e.guestChanges(p))
	}
	power := e.drv.Calls(fake.OpPower)
	run := e.approve(p.ID)
	st, _ := e.drv.Observe(e.ctx, id)
	if run.Status != "succeeded" || e.drv.Calls(fake.OpPower) != power+1 || st.Spec.MemoryMB != 2048 || st.Pending != nil {
		t.Fatalf("running: %s, power %d, %+v", run.Status, e.drv.Calls(fake.OpPower)-power, st)
	}
	if s := e.guestStep(run); find(stepChanges(t, s), wire.AttrRestart) == nil {
		t.Errorf("step lacks the restart: %+v", stepChanges(t, s))
	}

	// Re-enrollment and a change in one Plan: one write, one power action.
	updates, power := e.drv.Calls(fake.OpUpdate), e.drv.Calls(fake.OpPower)
	pl, err := e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Put: []state.Doc{guestTarget(4096, true)}, Reenroll: []string{"web1"}}, Author: "user:admin"})
	if err != nil {
		t.Fatal(err)
	}
	if run := e.approve(pl.ID); run.Status != "succeeded" || e.drv.Calls(fake.OpUpdate) != updates+1 || e.drv.Calls(fake.OpPower) != power+1 {
		t.Errorf("reenroll + memory: %s, updates %d, power %d", run.Status, e.drv.Calls(fake.OpUpdate)-updates, e.drv.Calls(fake.OpPower)-power)
	}

	// Applies live: the planned restart is skipped.
	e.drv.SetHotplug(true)
	power = e.drv.Calls(fake.OpPower)
	run = e.approve(e.propose(plan.Proposal{Put: []state.Doc{guestTarget(1024, true)}}).ID)
	if s := e.guestStep(run); run.Status != "succeeded" || s.Status != "applied" || e.drv.Calls(fake.OpPower) != power || find(stepChanges(t, s), wire.AttrRestart) != nil {
		t.Errorf("hotplug: %s %s, power %d", run.Status, s.Status, e.drv.Calls(fake.OpPower)-power)
	}
	e.drv.SetHotplug(false)

	// Stopped: no restart and no start.
	e.drv.StopOutside(id)
	power = e.drv.Calls(fake.OpPower)
	e.approve(e.propose(plan.Proposal{Put: []state.Doc{guestTarget(2048, true)}}).ID)
	if st, _ := e.drv.Observe(e.ctx, id); st.Power != driver.PowerStopped || e.drv.Calls(fake.OpPower) != power {
		t.Errorf("stopped: %s, power %d", st.Power, e.drv.Calls(fake.OpPower)-power)
	}
}

// allow_restart true: failures name the power state; a planned Run
// without a restart fails before a write; a value that still waits after
// the restart fails; the resource steps wait for a new hello.
func TestAllowRestartFailures(t *testing.T) {
	e, id := pendingEnv(t, true)
	e.drv.FailRestart("start", errors.New("start task failed"))
	power := e.drv.Calls(fake.OpPower)
	run := e.approve(e.propose(plan.Proposal{Put: []state.Doc{guestTarget(2048, true)}}).ID)
	steps, _ := e.st.Steps(e.ctx, run.ID)
	if s := e.guestStep(run); run.Status != "failed" || !strings.Contains(s.Output, "the restart failed: the guest is stopped") || e.drv.Calls(fake.OpPower) != power+1 {
		t.Errorf("start fails: %s %q", run.Status, s.Output)
	}
	for _, s := range steps {
		if s.Resource != wire.GuestConfigID && s.Status != "failed" {
			t.Errorf("a resource step ran: %+v", s)
		}
	}
	e.drv.StartOutside(id)
	e.online("web1")

	e.drv.FailRestart("stop", errors.New("shutdown timed out"))
	run = e.approve(e.propose(plan.Proposal{Put: []state.Doc{guestTarget(4096, true)}}).ID)
	if s := e.guestStep(run); run.Status != "failed" || !strings.Contains(s.Output, "still runs with the old value") {
		t.Errorf("stop fails: %s %q", run.Status, s.Output)
	}
	// Cold start outside, so nothing waits any more.
	e.drv.ColdRestart(id, false)
	e.online("web1")

	// Planned while stopped (no restart in the Plan), started before the Run.
	e.drv.StopOutside(id)
	p := e.propose(plan.Proposal{Put: []state.Doc{guestTarget(8192, true)}})
	e.drv.StartOutside(id)
	updates := e.drv.Calls(fake.OpUpdate)
	power = e.drv.Calls(fake.OpPower)
	run = e.approve(p.ID)
	if s := e.guestStep(run); run.Status != "failed" || !strings.Contains(s.Output, "plan again") || e.drv.Calls(fake.OpUpdate) != updates || e.drv.Calls(fake.OpPower) != power {
		t.Errorf("no planned restart: %s %q", run.Status, s.Output)
	}

	// Still waits after the restart.
	e.drv.Hook(fake.OpPower, func() {
		e.drv.Hook(fake.OpObserve, func() {
			e.drv.Tamper(id, func(s *driver.GuestSpec) { s.MemoryMB = 1024 })
			e.drv.TamperPending(id, func(s *driver.GuestSpec) { s.MemoryMB = 16384 })
		})
	})
	run = e.approve(e.propose(plan.Proposal{Put: []state.Doc{guestTarget(16384, true)}}).ID)
	if s := e.guestStep(run); run.Status != "failed" || !strings.Contains(s.Output, "after the restart a value still waits") {
		t.Errorf("still waits: %s %q", run.Status, s.Output)
	}
}

// After a restart the resource steps wait for a hello of the new boot:
// an agent that only looks online from the old link gets no command.
func TestRestartWaitsForHello(t *testing.T) {
	e, _ := pendingEnv(t, true)
	e.mu.Lock()
	e.noBootHello = true
	e.mu.Unlock()
	applies := 0
	for _, c := range e.agents["web1"].Commands() {
		if c == wire.CommandApply {
			applies++
		}
	}
	put := doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","allow_restart":true,"guest":{"cores":2,"memory":2048,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"changed"}}]}`)
	run := e.approve(e.propose(plan.Proposal{Put: []state.Doc{put}}).ID)
	n := 0
	for _, c := range e.agents["web1"].Commands() {
		if c == wire.CommandApply {
			n++
		}
	}
	if run.Status != "failed" || n != applies {
		t.Errorf("without a new hello: run %s, applies %d", run.Status, n-applies)
	}
}

// After migration 0007 (every digest empty) the first poll round compares
// every guest: a pending value from before the upgrade opens its Issue,
// and a value that a Run wrote before the upgrade enters the list.
func TestFirstRoundAfterMigration(t *testing.T) {
	e, id := pendingEnv(t, false)
	e.approve(e.propose(plan.Proposal{Put: []state.Doc{guestTarget(2048, false)}}).ID)
	// The state of an old controller: the digest seen, no list.
	st, _ := e.drv.Observe(e.ctx, id)
	b, _ := e.st.GetBinding(e.ctx, state.KindTarget, "web1")
	b.Digest, b.Waiting = st.Digest+" "+string(st.Power), nil // the poll key (D-299)
	e.st.PutBinding(e.ctx, b)
	e.check()
	if len(e.waiting()) != 0 {
		t.Fatal("the test needs a poll that skips the guest")
	}
	b.Digest = "" // what 0007 does
	e.st.PutBinding(e.ctx, b)
	e.check()
	if w := e.waiting(); len(w) != 1 || w[0].To != 2048 {
		t.Errorf("round 1: %+v", w)
	}

	// A pending value from outside, seen by the old controller.
	e.drv.ColdRestart(id, false)
	e.check()
	e.drv.TamperPending(id, func(s *driver.GuestSpec) { s.Cores = 6 })
	st, _ = e.drv.Observe(e.ctx, id)
	b, _ = e.st.GetBinding(e.ctx, state.KindTarget, "web1")
	b.Digest = st.Digest + " " + string(st.Power) // the poll key (D-299)
	e.st.PutBinding(e.ctx, b)
	e.check()
	if _, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID); err == nil {
		t.Fatal("the test needs a poll that skips the guest")
	}
	b.Digest = ""
	e.st.PutBinding(e.ctx, b)
	e.check()
	if iss, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID); err != nil || !strings.Contains(string(iss.Changes), `"pending":true`) {
		t.Errorf("round 1: %+v %v", iss, err)
	}
}

// Review S2: a pending create-only input refuses a Plan of the Target with
// a text that says what to do in Proxmox.
func TestPendingCreateOnlyText(t *testing.T) {
	e, id := pendingEnv(t, false)
	e.drv.TamperPending(id, func(s *driver.GuestSpec) { s.IP = "192.0.2.9/24" })
	put := doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"other"}}]}`)
	_, err := e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Put: []state.Doc{put}}, Author: "user:admin"})
	if !errors.Is(err, plan.ErrCreateOnly) {
		t.Fatalf("propose: %v", err)
	}
	for _, part := range []string{`"ip" has a pending value in Proxmox (192.0.2.9/24, not active; the VM uses dhcp)`, "qm set " + string(id) + " --revert <key>", "Then plan again"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("text lacks %q: %v", part, err)
		}
	}
	if strings.Contains(err.Error(), "create a new target") {
		t.Errorf("the old text: %v", err)
	}
}
