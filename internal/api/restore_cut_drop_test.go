package api

import (
	"testing"

	"github.com/Bernotese/plumb/internal/plan"
)

// T-260 (O9 a): TODAY'S behaviour after a restore Run that a controller
// stop cut after the restore call (D-427). The start rule makes the
// re-enroll Plan, and the Run ends "succeeded", but it does not run the
// D-429 drop of the older safety backups: only the live Run calls
// DropSafetyBackups (restore_guest.go), recoverRestores does not. The old
// pre-restore volumes stay until the next restore that works. If the drop
// moves into the start rule (a fix card), this test changes with it.
func TestCutRestoreRunDropsNoSafetyBackups(t *testing.T) {
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
		t.Fatalf("a drop ran after the cut Run (keep %v): the start rule does it now; update O9 (a) and this test", dropped)
	}
}
