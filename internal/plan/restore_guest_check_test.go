package plan

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/store"
)

// oneVolume is a GuestRestorer that always says plumb for one volume.
type oneVolume struct{ v RestoreVolume }

func (o oneVolume) FindBackup(context.Context, string, string, string) (RestoreVolume, error) {
	return o.v, nil
}
func (oneVolume) RestoreGuest(context.Context, string, RestoreVolume) error { return nil }
func (oneVolume) SafetyBackup(context.Context, string) (RestoreVolume, error) {
	return RestoreVolume{}, nil
}
func (oneVolume) DropSafetyBackups(context.Context, string, string) error { return nil }

// T-229: restoreVolume checks every field against the recorded row itself;
// one field off is refused, the right volume passes.
func TestRestoreVolumeSecondCheck(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.PutBinding(ctx, store.Binding{Kind: "Target", Name: "db1", Ref: "9001"})
	b, _ := st.GetBinding(ctx, "Target", "db1")
	lineage, _ := st.BackupLineage(ctx, "db1", b.Gen)
	at := time.Date(2026, 11, 15, 3, 0, 12, 0, time.UTC)
	id1, _ := st.StartGuestBackupRun(ctx, "db1", lineage, "scheduled")
	st.FinishGuestBackupRun(ctx, id1, "done", "bk:s", 100, at.Unix(), "")
	id2, _ := st.StartGuestBackupRun(ctx, "db1", lineage, "manual")
	st.FinishGuestBackupRun(ctx, id2, "done", "bk:m", 200, at.Unix()+60, "")
	good := RestoreVolume{ID: "3f2a9c0d1e4b5a67", Volid: "bk:s", Kind: "plumb", Notes: "plumb:iid:db1:" + lineage, Time: at, Size: 100}
	manual := RestoreVolume{ID: "3f2a9c0d1e4b5a68", Volid: "bk:m", Kind: "plumb", Notes: "plumb:iid:db1:" + lineage + ":m", Time: at.Add(time.Minute), Size: 200}
	check := func(v RestoreVolume) error {
		e := &Engine{st: st, cfg: Config{InstallID: "iid", GuestRestore: oneVolume{v}}}
		_, err := e.restoreVolume(ctx, "db1", b, v.ID)
		return err
	}
	for name, v := range map[string]RestoreVolume{"scheduled": good, "manual": manual} {
		if err := check(v); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, change := range map[string]func(v *RestoreVolume){
		"install":          func(v *RestoreVolume) { v.Notes = "plumb:other:db1:" + lineage },
		"Target":           func(v *RestoreVolume) { v.Notes = "plumb:iid:db2:" + lineage },
		"lineage":          func(v *RestoreVolume) { v.Notes = "plumb:iid:db1:0123456789abcdef" },
		"trigger":          func(v *RestoreVolume) { v.Notes += ":m" },
		"size":             func(v *RestoreVolume) { v.Size++ },
		"time":             func(v *RestoreVolume) { v.Time = v.Time.Add(time.Second) },
		"not recorded":     func(v *RestoreVolume) { v.Volid = "bk:other" },
		"manual's trigger": func(v *RestoreVolume) { *v = manual; v.Notes = "plumb:iid:db1:" + lineage },
		"suffix":           func(v *RestoreVolume) { v.Notes += ":x" },
	} {
		v := good
		change(&v)
		if err := check(v); !errors.Is(err, ErrNotRestorable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Deleted, then made again under the same name.
	if err := st.MarkGuestBackupDeleted(ctx, "db1", "bk:s"); err != nil {
		t.Fatal(err)
	}
	if err := check(good); !errors.Is(err, ErrNotRestorable) {
		t.Errorf("deleted: %v", err)
	}
	// No install id in the engine: nothing is restorable, also a marker
	// with an empty install field.
	empty := manual
	empty.Notes = "plumb::db1:" + lineage + ":m"
	e := &Engine{st: st, cfg: Config{GuestRestore: oneVolume{empty}}}
	if _, err := e.restoreVolume(ctx, "db1", b, empty.ID); !errors.Is(err, ErrNotRestorable) {
		t.Errorf("no install id: %v", err)
	}
}
