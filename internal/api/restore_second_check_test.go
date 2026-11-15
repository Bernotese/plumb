package api

// T-229 (Q1 of #340): the engine's own second check does not trust the
// restorer's kind. A restorer that says "plumb" for the Plan and the
// approval, then hands the Run a volume that differs in one field: the Run
// refuses before the stop; the guest runs, the agent keeps its credential.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
)

// switchRestorer answers good for the first two FindBackup calls (propose,
// approval) and bad from the third (the Run).
type switchRestorer struct {
	fakeRestorer
	mu    sync.Mutex
	calls int
	good  plan.RestoreVolume
	bad   plan.RestoreVolume
	atRun func() // runs at the Run's lookup, before the answer
}

func (s *switchRestorer) FindBackup(_ context.Context, _, _, id string) (plan.RestoreVolume, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls <= 2 {
		return s.good, nil
	}
	if s.atRun != nil {
		s.atRun()
	}
	return s.bad, nil
}

func TestRestoreRunSecondCheck(t *testing.T) {
	at := time.Date(2026, 11, 15, 3, 0, 12, 0, time.UTC)
	for _, c := range []struct {
		name   string
		change func(v *plan.RestoreVolume, lineage string)
		before func(e *env) // a store change at the Run's lookup
	}{
		{"another install", func(v *plan.RestoreVolume, l string) { v.Notes = "plumb:other:web1:" + l }, nil},
		{"another Target", func(v *plan.RestoreVolume, l string) { v.Notes = "plumb:iid:web2:" + l }, nil},
		{"another lineage", func(v *plan.RestoreVolume, _ string) { v.Notes = "plumb:iid:web1:0123456789abcdef" }, nil},
		{"manual marker of a scheduled job", func(v *plan.RestoreVolume, l string) { v.Notes = "plumb:iid:web1:" + l + ":m" }, nil},
		{"another size", func(v *plan.RestoreVolume, _ string) { v.Size = 101 }, nil},
		{"another time", func(v *plan.RestoreVolume, _ string) { v.Time = v.Time.Add(time.Second) }, nil},
		{"another volume", func(v *plan.RestoreVolume, _ string) { v.Volid = "bk:v2" }, nil},
		{"deleted, then made again", func(*plan.RestoreVolume, string) {}, func(e *env) {
			if err := e.st.MarkGuestBackupDeleted(e.ctx, "web1", "bk:v1"); err != nil {
				panic(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			sr := &switchRestorer{fakeRestorer: fakeRestorer{vols: map[string]plan.RestoreVolume{}}}
			e := newEnv(t, true, func(cfg *plan.Config) { cfg.GuestRestore = sr; cfg.InstallID = "iid" })
			adm := e.admin()
			e.seed(adm, "web1")
			e.approve(adm, "allow restart", restoreTarget("web1", true))
			ctx := context.Background()
			b, _ := e.st.GetBinding(ctx, state.KindTarget, "web1")
			lineage, _ := e.st.BackupLineage(ctx, "web1", b.Gen)
			run, _ := e.st.StartGuestBackupRun(ctx, "web1", lineage, "scheduled")
			e.st.FinishGuestBackupRun(ctx, run, "done", "bk:v1", 100, at.Unix(), "")
			sr.good = plan.RestoreVolume{ID: "3f2a9c0d1e4b5a67", Volid: "bk:v1", Kind: "plumb", Notes: "plumb:iid:web1:" + lineage, Time: at, Size: 100}
			sr.bad = sr.good
			c.change(&sr.bad, lineage)
			code, body := adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r"})
			if code != 201 {
				t.Fatalf("propose: %d %s", code, body)
			}
			var p struct{ ID int64 }
			json.Unmarshal(body, &p)
			if c.before != nil {
				sr.atRun = func() { c.before(e) } // between the approval and the Run's check
			}
			if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
				t.Fatalf("approve: %d %s", code, body)
			}
			e.eng.Wait()
			r, _ := e.st.RunOfPlan(ctx, p.ID)
			steps, _ := e.st.Steps(ctx, r.ID)
			if r.Status != "failed" || len(steps) != 1 || !strings.HasPrefix(steps[0].Output, "nothing was changed:") {
				t.Fatalf("run %s, steps %+v", r.Status, steps)
			}
			if len(sr.restored) != 0 {
				t.Errorf("restored: %v", sr.restored)
			}
			if st, _ := e.drv.Observe(ctx, driver.GuestID(b.Ref)); st.Power != driver.PowerRunning {
				t.Errorf("the guest was stopped: %s", st.Power)
			}
			if _, err := e.st.AgentByTarget(ctx, "web1"); err != nil {
				t.Errorf("the agent was revoked: %v", err)
			}
		})
	}
}
