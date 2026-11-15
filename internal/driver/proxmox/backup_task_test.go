package proxmox

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// T-248: the vzdump task names its own volume. BackUpGuest gives the UPID
// to onTask before the wait; GuestBackupTask finds the volume by the task
// log and only for a vzdump task of this guest.
func TestGuestBackupTask(t *testing.T) {
	f, d := backupFake(t)
	ctx := context.Background()
	var seen string
	res, err := d.BackUpGuest(ctx, "plumb-backup", "9001", testMarker, nil, func(u string) { seen = u })
	if err != nil || res.UPID == "" || seen != res.UPID {
		t.Fatalf("%+v %v, onTask %q", res, err, seen)
	}
	task, err := d.GuestBackupTask(ctx, "plumb-backup", "9001", res.UPID)
	if err != nil || task.Running || !task.OK || task.Volid != res.Volid {
		t.Fatalf("task %+v %v, want the volume %s", task, err, res.Volid)
	}
	// The task of another guest, and a task that is no vzdump, prove nothing.
	if _, err := d.GuestBackupTask(ctx, "plumb-backup", "9002", res.UPID); !errors.Is(err, ErrNotGuestTask) {
		t.Errorf("other guest: %v", err)
	}
	for _, u := range []string{"UPID:pve:00000001:00000001:00000001:qmstart:9001:root@pam:", "UPID:other:00000001:00000001:00000001:vzdump:9001:root@pam:", "garbage"} {
		if _, err := d.GuestBackupTask(ctx, "plumb-backup", "9001", u); !errors.Is(err, ErrNotGuestTask) {
			t.Errorf("%s: %v", u, err)
		}
	}
	// A failed vzdump: not OK, no volume; the UPID still comes back.
	f.backup.failVzdump = "job errors"
	res2, err := d.BackUpGuest(ctx, "plumb-backup", "9001", testMarker, nil, nil)
	if err == nil || res2.UPID == "" {
		t.Fatalf("failed vzdump: %+v %v", res2, err)
	}
	task, err = d.GuestBackupTask(ctx, "plumb-backup", "9001", res2.UPID)
	if err != nil || task.OK || task.Volid != "" {
		t.Fatalf("failed task %+v %v", task, err)
	}
}

func TestArchiveOfAndNames(t *testing.T) {
	for line, want := range map[string]string{
		"INFO: creating vzdump archive '/var/lib/vz/dump/vzdump-qemu-101-2026_11_15-10_00_00.vma.zst'": "/var/lib/vz/dump/vzdump-qemu-101-2026_11_15-10_00_00.vma.zst",
		"INFO: creating Proxmox Backup Server archive 'vm/101/2026-11-15T10:00:00Z'":                   "vm/101/2026-11-15T10:00:00Z",
		"INFO: creating vzdump archive ''":     "",
		"creating vzdump archive '/x/y.vma'":   "",
		"INFO: starting new backup job":        "",
		"INFO: creating archive '/x/y.vma":     "",
		"WARN: creating vzdump archive '/a/b'": "",
	} {
		got, ok := archiveOf(line)
		if got != want || ok != (want != "") {
			t.Errorf("%q: %q %v, want %q", line, got, ok, want)
		}
	}
	for _, c := range []struct {
		archive, volid string
		want           bool
	}{
		{"/var/lib/vz/dump/vzdump-qemu-101-a.vma.zst", "local:backup/vzdump-qemu-101-a.vma.zst", true},
		{"/var/lib/vz/dump/xvzdump-qemu-101-a.vma.zst", "local:backup/vzdump-qemu-101-a.vma.zst", false},
		{"vm/101/2026-11-15T10:00:00Z", "pbs:backup/vm/101/2026-11-15T10:00:00Z", true},
		{"ct/101/2026-11-15T10:00:00Z", "pbs:backup/vm/101/2026-11-15T10:00:00Z", false},
		{"/dump/2026-11-15T10:00:00Z", "pbs:backup/vm/101/2026-11-15T10:00:00Z", false},
		{"/dump/a.vma", "nocolon", false},
		{"/dump/", "local:backup/", false},
	} {
		if got := archiveNames(c.archive, c.volid); got != c.want {
			t.Errorf("archiveNames(%q, %q) = %v", c.archive, c.volid, got)
		}
	}
}

// M2 of #368: the proof needs exactly one archive line and exactly one
// volume for it. A guest that injects a second "creating … archive" line
// (tar output, a guest-agent text) only stops the claim.
func TestGuestBackupTaskExactlyOne(t *testing.T) {
	f, d := backupFake(t)
	ctx := context.Background()
	res, err := d.BackUpGuest(ctx, "plumb-backup", "9001", testMarker, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	other := f.backup.addVol(9001, testMarker, 1)
	_, otherFile, _ := strings.Cut(other, ":backup/")
	f.tasks[res.UPID].log = append(f.tasks[res.UPID].log, "INFO: creating vzdump archive '/mnt/pve/plumb-backup/dump/"+otherFile+"'")
	if task, err := d.GuestBackupTask(ctx, "plumb-backup", "9001", res.UPID); err != nil || !task.OK || task.Volid != "" {
		t.Fatalf("two archive lines: %+v %v", task, err)
	}
	// One archive line that names two volumes of the guest.
	f.backup.vols["plumb-backup:backup/a.vma.zst"] = &fakeVol{vmid: 9001, ctime: 1, size: 1, notes: testMarker}
	f.backup.vols["plumb-backup:backup/sub/a.vma.zst"] = &fakeVol{vmid: 9001, ctime: 1, size: 1, notes: testMarker}
	f.tasks[res.UPID].log = []string{"INFO: creating Proxmox Backup Server archive 'sub/a.vma.zst'"}
	if task, err := d.GuestBackupTask(ctx, "plumb-backup", "9001", res.UPID); err != nil || task.Volid != "" {
		t.Fatalf("two matching volumes: %+v %v", task, err)
	}
	// L1 of #368: only a task of Plumb's token (user@realm!name, or its user).
	f.tasks[res.UPID].log = []string{"INFO: creating vzdump archive '/d/" + strings.TrimPrefix(res.Volid, "plumb-backup:backup/") + "'"}
	head := strings.TrimSuffix(res.UPID, "plumb@pve!plumb:")
	for user, ok := range map[string]bool{"plumb@pve!plumb": true, "plumb@pve": true, "root@pam": false, "plumb@pve!other": false, "": false} {
		u := head + user + ":"
		f.tasks[u] = f.tasks[res.UPID]
		task, err := d.GuestBackupTask(ctx, "plumb-backup", "9001", u)
		if ok && (err != nil || task.Volid != res.Volid) || !ok && !errors.Is(err, ErrNotGuestTask) {
			t.Errorf("user %q: %+v %v", user, task, err)
		}
	}
}
