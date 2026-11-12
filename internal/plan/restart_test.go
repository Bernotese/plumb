package plan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// TestRestartRule pins table C of T-056 (D-194), one case per row.
func TestRestartRule(t *testing.T) {
	mem := wire.Change{Attr: driver.AttrMemory, From: 1024, To: 2048, Risk: wire.RiskDisruptive}
	memWaits := mem
	memWaits.WaitsForRestart = true
	bridge := wire.Change{Attr: driver.AttrBridge, From: "vmbr0", To: "vmbr1", Risk: wire.RiskDisruptive}
	pending := wire.Change{Attr: driver.AttrCores, From: 8, To: 2, Risk: wire.RiskReversible, Pending: true}
	waiting := []wire.Change{{Attr: driver.AttrCores, From: 1, To: 2, Risk: wire.RiskRead, WaitsForRestart: true}}
	restart := wire.Change{Attr: wire.AttrRestart, Risk: wire.RiskDisruptive}
	run, stop := driver.PowerRunning, driver.PowerStopped

	cases := []struct {
		name     string
		changes  []wire.Change
		waiting  []wire.Change
		power    driver.Power
		allow    bool
		reenroll bool
		want     []wire.Change
		restart  bool
		err      bool
	}{
		{"stopped, false", []wire.Change{mem}, nil, stop, false, false, []wire.Change{mem}, false, false},
		{"stopped, true", []wire.Change{mem}, nil, stop, true, false, []wire.Change{mem}, false, false},
		{"stopped, re-enroll, false", []wire.Change{mem}, nil, stop, false, true, []wire.Change{mem}, false, false},
		{"running, re-enroll, true", []wire.Change{mem}, nil, run, true, true, []wire.Change{mem}, true, false},
		{"running, re-enroll, false", []wire.Change{mem}, nil, run, false, true, nil, false, true},
		{"running, true, memory", []wire.Change{mem}, nil, run, true, false, []wire.Change{mem, restart}, true, false},
		{"running, false, memory", []wire.Change{mem}, nil, run, false, false, []wire.Change{memWaits}, false, false},
		{"running, true, bridge only", []wire.Change{bridge}, nil, run, true, false, []wire.Change{bridge}, false, false},
		{"running, false, bridge only", []wire.Change{bridge}, nil, run, false, false, []wire.Change{bridge}, false, false},
		{"running, true, pending revert", []wire.Change{pending}, nil, run, true, false, []wire.Change{pending}, false, false},
		{"running, false, pending revert", []wire.Change{pending}, nil, run, false, false, []wire.Change{pending}, false, false},
		{"waits + true", nil, waiting, run, true, false, append(append([]wire.Change{}, waiting...), restart), true, false},
		{"waits + false", nil, waiting, run, false, false, nil, false, false},
		{"memory + waits + true", []wire.Change{mem}, waiting, run, true, false, []wire.Change{mem, waiting[0], restart}, true, false},
	}
	for _, c := range cases {
		got, restarts, err := restartRule(wire.KindVM, c.changes, c.waiting, c.power, c.allow, c.reenroll)
		if (err != nil) != c.err {
			t.Errorf("%s: err %v", c.name, err)
			continue
		}
		if c.err {
			if err.Error() != TextReenrollRefused {
				t.Errorf("%s: text %q", c.name, err)
			}
			continue
		}
		if restarts != c.restart || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v restart %v, want %+v restart %v", c.name, got, restarts, c.want, c.restart)
		}
	}
	// The input is not changed.
	in := []wire.Change{mem}
	restartRule(wire.KindVM, in, nil, run, false, false)
	if in[0].WaitsForRestart {
		t.Error("restartRule changed its input")
	}
	// A container takes cores and memory live (host C5): no restart, no
	// waiting flag, also with allow_restart. A re-enrollment still
	// restarts it, and still needs allow_restart when it runs.
	for _, allow := range []bool{false, true} {
		got, restarts, err := restartRule(wire.KindLXC, []wire.Change{mem}, nil, run, allow, false)
		if err != nil || restarts || !reflect.DeepEqual(got, []wire.Change{mem}) {
			t.Errorf("lxc allow=%v: %+v restart %v %v", allow, got, restarts, err)
		}
	}
	if _, restarts, err := restartRule(wire.KindLXC, nil, nil, run, true, true); err != nil || !restarts {
		t.Errorf("lxc re-enroll: restart %v %v", restarts, err)
	}
	if _, _, err := restartRule(wire.KindLXC, nil, nil, run, false, true); err == nil {
		t.Error("lxc re-enroll of a running container without allow_restart")
	}
	// The refusal names the two real ways.
	for _, part := range []string{"Stop the VM in Proxmox and re-enroll again", "set allow_restart first"} {
		if !strings.Contains(TextReenrollRefused, part) {
			t.Errorf("refusal text lacks %q", part)
		}
	}
}

func TestAllowRestartChange(t *testing.T) {
	off, on := &state.Rendered{}, &state.Rendered{AllowRestart: true}
	if c := allowRestartChange(off, on); c == nil || c.From != false || c.To != true || c.Risk != wire.RiskRead {
		t.Errorf("off → on: %+v", c)
	}
	if allowRestartChange(on, on) != nil || allowRestartChange(nil, on) != nil {
		t.Error("a change without a difference or for a new Target")
	}
	got := driverChanges([]wire.Change{*allowRestartChange(off, on), {Attr: wire.AttrRestart, Risk: wire.RiskDisruptive},
		{Attr: driver.AttrCores, From: 1, To: 2, Risk: wire.RiskRead, WaitsForRestart: true},
		{Attr: driver.AttrMemory, From: 1, To: 2, Risk: wire.RiskDisruptive}})
	if len(got) != 1 || got[0].Attr != driver.AttrMemory {
		t.Errorf("driverChanges = %+v", got)
	}
}

func TestForKind(t *testing.T) {
	if got := forKind(textReenrollRuns, wire.KindLXC); !strings.Contains(got, "Stop the container") || strings.Contains(got, "VM") {
		t.Errorf("lxc: %q", got)
	}
	if forKind(textReenrollRuns, wire.KindVM) != textReenrollRuns {
		t.Error("vm text changed")
	}
	if strings.Contains(textStartedOutsideCT, "smbios1") || strings.Contains(textStartedOutsideCT, "VM") {
		t.Error("container text names the VM")
	}
}
