package driver

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

func diskVM(disks ...Disk) GuestSpec {
	return GuestSpec{Kind: wire.KindVM, Cores: 1, MemoryMB: 512, DiskGB: 10, Bridge: "vmbr0", Disks: disks, DisksSet: true}
}

func TestDiskAttrAndValue(t *testing.T) {
	if DiskIndex("disk2") != 2 || DiskIndex("disk") != 0 || DiskIndex("disk4") != 0 || DiskIndex("disk02") != 0 || DiskIndex("nic1") != 0 {
		t.Fatal("DiskIndex")
	}
	if v := (&Disk{Slot: 1, SizeGB: 8}).Value(); v != "8 GiB" {
		t.Fatalf("vm value %v", v)
	}
	if v := (&Disk{Slot: 1, SizeGB: 8, Path: "/srv/data"}).Value(); v != "8 GiB at /srv/data" {
		t.Fatalf("ct value %v", v)
	}
	if v := (*Disk)(nil).Value(); v != nil {
		t.Fatalf("gap %v", v)
	}
	d, err := ParseDiskValue("8 GiB at /srv/data")
	if err != nil || d.SizeGB != 8 || d.Path != "/srv/data" {
		t.Fatalf("parse %v %v", d, err)
	}
	if _, err := ParseDiskValue("local-lvm:vm-1-disk-1 (not made by Plumb)"); err == nil {
		t.Fatal("a foreign value parsed")
	}
	if DiskSerial(2) != "plumb-2" {
		t.Fatal("serial")
	}
}

func TestDriftDisks(t *testing.T) {
	obs := GuestState{Spec: diskVM(Disk{Slot: 1, SizeGB: 4, Volume: "local-lvm:vm-9001-disk-1"}, Disk{Slot: 3, Foreign: true, Raw: "other:vm-9001-disk-7,size=2G"})}
	want := diskVM(Disk{Slot: 1, SizeGB: 6}, Disk{Slot: 2, SizeGB: 2})
	got := map[string]wire.Change{}
	for _, c := range DriftGuest(want, obs) {
		got[c.Attr] = c
	}
	if c := got["disk1"]; c.From != "4 GiB" || c.To != "6 GiB" || c.Risk != wire.RiskDisruptive {
		t.Errorf("grow %+v", c)
	}
	if c := got["disk2"]; c.From != nil || c.To != "2 GiB" || c.Risk != wire.RiskReversible {
		t.Errorf("add %+v", c)
	}
	if c := got["disk3"]; c.To != nil || c.Risk != wire.RiskDestructive || c.From != "other:vm-9001-disk-7,size=2G (not made by Plumb)" {
		t.Errorf("detach %+v", c)
	}
	// A removed Plumb disk is destructive.
	cs := DriftGuest(diskVM(), GuestState{Spec: diskVM(Disk{Slot: 2, SizeGB: 4})})
	if len(cs) != 1 || cs[0].Attr != "disk2" || cs[0].Risk != wire.RiskDestructive || cs[0].To != nil {
		t.Fatalf("remove %+v", cs)
	}
}

func TestDriftDisksUnmanaged(t *testing.T) {
	// A missing disks field manages nothing (D-436): no disk change.
	want := diskVM()
	want.DisksSet = false
	if cs := DriftGuest(want, GuestState{Spec: diskVM(Disk{Slot: 1, SizeGB: 4})}); len(cs) != 0 {
		t.Fatalf("unmanaged: %+v", cs)
	}
	// An adopted guest never compares disks.
	want = diskVM()
	want.Adopted = true
	if cs := DriftGuest(want, GuestState{Spec: diskVM(Disk{Slot: 1, SizeGB: 4})}); len(cs) != 0 {
		t.Fatalf("adopted: %+v", cs)
	}
}

