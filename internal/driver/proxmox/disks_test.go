package proxmox

import (
	"reflect"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

func TestOwnVolume(t *testing.T) {
	for vol, want := range map[string]bool{
		"local-lvm:vm-9001-disk-1":           true,
		"dirst:9001/vm-9001-disk-2.raw":      true,
		"dirst:9001/vm-9001-disk-2.qcow2":    true,
		"local-lvm:vm-9002-disk-1":           false, // another owner
		"other:vm-9001-disk-1":               false, // another storage
		"local-lvm:base-9001-disk-1":         false,
		"local-lvm:vm-9001-cloudinit":        false,
		"local-lvm:vm-9001-disk-x":           false,
		"local-lvm:vm-90011-disk-1":          false,
		"dirst:9002/vm-9001-disk-1.raw":      false,
		"/dev/sdb":                           false,
		"local-lvm:vm-9001-disk-1/../escape": false,
	} {
		st := "local-lvm"
		if vol[:5] == "dirst" {
			st = "dirst"
		}
		if got := ownVolume(vol, st, 9001); got != want {
			t.Errorf("%s: %v", vol, got)
		}
	}
}

func TestWithDisksVM(t *testing.T) {
	cfg := map[string]string{
		"scsi0":   "local-lvm:vm-9001-disk-0,size=10G",
		"scsi1":   "local-lvm:vm-9001-disk-1,serial=plumb-1,size=4G",
		"scsi2":   "local-lvm:vm-9001-disk-2,size=1G", // no serial: a person's disk
		"scsi3":   "local:iso/x.iso,media=cdrom",      // a cdrom
		"unused0": "local-lvm:vm-9001-disk-5",
	}
	s := withDisks(driver.GuestSpec{Kind: wire.KindVM}, cfg, "local-lvm", 9001)
	want := []driver.Disk{
		{Slot: 1, SizeGB: 4, Volume: "local-lvm:vm-9001-disk-1"},
		{Slot: 2, Foreign: true, Raw: "local-lvm:vm-9001-disk-2,size=1G"},
		{Slot: 3, Foreign: true, Raw: "local:iso/x.iso,media=cdrom"},
	}
	if !reflect.DeepEqual(s.Disks, want) || !s.DisksSet || !reflect.DeepEqual(s.Unused, []string{"local-lvm:vm-9001-disk-5"}) {
		t.Fatalf("%+v", s)
	}
	// A serial of another slot is not Plumb's disk of this slot.
	s = withDisks(driver.GuestSpec{Kind: wire.KindVM}, map[string]string{"scsi1": "local-lvm:vm-9001-disk-1,serial=plumb-2,size=4G"}, "local-lvm", 9001)
	if len(s.Disks) != 1 || !s.Disks[0].Foreign {
		t.Fatalf("serial of another slot: %+v", s.Disks)
	}
}

func TestWithDisksCT(t *testing.T) {
	cfg := map[string]string{
		"mp0": "local-lvm:vm-9001-disk-1,mp=/srv/zero,size=1G", // mp0 is never a data disk
		"mp1": "local-lvm:vm-9001-disk-2,mp=/srv/data,backup=1,size=2G",
		"mp2": "/srv/host,mp=/host",                     // a bind mount
		"mp3": "other:vm-9001-disk-3,mp=/srv/o,size=1G", // another storage
	}
	base := driver.GuestSpec{Kind: wire.KindLXC, Mounts: []string{
		"mp0=" + cfg["mp0"], "mp1=" + cfg["mp1"], "mp2=" + cfg["mp2"], "mp3=" + cfg["mp3"]}}
	s := withDisks(base, cfg, "local-lvm", 9001)
	want := []driver.Disk{
		{Slot: 1, SizeGB: 2, Path: "/srv/data", Volume: "local-lvm:vm-9001-disk-2"},
		{Slot: 3, Foreign: true, Raw: "other:vm-9001-disk-3,mp=/srv/o,size=1G"},
	}
	if !reflect.DeepEqual(s.Disks, want) {
		t.Fatalf("disks %+v", s.Disks)
	}
	if !reflect.DeepEqual(s.Mounts, []string{"mp0=" + cfg["mp0"], "mp2=" + cfg["mp2"]}) {
		t.Fatalf("mounts %v", s.Mounts)
	}
}

func TestDiskValues(t *testing.T) {
	if v := vmDiskValue("local-lvm", 2, 8); v != "local-lvm:8,serial=plumb-2" {
		t.Fatal(v)
	}
	if v := ctDiskValue("local-lvm", 8, "/srv/data"); v != "local-lvm:8,mp=/srv/data,backup=1" {
		t.Fatal(v)
	}
}
