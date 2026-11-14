package proxmox

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
)

const testMarker = "plumb:6f1c2a9e-3b4d-4e5f-8a7b-0c1d2e3f4a5b:db1:0123456789abcdef"

func backupFake(t *testing.T) (*fakePVE, *Driver) {
	t.Helper()
	f := newFakePVE(t)
	f.backup = newFakeBackup()
	f.addVM(9001, fakePool, map[string]string{"name": "db1"}, true)
	f.addCT(9002, fakePool, map[string]string{"hostname": "ct1"}, true)
	return f, f.driver(t)
}

// T-221: one vzdump with the marker; the new volume is found; no option
// that needs more rights is ever sent (D-405).
func TestBackUpGuest(t *testing.T) {
	f, d := backupFake(t)
	ctx := context.Background()
	old := f.backup.addVol(9001, "made by a person", 1)
	res, err := d.BackUpGuest(ctx, "plumb-backup", "9001", testMarker, []string{"/etc/plumb-agent"})
	if err != nil || res.Warned || res.Volid == "" || res.Volid == old {
		t.Fatalf("%+v %v", res, err)
	}
	p := f.lastParams("POST /nodes/pve/vzdump")
	if p.Get("mode") != "snapshot" || p.Get("notes-template") != testMarker || p.Has("exclude-path") {
		t.Fatalf("params %v (a VM gets no exclude-path)", p)
	}
	if len(f.backup.forbidden) != 0 {
		t.Fatalf("forbidden options sent: %v", f.backup.forbidden)
	}
	vols, err := d.ListGuestBackups(ctx, "plumb-backup", "9001")
	if err != nil || len(vols) != 2 {
		t.Fatalf("%+v %v", vols, err)
	}
	i := slices.IndexFunc(vols, func(v BackupVolume) bool { return v.Volid == res.Volid })
	if i < 0 || vols[i].Notes != testMarker {
		t.Fatalf("marker not listed: %+v", vols)
	}
	// A container: the agent paths are left out (L1 of #321).
	if _, err := d.BackUpGuest(ctx, "plumb-backup", "9002", testMarker, []string{"/etc/plumb-agent", "/var/lib/plumb-agent"}); err != nil {
		t.Fatal(err)
	}
	if got := f.lastParams("POST /nodes/pve/vzdump")["exclude-path"]; len(got) != 2 {
		t.Fatalf("exclude-path %v", got)
	}
}

// A notes failure is only a warning in PVE: the result says so and the
// caller reads the marker back.
func TestBackUpGuestNotesWarning(t *testing.T) {
	f, d := backupFake(t)
	f.backup.notesFail = true
	res, err := d.BackUpGuest(context.Background(), "plumb-backup", "9001", testMarker, nil)
	if err != nil || !res.Warned {
		t.Fatalf("%+v %v", res, err)
	}
	vols, _ := d.ListGuestBackups(context.Background(), "plumb-backup", "9001")
	if len(vols) != 1 || vols[0].Notes != "" {
		t.Fatalf("%+v", vols)
	}
	// A real failure stays an error.
	f.backup.failVzdump = "job errors"
	if _, err := d.BackUpGuest(context.Background(), "plumb-backup", "9001", testMarker, nil); err == nil {
		t.Fatal("a failed vzdump passed")
	}
}

func TestBackUpGuestRefusals(t *testing.T) {
	f, d := backupFake(t)
	ctx := context.Background()
	f.addVM(9050, "other", map[string]string{"name": "x"}, true)
	if _, err := d.BackUpGuest(ctx, "plumb-backup", "9050", testMarker, nil); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("a guest outside the pool: %v", err)
	}
	if _, err := d.BackUpGuest(ctx, "plumb-backup", "9001", "plumb:{{guestname}}", nil); err == nil {
		t.Fatal("notes with a template name passed")
	}
	if n := f.count("POST /nodes/pve/vzdump"); n != 0 {
		t.Fatalf("%d vzdump calls", n)
	}
}

// Delete only a volume of this guest.
func TestDeleteGuestBackup(t *testing.T) {
	f, d := backupFake(t)
	ctx := context.Background()
	other := f.backup.addVol(9002, testMarker, 1)
	if err := d.DeleteGuestBackup(ctx, "plumb-backup", "9001", other); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("a volume of another VMID: %v", err)
	}
	own := f.backup.addVol(9001, testMarker, 2)
	if err := d.DeleteGuestBackup(ctx, "plumb-backup", "9001", own); err != nil {
		t.Fatal(err)
	}
	if f.backup.vols[own] != nil || f.backup.vols[other] == nil {
		t.Fatal("wrong volume deleted")
	}
}

// The storage state from the index (T-214 FACT 2) and the second-storage
// list (M2 of #321).
func TestBackupStorageInfo(t *testing.T) {
	f, d := backupFake(t)
	ctx := context.Background()
	st, err := d.BackupStorageInfo(ctx, "plumb-backup")
	if err != nil || st.Encrypted || st.Type != "dir" || st.FreePct != 60 || !st.Backup {
		t.Fatalf("%+v %v", st, err)
	}
	f.backup.typ, f.backup.encKey = "pbs", "aa:bb:cc"
	if st, _ := d.BackupStorageInfo(ctx, "plumb-backup"); !st.Encrypted {
		t.Fatalf("pbs with a key: %+v", st)
	}
	f.backup.typ, f.backup.encKey = "dir", "aa:bb"
	if st, _ := d.BackupStorageInfo(ctx, "plumb-backup"); st.Encrypted {
		t.Fatal("a dir is never encrypted by a key field")
	}
	if _, err := d.BackupStorageInfo(ctx, "nope"); !errors.Is(err, ErrNoBackupStorage) {
		t.Fatalf("unknown storage: %v", err)
	}
	f.backup.others = []storageEntry{{Storage: "plumb-snippets", Type: "dir", Content: "snippets,backup"}, {Storage: "local-lvm", Type: "lvmthin", Content: "images,rootdir"}}
	if o, _ := d.OtherBackupStorages(ctx, "plumb-backup"); !slices.Equal(o, []string{"plumb-snippets"}) {
		t.Fatalf("others %v", o)
	}
}