func TestDriftDisksContainerMounts(t *testing.T) {
	ct := func(disks ...Disk) GuestSpec {
		s := diskVM(disks...)
		s.Kind = wire.KindLXC
		return s
	}
	foreign := Disk{Slot: 1, Foreign: true, Raw: "other:vm-9001-disk-3,mp=/x"}
	// Unmanaged: a foreign volume in mp1 stays the person's mount point.
	want := ct()
	want.DisksSet = false
	cs := DriftGuest(want, GuestState{Spec: ct(foreign)})
	if len(cs) != 1 || cs[0].Attr != AttrMounts {
		t.Fatalf("unmanaged foreign mp1: %+v", cs)
	}
	// Managed: it is a disk slot whose revert detaches it; no mounts drift.
	cs = DriftGuest(ct(), GuestState{Spec: ct(foreign)})
	if len(cs) != 1 || cs[0].Attr != "disk1" || cs[0].Risk != wire.RiskDestructive {
		t.Fatalf("managed foreign mp1: %+v", cs)
	}
	// A Plumb disk of an unmanaged container is no drift (accepted).
	if cs := DriftGuest(want, GuestState{Spec: ct(Disk{Slot: 1, SizeGB: 2, Path: "/srv"})}); len(cs) != 0 {
		t.Fatalf("plumb disk unmanaged: %+v", cs)
	}
}

func TestDiffDisksRefusals(t *testing.T) {
	obs := GuestState{Spec: diskVM(Disk{Slot: 1, SizeGB: 4}, Disk{Slot: 2, Foreign: true, Raw: "x"})}
	_, err := DiffGuest(diskVM(Disk{Slot: 1, SizeGB: 2}), obs)
	var se *ShrinkError
	if !errors.As(err, &se) || se.Slot != 1 || se.Have != 4 || se.Want != 2 {
		t.Fatalf("shrink: %v", err)
	}
	_, err = DiffGuest(diskVM(Disk{Slot: 1, SizeGB: 4}, Disk{Slot: 2, SizeGB: 4}), obs)
	var fe *DiskForeignError
	if !errors.As(err, &fe) || fe.Slot != 2 {
		t.Fatalf("foreign: %v", err)
	}
	ct := GuestState{Spec: diskVM(Disk{Slot: 1, SizeGB: 4, Path: "/srv/a"})}
	_, err = DiffGuest(diskVM(Disk{Slot: 1, SizeGB: 4, Path: "/srv/b"}), ct)
	var pe *DiskPathError
	if !errors.As(err, &pe) || pe.Slot != 1 {
		t.Fatalf("path: %v", err)
	}
	// A pending remove (a running container) waits for the restart: no drift.
	pend := GuestState{Spec: diskVM(Disk{Slot: 1, SizeGB: 4})}
	nx := diskVM()
	pend.Pending = &nx
	if cs, err := DiffGuest(diskVM(), pend); err != nil || len(cs) != 0 {
		t.Fatalf("pending remove: %+v %v", cs, err)
	}
}

// T-253 F3 (T-247 step V): a free that Proxmox refuses because the volume
// is still in use names the snapshot and warns against pvesm free.
func TestVolumeLeftSnapshotHint(t *testing.T) {
	inUse := &VolumeLeftError{Slot: 2, Volume: "local-lvm:vm-1-disk-2", Err: errors.New("unable to delete 'local-lvm:vm-1-disk-2' - volume is still in use (snapshot?)")}
	if !inUse.InUse() || !strings.Contains(inUse.Error(), "A snapshot may hold the volume: remove the snapshot first") || !strings.Contains(inUse.Error(), "Do not `pvesm free`") {
		t.Errorf("in use: %s", inUse.Error())
	}
	other := &VolumeLeftError{Slot: 2, Volume: "local-lvm:vm-1-disk-2", Err: errors.New("storage offline")}
	if other.InUse() || strings.Contains(other.Error(), "snapshot") || !strings.Contains(other.Error(), "`pvesm free local-lvm:vm-1-disk-2` when no unused line names it") {
		t.Errorf("other: %s", other.Error())
	}
}
