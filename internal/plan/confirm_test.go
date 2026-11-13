package plan

import (
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"

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
	showSafeApply(&tp, nil)
	for i, want := range []int{wire.SafeApplySeconds, 0, wire.SafeApplySeconds, 0} {
		if got := tp.Resources[i].ConfirmSeconds; got != want {
			t.Errorf("resource %d: confirm_seconds %d, want %d", i, got, want)
		}
	}
}

// The Plan shows the effective SSH rule and the policy (D-300).
func TestSafeApplySummary(t *testing.T) {
	fw := func(attrs map[string]any) *state.Rendered {
		a := map[string]any{"name": "main"}
		for k, v := range attrs {
			a[k] = v
		}
		return &state.Rendered{Resources: []wire.Resource{{ID: "firewall:main", Type: "firewall", Block: "fw", Attrs: a}}}
	}
	for _, c := range []struct {
		attrs map[string]any
		want  []string
	}{
		{nil, []string{"SSH tcp/22 open from everywhere", "policy drop: other input is dropped"}},
		{map[string]any{"ssh_port": "2222", "ssh_sources": "192.0.2.0/24", "policy": "accept"},
			[]string{"SSH tcp/2222 open from 192.0.2.0/24", "policy accept: other input is accepted"}},
		{map[string]any{"allow_no_ssh": true}, []string{"no SSH rule (allow_no_ssh)", "policy drop: other input is dropped"}},
		{map[string]any{"state": "absent"}, nil},
	} {
		tp := TargetPlan{Resources: []ResourceChange{{Resource: "firewall:main", Unverified: true}}}
		showSafeApply(&tp, fw(c.attrs))
		if got := tp.Resources[0].Summary; strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%v: %q, want %q", c.attrs, got, c.want)
		}
	}
}
