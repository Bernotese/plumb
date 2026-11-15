package issue_test

import (
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/wire"
)

// D-418: Accept never takes a hand change of a more NIC into the document.
func TestAcceptRefusedForNICs(t *testing.T) {
	for _, attr := range []string{"nic1", "nic2", "nic3"} {
		ch := []wire.Change{{Attr: "cores", From: 2, To: 1, Risk: wire.RiskDisruptive},
			{Attr: attr, From: "bridge=vmbr9,tag=30,mac=02:00:00:00:00:01", To: "bridge=vmbr9,tag=20,mac=02:00:00:00:00:01", Risk: wire.RiskDisruptive}}
		if err := issue.AcceptRefused(wire.GuestConfigID, ch); !errors.Is(err, issue.ErrAcceptNIC) {
			t.Errorf("%s: %v", attr, err)
		}
	}
	// Not a NIC attribute, or not guest:config: no refusal from this rule.
	for _, c := range []struct{ resource, attr string }{{wire.GuestConfigID, "nic0"}, {wire.GuestConfigID, "nic4"}, {wire.GuestConfigID, "cores"}, {"file:/etc/nic1", "nic1"}} {
		if err := issue.AcceptRefused(c.resource, []wire.Change{{Attr: c.attr, From: "a", To: "b"}}); err != nil {
			t.Errorf("%s %s: %v", c.resource, c.attr, err)
		}
	}
}
