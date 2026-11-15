package proxmox

import (
	"context"
	"errors"
	"strconv"
	"strings"
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

const sdnExit = "403 Permission check failed (/sdn/zones/localnetwork/vmbr9/20, SDN.Use)"

// H1 of #369: only PVE's exact end status counts, never a log line (a
// container restore logs file names from the guest's archive).
func TestSDNRefusedExactExitStatus(t *testing.T) {
	for _, c := range []struct {
		te   TaskError
		want bool
	}{
		{TaskError{ExitStatus: sdnExit}, true},
		{TaskError{ExitStatus: "403 Permission check failed (/sdn/zones/localnetwork/vmbr9, SDN.Use)"}, true},
		{TaskError{ExitStatus: "unable to restore", Log: []string{"TASK ERROR: " + sdnExit}}, false},
		{TaskError{ExitStatus: "x: " + sdnExit}, false},
		{TaskError{ExitStatus: sdnExit + " and more"}, false},
		{TaskError{ExitStatus: "403 Permission check failed (/sdn/zones/localnetwork/vmbr9, SDN.Audit)"}, false},
		{TaskError{ExitStatus: "403 Permission check failed (/vms/9001, SDN.Use)"}, false},
		{TaskError{ExitStatus: "403 Permission check failed (/sdn/zones/a b, SDN.Use)"}, false},
	} {
		if got := sdnRefused(&c.te); got != c.want {
			t.Errorf("%+v: %v, want %v", c.te, got, c.want)
		}
	}
}

// T-244 R1: PVE refuses a VM restore without SDN.Use in its task, before
// it touches a disk. "Not started" needs the exact end status, a VM, and
// the VM's disk volumes read back unchanged; a container, a changed disk
// and any other failure stay on the normal failure path ("may have no
// disk").
func TestRestoreGuestSDNRefusalNotStarted(t *testing.T) {
	for _, c := range []struct {
		name       string
		guest      int
		exit       string
		wipes      bool
		notStarted bool
	}{
		{"VM, SDN refusal", 9001, sdnExit, false, true},
		{"VM, SDN refusal, disk gone", 9001, sdnExit, true, false},
		{"container, SDN refusal", 9002, sdnExit, false, false},
		{"VM, other 403", 9001, "403 Permission check failed (/vms/9001, VM.Config.Disk)", false, false},
		{"VM, no space", 9001, "unable to restore VM 9001 - no space left", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, d := backupFake(t)
			vol := f.backup.addVol(c.guest, testMarker, 1)
			v := f.backup.vols[vol]
			f.vms[c.guest].running = false
			if f.vms[c.guest].ct {
				f.vms[c.guest].cfg["unprivileged"] = "1"
			}
			if f.vms[c.guest].cfg["scsi0"] == "" && !f.vms[c.guest].ct {
				f.vms[c.guest].cfg["scsi0"] = "local-lvm:vm-9001-disk-0,size=4G"
			}
			f.backup.restoreFail, f.backup.restoreWipes = c.exit, c.wipes
			err := d.RestoreGuest(context.Background(), "plumb-backup", strconv.Itoa(c.guest), vol, v.size, v.ctime)
			if err == nil {
				t.Fatal("no error")
			}
			if got := errors.Is(err, ErrRestoreNotStarted); got != c.notStarted {
				t.Fatalf("not started = %v, want %v: %v", got, c.notStarted, err)
			}
			if c.exit == sdnExit && !strings.Contains(err.Error(), "SDN.Use") {
				t.Fatalf("the restore task did not run: %v", err)
			}
			if c.notStarted != errors.Is(err, ErrNetworkRefused) {
				t.Fatalf("network refusal mark: %v", err)
			}
		})
	}
}
