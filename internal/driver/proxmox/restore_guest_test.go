package proxmox

import (
	"context"
	"errors"
	"testing"
)

// T-222: a restore over a stopped pool guest with force=1, only a volume
// of that guest, never over a running guest.
func TestRestoreGuest(t *testing.T) {
	f, d := backupFake(t)
	ctx := context.Background()
	vm := f.backup.addVol(9001, testMarker, 1)
	other := f.backup.addVol(9002, testMarker, 2)
	if err := d.RestoreGuest(ctx, "plumb-backup", "9001", vm); !errors.Is(err, ErrGuestRunning) {
		t.Fatalf("running guest: %v", err)
	}
	f.vms[9001].running = false
	if err := d.RestoreGuest(ctx, "plumb-backup", "9001", other); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("a volume of another guest: %v", err)
	}
	if err := d.RestoreGuest(ctx, "plumb-backup", "9001", "plumb-backup:backup/none"); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("an unknown volume: %v", err)
	}
	if err := d.RestoreGuest(ctx, "plumb-backup", "9001", vm); err != nil {
		t.Fatal(err)
	}
	p := f.lastParams("POST /nodes/pve/qemu")
	if p.Get("archive") != vm || p.Get("force") != "1" || p.Get("storage") != d.cfg.DiskStorage || p.Get("vmid") != "9001" || len(p) != 4 {
		t.Errorf("VM restore params %v", p)
	}
	// A container: ostemplate, restore=1, unprivileged.
	f.vms[9002].running = false
	f.vms[9002].cfg["unprivileged"] = "1"
	if err := d.RestoreGuest(ctx, "plumb-backup", "9002", other); err != nil {
		t.Fatal(err)
	}
	p = f.lastParams("POST /nodes/pve/lxc")
	if p.Get("ostemplate") != other || p.Get("restore") != "1" || p.Get("unprivileged") != "1" || p.Get("force") != "1" || len(p) != 6 {
		t.Errorf("CT restore params %v", p)
	}
	if len(f.backup.restored) != 2 {
		t.Errorf("restored %v", f.backup.restored)
	}
	// A guest outside the pool is never touched.
	if err := d.RestoreGuest(ctx, "plumb-backup", "100", vm); err == nil {
		t.Error("a guest outside the pool")
	}
}
