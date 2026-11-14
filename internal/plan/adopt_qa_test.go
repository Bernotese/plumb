package plan

import (
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
)

// T-195 (mutant D9, D-386): on a bound Target, spec.adopted can be neither
// added (a Plumb-made guest would lose its create-only checks, and its
// delete would only unbind) nor removed (an adopted guest would be treated
// as Plumb's). Only a new adoption or a new create changes it.
func TestAdoptQAAdoptedCannotAppearOrGo(t *testing.T) {
	ad := &state.Adopted{Node: "pve", VMID: 100}
	other := &state.Adopted{Node: "pve", VMID: 101}
	for _, c := range []struct {
		name      string
		before, r *state.Rendered
		ok        bool
	}{
		{"added on a bound Target", &state.Rendered{}, &state.Rendered{Adopted: ad}, false},
		{"removed from a bound Target", &state.Rendered{Adopted: ad}, &state.Rendered{}, false},
		{"another VMID", &state.Rendered{Adopted: ad}, &state.Rendered{Adopted: other}, false},
		{"unchanged", &state.Rendered{Adopted: ad}, &state.Rendered{Adopted: &state.Adopted{Node: "pve", VMID: 100, DeleteGuest: true}}, true},
		{"never adopted", &state.Rendered{}, &state.Rendered{}, true},
	} {
		err := checkAdoptedChange("wiki", c.before, c.r, "100")
		if c.ok != (err == nil) || (err != nil && !errors.Is(err, ErrAdoptedChange)) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}
