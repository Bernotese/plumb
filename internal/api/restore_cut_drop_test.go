package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// O9 (a): a restore Run that a controller stop cut after the restore call
// (D-427). The start rule makes the re-enroll Plan, and it now also runs
// the D-429 drop that the live Run would have run: the older safety
// backups go, this Run's safety backup (the way back of the Issue) stays.
func TestCutRestoreRunDropsOlderSafetyBackups(t *testing.T) {
	s := newSafetyEnv(t)
	sv, err := s.fr.safety() // this Run's safety backup: bk:safety-1
	if err != nil {
		t.Fatal(err)
	}
	cutRestore(t, s, "restored backup 3f2a9c0d1e4b5a67 of 2026-11-15T03:00:12Z")
	// The Issue of the cut Run, as restoreRunningIssue writes it: the
	// running text and the way back as data.
	ch := []wire.Change{
		{Attr: "restore", From: "running: if this restore does not end, the guest may have no disk", To: "restore the safety backup " + sv.ID, Risk: wire.RiskDestructive},
		{Attr: "way_back", To: map[string]any{"backup": sv.ID, "time": sv.Time.UTC().Format(time.RFC3339)}, Risk: wire.RiskDestructive},
	}
	if err := s.e.iss.Intake(s.e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: plan.GuestRestoreFailedID, Status: wire.StatusDiffers, Changes: ch}}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // a second start changes nothing more
		if err := s.e.eng.Recover(s.e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	s.fr.mu.Lock()
	dropped := append([]string(nil), s.fr.dropped...)
	s.fr.mu.Unlock()
	if len(dropped) != 1 || dropped[0] != sv.Volid {
		t.Fatalf("drops after the cut Run: %v, want one that keeps %s", dropped, sv.Volid)
	}
	if re, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll); len(re) != 1 {
		t.Fatalf("re-enroll Plans: %d, want 1", len(re))
	}
	s.reenrollDone(t)
	s.fr.mu.Lock()
	defer s.fr.mu.Unlock()
	if len(s.fr.dropped) != 1 {
		t.Errorf("the re-enroll Run dropped again: %v", s.fr.dropped)
	}
}

// Without a way back in the Issue (the stop came after the Issue already
// said "restored", or the Issue names no safety backup) the start rule
// cannot know which volume this Run kept: it drops nothing, and the next
// restore that works does it (D-429, as before O9 (a)).
func TestCutRestoreRunWithoutWayBackDropsNothing(t *testing.T) {
	s := newSafetyEnv(t)
	cutRestore(t, s, "restored backup 3f2a9c0d1e4b5a67 of 2026-11-15T03:00:12Z")
	for i := 0; i < 2; i++ {
		if err := s.e.eng.Recover(s.e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if re, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll); len(re) != 1 {
		t.Fatalf("re-enroll Plans: %d, want 1", len(re))
	}
	s.reenrollDone(t)
	s.fr.mu.Lock()
	dropped := append([]string(nil), s.fr.dropped...)
	s.fr.mu.Unlock()
	if len(dropped) != 0 {
		t.Fatalf("a drop ran without a known volume to keep: %v", dropped)
	}
}

// cutSkipRestore is cutRestore for a Plan that skips the safety backup
// (the guest is broken already): it restores backup id, and its Issue has
// no way back ("restore another backup").
func cutSkipRestore(t *testing.T, s *safetyEnv, id, at string) {
	t.Helper()
	e := s.e
	head, _ := e.st.Head(e.ctx)
	docs, _ := e.st.RevisionDocs(e.ctx, head.ID)
	changes, _ := json.Marshal([]plan.TargetPlan{{Target: "web1", Resources: []plan.ResourceChange{{Resource: wire.GuestConfigID, Action: wire.ActionUpdate,
		Risk: wire.RiskDestructive, Changes: []wire.Change{
			{Attr: plan.AttrRestore, To: "backup " + id + " of " + at, Risk: wire.RiskDestructive},
			{Attr: plan.AttrSafetyBackup, To: "skipped: the guest is broken already", Risk: wire.RiskDestructive},
		}}}}})
	p, err := e.st.CreatePlan(e.ctx, store.Plan{BaseRevision: head.ID, Origin: plan.OriginRestore, Risk: "destructive", Author: "user:admin", Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := e.st.ApprovePlan(e.ctx, p.ID, "user:admin", store.NewRevision{Author: "user:admin", Docs: docs})
	if err != nil {
		t.Fatal(err)
	}
	seq, err := e.st.AddStep(e.ctx, store.Step{RunID: run.ID, Target: "web1", Resource: wire.GuestConfigID, Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpdateStep(e.ctx, run.ID, seq, "running", nil, "restored backup "+id+" of "+at); err != nil {
		t.Fatal(err)
	}
	ch := []wire.Change{{Attr: "restore", From: "running: if this restore does not end, the guest may have no disk", To: "restore another backup", Risk: wire.RiskDestructive}}
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: plan.GuestRestoreFailedID, Status: wire.StatusDiffers, Changes: ch}}}); err != nil {
		t.Fatal(err)
	}
}

func recoverTwice(t *testing.T, s *safetyEnv) []string {
	t.Helper()
	for i := 0; i < 2; i++ {
		if err := s.e.eng.Recover(s.e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	s.fr.mu.Lock()
	defer s.fr.mu.Unlock()
	return append([]string(nil), s.fr.dropped...)
}

// #408 M1: a cut restore of a SCHEDULED backup by a Plan that skips the
// safety backup keeps everything: the live Run keeps a restored volume
// only when it is a safety backup itself (isPreRestore), and drops
// nothing otherwise. Without the guard, every safety backup older than the
// scheduled volume would go, and one of them may be the only way back to
// the guest before an earlier restore.
func TestCutSkipRestoreOfScheduledBackupDropsNothing(t *testing.T) {
	s := newSafetyEnv(t)
	if _, err := s.fr.safety(); err != nil { // an older safety backup that must stay
		t.Fatal(err)
	}
	cutSkipRestore(t, s, "3f2a9c0d1e4b5a67", "2026-11-15T03:00:12Z") // scheduled, not pre-restore
	if dropped := recoverTwice(t, s); len(dropped) != 0 {
		t.Fatalf("a cut restore of a scheduled backup dropped safety backups (keep %v)", dropped)
	}
}

// #408 M1: a cut restore of a SAFETY backup by a skip Plan keeps that
// volume (and every newer one) and drops only the older ones, as the live
// Run does (D-429).
func TestCutSkipRestoreOfSafetyBackupKeepsIt(t *testing.T) {
	s := newSafetyEnv(t)
	if _, err := s.fr.safety(); err != nil { // bk:safety-1, older
		t.Fatal(err)
	}
	sv, err := s.fr.safety() // bk:safety-2, the restored one
	if err != nil {
		t.Fatal(err)
	}
	cutSkipRestore(t, s, sv.ID, sv.Time.UTC().Format(time.RFC3339))
	dropped := recoverTwice(t, s)
	if len(dropped) == 0 {
		t.Fatal("no drop after a cut restore of a safety backup")
	}
	for _, keep := range dropped {
		if keep != sv.Volid {
			t.Fatalf("drop kept %s, want the restored safety backup %s", keep, sv.Volid)
		}
	}
}
