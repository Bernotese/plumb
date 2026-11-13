package plan

import (
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// The Plan names the window of a safe apply (D-293, D-300).
func TestShowConfirm(t *testing.T) {
	tp := TargetPlan{Resources: []ResourceChange{
		{Resource: "firewall:main", Changes: []wire.Change{{Attr: "policy", Risk: wire.RiskDisruptive}}},
		{Resource: "file:/x", Changes: []wire.Change{{Attr: "mode", Risk: wire.RiskReversible}}},
		{Resource: "firewall:main", Unverified: true},
		{Resource: "firewall:main"}, // no change
	}}
	showConfirm(&tp)
	for i, want := range []int{wire.SafeApplySeconds, 0, wire.SafeApplySeconds, 0} {
		if got := tp.Resources[i].ConfirmSeconds; got != want {
			t.Errorf("resource %d: confirm_seconds %d, want %d", i, got, want)
		}
	}
}
