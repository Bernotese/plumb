package api

import (
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
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
