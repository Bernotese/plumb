package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func TestBackupTargetOf(t *testing.T) {
	b := store.Binding{Kind: state.KindTarget, Name: "db1", Ref: "9001", Gen: 7}
	got, err := backupTargetOf(state.Doc{Kind: state.KindTarget, Name: "db1",
		Spec: []byte(`{"type":"lxc","guest":{},"backup":{"schedule":"weekly","weekday":"sun","time":"03:30","keep":4}}`)}, b)
	if err != nil || !got.LXC || got.Gen != 7 || got.Ref != "9001" || got.Backup == nil ||
		got.Backup.Hour != 3 || got.Backup.Minute != 30 || got.Backup.Weekday != "sun" || got.Backup.Keep != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	none, err := backupTargetOf(state.Doc{Kind: state.KindTarget, Name: "db1", Spec: []byte(`{"type":"vm","guest":{}}`)}, b)
	if err != nil || none.Backup != nil || none.LXC {
		t.Fatalf("%+v %v", none, err)
	}
	if _, err := backupTargetOf(state.Doc{Kind: state.KindTarget, Name: "db1", Spec: []byte(`{"type":"vm","backup":{"time":"3"}}`)}, b); err == nil {
		t.Fatal("a bad time passed")
	}
}

// T-221 (D-408, D-417): the Plan-only change of spec.backup: read, a lower
// keep destructive with the count, no field deletes nothing, and a new
// field carries the storage note.
func TestBackupChange(t *testing.T) {
	ctx := context.Background()
	asked := -1
	e := &Engine{cfg: Config{
		BackupKeepDeletes: func(_ context.Context, target string, keep int) (int, error) { asked = keep; return 4, nil },
		BackupStorageNote: func(context.Context) (string, error) { return "the storage keeps backups in clear", nil },
	}}
	r := func(b map[string]any) *state.Rendered { return &state.Rendered{Target: "db1", Backup: b} }
	daily := func(keep int) map[string]any {
		return map[string]any{"schedule": "daily", "time": "03:00", "keep": float64(keep)}
	}
	// New field: read, with the storage note.
	c, notes, err := e.backupChange(ctx, r(nil), r(daily(7)), "db1")
	if err != nil || c == nil || c.Risk != wire.RiskRead || c.To != "daily 03:00 keep 7" || len(notes) != 1 || !strings.Contains(notes[0], "clear") {
		t.Fatalf("new: %+v %v %v", c, notes, err)
	}
	// Same field: no change.
	if c, _, _ := e.backupChange(ctx, r(daily(7)), r(daily(7)), "db1"); c != nil {
		t.Fatalf("same: %+v", c)
	}
	// A higher keep: read, no note.
	if c, notes, _ := e.backupChange(ctx, r(daily(7)), r(daily(9)), "db1"); c == nil || c.Risk != wire.RiskRead || len(notes) != 0 {
		t.Fatalf("higher: %+v %v", c, notes)
	}
	// A lower keep: destructive, the count from the jobs.
	c, notes, _ = e.backupChange(ctx, r(daily(7)), r(daily(3)), "db1")
	if c == nil || c.Risk != wire.RiskDestructive || asked != 3 || len(notes) != 1 || !strings.Contains(notes[0], "deletes 4 backup") {
		t.Fatalf("lower: %+v %v asked %d", c, notes, asked)
	}
	// Removed: read, deletes nothing.
	c, notes, _ = e.backupChange(ctx, r(daily(7)), r(nil), "db1")
	if c == nil || c.Risk != wire.RiskRead || c.To != nil || len(notes) != 1 || !strings.Contains(notes[0], "deletes none") {
		t.Fatalf("removed: %+v %v", c, notes)
	}
	// driverChanges never gives it to Update.
	if got := driverChanges([]wire.Change{{Attr: AttrBackup, Risk: wire.RiskDestructive}}); len(got) != 0 {
		t.Fatalf("driver got %v", got)
	}
}
