package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// unitRunner is a systemd whose units exist when their file exists under
// root. A new unit is stopped and disabled until enabled and started.
type unitRunner struct {
	root    string
	mu      sync.Mutex
	calls   [][]string
	running map[string]bool
	enabled map[string]bool
}

func (r *unitRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, args...))
	if name != "systemctl" || len(args) == 0 {
		return nil, fmt.Errorf("unexpected command %s %v", name, args)
	}
	unit := args[len(args)-1]
	switch args[0] {
	case "show":
		if _, err := os.Stat(filepath.Join(r.root, "etc/systemd/system", unit)); err != nil {
			return []byte("LoadState=not-found\nActiveState=inactive\nUnitFileState=\n"), nil
		}
		active, file := "inactive", "disabled"
		if r.running[unit] {
			active = "active"
		}
		if r.enabled[unit] {
			file = "enabled"
		}
		return fmt.Appendf(nil, "LoadState=loaded\nActiveState=%s\nUnitFileState=%s\n", active, file), nil
	case "start":
		r.running[unit] = true
	case "enable":
		r.enabled[unit] = true
	}
	return nil, nil
}

func (r *unitRunner) count(words ...string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if len(c) >= len(words) && slices.Equal(c[:len(words)], words) {
			n++
		}
	}
	return n
}

// T-103 (F2 of T-100): the Plan observes a service before its unit
// exists. One approved Plan makes the unit and starts the service; a
// difference that no earlier step made is still refused (D-075).
func TestApplyNewUnitAndService(t *testing.T) {
	const content = "[Service]\nExecStart=/bin/true\n\n[Install]\nWantedBy=multi-user.target\n"
	unit := wire.Resource{ID: "unit:app.service", Type: "unit", Block: "app", Attrs: map[string]any{"name": "app.service", "content": content}}
	conf := fileRes("app", "/srv/app.conf", map[string]any{"content": "x\n"})
	svc := wire.Resource{ID: "service:app.service", Type: "service", Block: "app",
		Attrs: map[string]any{"name": "app.service", "restart_on": []any{unit.ID}}}
	tests := []struct {
		name      string
		resources []wire.Resource
		before    func(root string) // the guest after the Plan, before the apply
		status    wire.Status       // of the service
	}{
		{name: "unit made by the apply", resources: []wire.Resource{unit, svc},
			status: wire.StatusApplied},
		{name: "unit made outside the Plan", resources: []wire.Resource{unit, svc},
			before: func(root string) {
				write(t, filepath.Join(root, "etc/systemd/system/app.service"), content)
			},
			status: wire.StatusRefused},
		{name: "only an unrelated file made", resources: []wire.Resource{conf, svc},
			before: func(root string) {
				write(t, filepath.Join(root, "etc/systemd/system/app.service"), content)
			},
			status: wire.StatusRefused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ca := newCA(t)
			ctl := newController(t, ca, "tok-1")
			var r *unitRunner
			ta := startAgentWith(t, ctl, ca, "tok-1", func(o *Options) {
				r = &unitRunner{root: o.Root, running: map[string]bool{}, enabled: map[string]bool{}}
				o.Runner = r
			})
			if err := os.MkdirAll(filepath.Join(ta.root, "etc/systemd/system"), 0o755); err != nil {
				t.Fatal(err)
			}
			// The Plan: the agent's own observe, before the unit exists.
			var planned []wire.Planned
			for _, it := range ta.a.observe(context.Background(), tt.resources) {
				if len(it.Changes) > 0 {
					planned = append(planned, wire.Planned{Resource: it.Resource, Changes: it.Changes})
				}
			}
			if len(planned) != 2 || planned[1].Changes[0].From != wire.StateAbsent {
				t.Fatalf("Plan = %+v", planned)
			}
			if tt.before != nil {
				tt.before(ta.root)
			}
			ctl.send(t, "a1", wire.CommandApply, wire.ApplyBody{Resources: tt.resources, Planned: planned, MaxRisk: wire.RiskDisruptive})
			rep := ctl.result(t, "a1")
			if rep.Error != "" || len(rep.Items) != 2 {
				t.Fatalf("result = %+v", rep)
			}
			if tt.status == wire.StatusApplied && rep.Items[0].Status != wire.StatusApplied {
				t.Fatalf("unit = %+v", rep.Items[0])
			}
			it := rep.Items[1]
			if it.Status != tt.status {
				t.Fatalf("service = %+v, want %s", it, tt.status)
			}
			started := r.count("systemctl", "start") + r.count("systemctl", "enable")
			if tt.status == wire.StatusApplied {
				if rep.Items[0].Status != wire.StatusApplied || started != 2 {
					t.Fatalf("items = %+v, start/enable calls = %d", rep.Items, started)
				}
				return
			}
			if !strings.Contains(it.Output, "differs from the approved Plan") || started != 0 {
				t.Fatalf("service = %+v, start/enable calls = %d", it, started)
			}
		})
	}
}
