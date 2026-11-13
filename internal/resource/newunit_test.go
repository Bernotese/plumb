package resource

import (
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// T-103: the Plan observed the service before its unit existed; an
// earlier step of the same apply made the unit.
func TestNewUnitCovers(t *testing.T) {
	svc := wire.Resource{ID: "service:app.service", Type: "service", Attrs: map[string]any{"name": "app.service"}}
	unitRes := wire.Resource{ID: "unit:app.service", Type: "unit", Attrs: map[string]any{"name": "app.service"}}
	otherUnit := wire.Resource{ID: "unit:other.service", Type: "unit", Attrs: map[string]any{"name": "other.service"}}
	unitFile := wire.Resource{ID: "file:/etc/systemd/system/app.service", Type: "file", Attrs: map[string]any{"path": "/etc/systemd/system/app.service"}}
	otherFile := wire.Resource{ID: "file:/srv/app.conf", Type: "file", Attrs: map[string]any{"path": "/srv/app.conf"}}
	pkg := wire.Resource{ID: "package:app", Type: "package", Attrs: map[string]any{"name": "app"}}

	rev := wire.RiskReversible
	restart := wire.Change{Attr: wire.AttrRestart, Risk: wire.RiskDisruptive}
	// What the Plan shows for an absent unit (service.Diff).
	planned := []wire.Change{
		{Attr: wire.AttrState, From: wire.StateAbsent, To: ServiceRunning, Risk: rev},
		{Attr: "enabled", From: nil, To: true, Risk: rev},
		restart,
	}
	// What the agent sees after the unit step made the unit.
	fresh := []wire.Change{
		{Attr: wire.AttrState, From: ServiceStopped, To: ServiceRunning, Risk: rev},
		{Attr: "enabled", From: false, To: true, Risk: rev},
		restart,
	}
	tests := []struct {
		name    string
		svc     wire.Resource
		planned []wire.Change
		now     []wire.Change
		made    []wire.Resource
		want    bool
	}{
		{"stopped service: only enable planned", svc, planned[1:2], fresh[1:2], []wire.Resource{unitRes}, true},
		{"stopped service: higher enable risk", svc, planned[1:2],
			[]wire.Change{{Attr: "enabled", From: false, To: true, Risk: wire.RiskDisruptive}}, []wire.Resource{unitRes}, false},
		{"stopped service: unplanned state", svc, planned[1:2], fresh[:2], []wire.Resource{unitRes}, false},
		{"stopped service: unrelated maker", svc, planned[1:2], fresh[1:2], []wire.Resource{otherUnit}, false},
		{"unit resource of the same name", svc, planned, fresh, []wire.Resource{unitRes}, true},
		{"unit file", svc, planned, fresh, []wire.Resource{unitFile}, true},
		{"package", svc, planned, fresh, []wire.Resource{pkg}, true},
		{"static unit: no enabled change", svc, planned, fresh[:1], []wire.Resource{unitRes}, true},
		{"package started it: only enabled", svc, planned, fresh[1:2], []wire.Resource{pkg}, true},
		{"service name without suffix", wire.Resource{ID: "service:app", Type: "service", Attrs: map[string]any{"name": "app"}},
			planned, fresh, []wire.Resource{unitRes}, true},
		{"nothing made", svc, planned, fresh, nil, false},
		{"another unit made", svc, planned, fresh, []wire.Resource{otherUnit}, false},
		{"another file made", svc, planned, fresh, []wire.Resource{otherFile}, false},
		{"not a service", unitRes, planned, fresh, []wire.Resource{unitRes}, false},
		{"Plan saw the unit (stopped, not absent)", svc,
			[]wire.Change{{Attr: wire.AttrState, From: ServiceStopped, To: ServiceRunning, Risk: rev}},
			[]wire.Change{{Attr: wire.AttrState, From: ServiceStopped, To: ServiceRunning, Risk: rev}, {Attr: "enabled", From: false, To: true, Risk: rev}},
			[]wire.Resource{unitRes}, false},
		{"new unit is masked", svc, planned,
			[]wire.Change{fresh[0], {Attr: "enabled", From: unitMasked, To: true, Risk: rev}}, []wire.Resource{unitRes}, false},
		{"other to", svc, planned,
			[]wire.Change{{Attr: wire.AttrState, From: ServiceStopped, To: "other", Risk: rev}}, []wire.Resource{unitRes}, false},
		{"higher risk", svc, planned,
			[]wire.Change{{Attr: wire.AttrState, From: ServiceStopped, To: ServiceRunning, Risk: wire.RiskDisruptive}}, []wire.Resource{unitRes}, false},
		{"restart not planned", svc, planned[:2], fresh, []wire.Resource{unitRes}, false},
		{"unplanned attribute", svc, planned,
			append(append([]wire.Change{}, fresh...), wire.Change{Attr: "other", From: 1, To: 2, Risk: rev}), []wire.Resource{unitRes}, false},
		{"planned enabled false is not the absent form", svc,
			[]wire.Change{planned[0], {Attr: "enabled", From: true, To: false, Risk: rev}},
			[]wire.Change{fresh[0], {Attr: "enabled", From: false, To: true, Risk: rev}}, []wire.Resource{unitRes}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewUnitCovers(tt.svc, tt.planned, tt.now, tt.made); got != tt.want {
				t.Fatalf("NewUnitCovers = %v, want %v", got, tt.want)
			}
		})
	}
}
