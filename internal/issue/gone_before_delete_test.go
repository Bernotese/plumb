package issue_test

import (
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// Review #324: an Issue that went gone by itself just before the delete.
func TestGoneBeforeDeleteNotReopened(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	rep := func(st wire.Status, ch []wire.Change) {
		t.Helper()
		if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: wire.GuestAddressID, Status: st, Changes: ch}}}); err != nil {
			t.Fatal(err)
		}
	}
	ch := []wire.Change{{Attr: "addresses", From: "192.0.2.9", To: "192.0.2.8", Risk: wire.RiskReversible}}
	rep(wire.StatusDiffers, ch)
	first := e.openIssue("web1", wire.GuestAddressID)
	rep(wire.StatusUnchanged, nil) // gone by itself
	if err := e.iss.TargetDeleted(e.ctx, "web1", "the Target was deleted"); err != nil {
		t.Fatal(err)
	}
	rep(wire.StatusDiffers, ch) // the new Target of the name
	if next := e.openIssue("web1", wire.GuestAddressID); next.ID == first.ID {
		t.Fatalf("Issue %d of the deleted Target was reopened for the new Target", first.ID)
	}
}
