package proxmox

import (
	"context"
	"errors"
	"testing"
)

// T-241: the guest config of a backup, read with the token before a
// restore (T-244 R2).
func TestBackupConfig(t *testing.T) {
	f, d := backupFake(t)
	ctx := context.Background()
	vm := f.backup.addVol(9001, testMarker, 1)
	f.backup.vols[vm].config = "#a description line\nboot: order=scsi0\nnet0: virtio=02:00:00:00:00:01,bridge=vmbr0,firewall=1\n" +
		"net1: virtio=02:00:00:00:00:02,bridge=vmbr9,tag=20\nscsi1: local-lvm:vm-9001-disk-1,serial=plumb-1,size=8G\n" +
		"[snap1]\nnet2: virtio=02:00:00:00:00:03,bridge=vmbr7\n"
	got, err := d.BackupConfig(ctx, "plumb-backup", "9001", vm)
	if err != nil || got["net1"] != "virtio=02:00:00:00:00:02,bridge=vmbr9,tag=20" || got["scsi1"] == "" || got["net2"] != "" || len(got) != 4 {
		t.Fatalf("config %v %v", got, err)
	}
	if p := f.lastParams("GET /nodes/pve/vzdump/extractconfig"); p.Get("volume") != vm {
		t.Fatalf("params %v", p)
	}
	// Only a volume of the guest's own list.
	ct := f.backup.addVol(9002, testMarker, 1)
	if _, err := d.BackupConfig(ctx, "plumb-backup", "9001", ct); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("another guest's volume: %v", err)
	}
}

// T-244 R1: PVE refuses a restore without SDN.Use on a network of the
// backup inside its task, before it touches a disk: the driver says "not
// started", so no text says that the guest may have no disk.
func TestRestoreGuestSDNRefusalNotStarted(t *testing.T) {
	for exit, notStarted := range map[string]bool{
		"403 Permission check failed (/sdn/zones/localnetwork/vmbr9/20, SDN.Use)": true,
		"403 Permission check failed (/vms/9001, VM.Config.Disk)":                 false,
		"unable to restore VM 9001 - no space left":                               false,
	} {
		t.Run(exit, func(t *testing.T) {
			f, d := backupFake(t)
			vol := f.backup.addVol(9001, testMarker, 1)
			v := f.backup.vols[vol]
			f.vms[9001].running = false
			f.failNext["POST /nodes/pve/qemu"] = exit
			err := d.RestoreGuest(context.Background(), "plumb-backup", "9001", vol, v.size, v.ctime)
			if err == nil {
				t.Fatal("no error")
			}
			if got := errors.Is(err, ErrRestoreNotStarted); got != notStarted {
				t.Fatalf("not started = %v, want %v: %v", got, notStarted, err)
			}
			if notStarted && !errors.Is(err, ErrNetworkRefused) {
				t.Fatalf("not marked as a network refusal: %v", err)
			}
		})
	}
}
