package issue_test

import (
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// T-227 row 31 (#314 known low): an address Issue that the delete of its
// Target resolved is never opened again, also not when a new Target of the
// same name reports the same drift within the reopen window (D-215). The
// control: an address Issue that went away by itself is reopened (D-215
// still holds).
func TestNoReopenAfterTargetDelete(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	drift := func() {
		t.Helper()
		if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{
			Resource: wire.GuestAddressID, Status: wire.StatusDiffers,
			Changes: []wire.Change{{Attr: "addresses", From: "192.0.2.9", To: "192.0.2.8", Risk: wire.RiskReversible}},
		}}}); err != nil {
			t.Fatal(err)
		}
	}
	gone := func() {
		t.Helper()
		if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{
			Resource: wire.GuestAddressID, Status: wire.StatusUnchanged}}}); err != nil {
			t.Fatal(err)
		}
	}

	// Control: drift, gone by itself, drift again → the same Issue (D-215).
	drift()
	first := e.openIssue("web1", wire.GuestAddressID)
	gone()
	drift()
	if again := e.openIssue("web1", wire.GuestAddressID); again.ID != first.ID {
		t.Fatalf("control: a gone address Issue was not reopened (%d, then %d)", first.ID, again.ID)
	}

	// The Target is deleted: its Issue ends, marked.
	if err := e.iss.TargetDeleted(e.ctx, "web1", "the Target was deleted"); err != nil {
		t.Fatal(err)
	}
	if got, err := e.st.GetIssue(e.ctx, first.ID); err != nil || got.Status != "resolved" {
		t.Fatalf("after the delete: %+v %v", got, err)
	}
	// A new Target of the name reports the same drift: a new Issue.
	drift()
	if next := e.openIssue("web1", wire.GuestAddressID); next.ID == first.ID {
		t.Fatalf("Issue %d of the deleted Target was opened again", first.ID)
	}
}
