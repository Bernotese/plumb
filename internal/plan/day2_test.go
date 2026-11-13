package plan

import (
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

func TestRevertShrinks(t *testing.T) {
	for _, tc := range []struct {
		ch   []wire.Change
		want bool
	}{
		{[]wire.Change{{Attr: driver.AttrDisk, From: 50, To: 10}}, true},
		{[]wire.Change{{Attr: driver.AttrDisk, From: 50.0, To: 10.0}}, true}, // a JSON round trip
		{[]wire.Change{{Attr: driver.AttrDisk, From: 10, To: 50}}, false},
		{[]wire.Change{{Attr: driver.AttrCores, From: 8, To: 2}}, false},
		{nil, false},
	} {
		if got := RevertShrinks(tc.ch); got != tc.want {
			t.Errorf("%+v: %v", tc.ch, got)
		}
	}
}

func TestRulePower(t *testing.T) {
	run := driver.GuestState{Power: driver.PowerRunning}
	if rulePower(driver.GuestSpec{Power: driver.PowerStopped}, run, false) != driver.PowerStopped {
		t.Error("a guest that the Run stops never restarts for a waiting value")
	}
	if rulePower(driver.GuestSpec{Power: driver.PowerStopped}, run, true) != driver.PowerRunning {
		t.Error("a re-enrollment keeps its own rule")
	}
	if rulePower(driver.GuestSpec{}, run, false) != driver.PowerRunning {
		t.Error("unmanaged power: the observed state")
	}
}
