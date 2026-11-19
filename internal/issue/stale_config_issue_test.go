package issue_test

import (
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Row 82 of the T-260 log (T-247 F2): a guest:config drift Issue is open,
// and a Run that is not its revert (here a re-enroll, as on the host)
// writes the guest back to the desired state and stores the new digest.
// The poll compares a guest only when the digest changed, so before the
// fix it never compared again and the Issue stayed open although nothing
// differed. Now the next poll compares, ends the Issue with its system
// comment (D-428), and keeps a true one open.
func TestRunThatFixesDriftEndsTheConfigIssue(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	b, _ := e.st.GetBinding(e.ctx, state.KindTarget, "web1")
	id := driver.GuestID(b.Ref)
	e.drv.Tamper(id, func(s *driver.GuestSpec) { s.Cores = 8 })
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Fatal(err)
	}
	iss, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID)
	if err != nil || !strings.Contains(string(iss.Changes), `"from":8`) {
		t.Fatalf("drift Issue: %+v %v", iss, err)
	}
	// Stopped outside Plumb, the re-enroll Plan is allowed; its Run writes
	// the whole desired guest config (cores 2) and starts the guest.
	e.drv.StopOutside(id)
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Fatal(err)
	}
	run := e.approve(e.propose(plan.Proposal{Reenroll: []string{"web1"}}).ID)
	if run.Status != "succeeded" {
		steps, _ := e.st.Steps(e.ctx, run.ID)
		t.Fatalf("reenroll run %s: %+v", run.Status, steps)
	}
	if st, _ := e.drv.Observe(e.ctx, id); st.Spec.Cores != 2 {
		t.Fatalf("the Run did not write the cores back: %d", st.Spec.Cores)
	}
	// Right after the Run the Issue is still open; the next poll decides.
	if got, _ := e.st.GetIssue(e.ctx, iss.ID); got.Status != store.IssueOpen {
		t.Fatalf("the Run itself changed the Issue: %s", got.Status)
	}
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.GetIssue(e.ctx, iss.ID)
	if got.Status != store.IssueResolved {
		t.Fatalf("the Issue stays %s after the Run that fixed the guest: %s", got.Status, got.Changes)
	}
	if cs, _ := e.st.Comments(e.ctx, iss.ID); len(cs) == 0 {
		t.Error("the system resolution has no comment (D-428)")
	}
	// The digest is stored again by that poll: the next round does not
	// compare (no extra work while no Issue is open).
	after, _ := e.st.GetBinding(e.ctx, state.KindTarget, "web1")
	if after.Digest == "" {
		t.Error("the poll did not store the digest after the compare")
	}
}
