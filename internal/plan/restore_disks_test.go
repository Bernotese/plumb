package plan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

type cfgReader struct {
	GuestRestorer
	cfg map[string]string
	err error
}

func (c cfgReader) BackupConfig(context.Context, string, RestoreVolume) (map[string]string, error) {
	return c.cfg, c.err
}

// T-238: by type, an LXC loses a data disk that the backup lacks; a
// failed read says that the Plan cannot tell.
func TestRestoreDiskChanges(t *testing.T) {
	st := driver.GuestState{Spec: driver.GuestSpec{Kind: wire.KindLXC, Disks: []driver.Disk{
		{Slot: 1, SizeGB: 2, Path: "/srv/a", Volume: "local-lvm:vm-9-disk-1"},
		{Slot: 2, SizeGB: 1, Path: "/srv/b", Volume: "local-lvm:vm-9-disk-2"},
	}, Unused: []string{"local-lvm:vm-9-disk-7"}}}
	e := &Engine{cfg: Config{GuestRestore: cfgReader{cfg: map[string]string{"rootfs": "x", "mp1": "y"}}}}
	ch, notes := e.restoreDiskChanges(context.Background(), store.Binding{Ref: "9"}, RestoreVolume{}, st, nil)
	if len(ch) != 1 || ch[0].Attr != "disk2" || ch[0].To != "lost: not in the backup" || ch[0].Risk != wire.RiskDestructive {
		t.Fatalf("changes %+v", ch)
	}
	if n := strings.Join(notes, " "); !strings.Contains(n, "data on disk 2 is lost") || !strings.Contains(n, "local-lvm:vm-9-disk-7") {
		t.Fatalf("notes %q", notes)
	}
	e.cfg.GuestRestore = cfgReader{err: errors.New("403")}
	if ch, notes := e.restoreDiskChanges(context.Background(), store.Binding{Ref: "9"}, RestoreVolume{}, st, nil); len(ch) != 0 || len(notes) != 1 || !strings.Contains(notes[0], "could not read") {
		t.Fatalf("read error: %+v %q", ch, notes)
	}
	// No data disk: nothing to say.
	if ch, notes := e.restoreDiskChanges(context.Background(), store.Binding{}, RestoreVolume{}, driver.GuestState{}, nil); ch != nil || notes != nil {
		t.Fatal("notes without disks")
	}
}

// L2 of #373: a VM disk the backup lacks gets "a new empty disk" only
// when the document names its slot.
func TestRestoreDiskNoteNamedSlot(t *testing.T) {
	st := driver.GuestState{Spec: driver.GuestSpec{Kind: wire.KindVM, Disks: []driver.Disk{{Slot: 2, SizeGB: 1, Volume: "local-lvm:vm-9-disk-2"}}}}
	e := &Engine{cfg: Config{GuestRestore: cfgReader{cfg: map[string]string{"scsi0": "x"}}}}
	_, notes := e.restoreDiskChanges(context.Background(), store.Binding{}, RestoreVolume{}, st, nil)
	if n := strings.Join(notes, " "); strings.Contains(n, "new empty disk") || !strings.Contains(n, "kept as an unused volume") {
		t.Errorf("unmanaged: %q", notes)
	}
	_, notes = e.restoreDiskChanges(context.Background(), store.Binding{}, RestoreVolume{}, st, map[int]bool{2: true})
	if n := strings.Join(notes, " "); !strings.Contains(n, "new empty disk in slot 2") {
		t.Errorf("named: %q", notes)
	}
}
