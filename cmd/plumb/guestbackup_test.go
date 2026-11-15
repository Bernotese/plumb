package main

import (
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
)

func TestGuestTargetOf(t *testing.T) {
	g := guestTargetOf(plan.BackupTarget{Name: "db1", Ref: "9001", Gen: 3, LXC: true,
		Backup: &plan.BackupField{Schedule: "weekly", Weekday: "wed", Hour: 4, Minute: 5, Keep: 2}})
	if g.Name != "db1" || g.Guest != "9001" || g.Gen != 3 || !g.LXC || g.Backup.Weekday != time.Wednesday || g.Backup.Minute != 5 || g.Backup.Keep != 2 {
		t.Fatalf("%+v %+v", g, g.Backup)
	}
	// serve wires these (after T-218); until then this test is their user.
	if one, all := guestTargets(nil); one == nil || all == nil {
		t.Fatal("no funcs")
	}
	if guestTargetOf(plan.BackupTarget{Name: "x"}).Backup != nil {
		t.Fatal("no backup field became a schedule")
	}
}
