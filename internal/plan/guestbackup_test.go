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
	// New field, nothing to delete: read, with the storage note.
	deletes := 0
	e.cfg.BackupKeepDeletes = func(_ context.Context, _ string, keep int) (int, error) { asked = keep; return deletes, nil }
	c, notes, err := e.backupChange(ctx, r(nil), r(daily(7)), "db1", false)
	if err != nil || c == nil || c.Risk != wire.RiskRead || c.To != "daily 03:00 keep 7" || len(notes) != 1 || !strings.Contains(notes[0], "clear") {
		t.Fatalf("new: %+v %v %v", c, notes, err)
	}
	// M1 of #334: the field comes back after a removal with keep 1 and
	// old backups exist: destructive, by the count, not by the old keep.
	deletes = 6
	c, notes, _ = e.backupChange(ctx, r(nil), r(daily(1)), "db1", false)
	if c == nil || c.Risk != wire.RiskDestructive || !strings.Contains(strings.Join(notes, " "), "deletes 6 backup") {
		t.Fatalf("re-added: %+v %v", c, notes)
	}
	deletes = 4
	// Same field: no change.
	if c, _, _ := e.backupChange(ctx, r(daily(7)), r(daily(7)), "db1", false); c != nil {
		t.Fatalf("same: %+v", c)
	}
	// A higher keep that deletes nothing: read, no note.
	deletes = 0
	if c, notes, _ := e.backupChange(ctx, r(daily(7)), r(daily(9)), "db1", false); c == nil || c.Risk != wire.RiskRead || len(notes) != 0 {
		t.Fatalf("higher: %+v %v", c, notes)
	}
	deletes = 4
	// A lower keep: destructive, the counts from the jobs (T-233: the
	// difference to the old keep's count).
	e.cfg.BackupKeepDeletes = func(_ context.Context, _ string, keep int) (int, error) {
		if keep == 3 {
			asked = keep
			return 4, nil
		}
		return 0, nil
	}
	c, notes, _ = e.backupChange(ctx, r(daily(7)), r(daily(3)), "db1", false)
	if c == nil || c.Risk != wire.RiskDestructive || asked != 3 || len(notes) != 1 || !strings.Contains(notes[0], "deletes 4 backup(s), 4 more") {
		t.Fatalf("lower: %+v %v asked %d", c, notes, asked)
	}
	// No count (no jobs): a lower keep is destructive by the fallback.
	noCount := &Engine{}
	if c, _, _ := noCount.backupChange(ctx, r(daily(7)), r(daily(3)), "db1", false); c == nil || c.Risk != wire.RiskDestructive {
		t.Fatalf("fallback: %+v", c)
	}
	// Unknown count (-1, or no hook) fails closed (review of #334): a
	// field that comes back, or a lower keep, is destructive; a keep that is
	// not lower stays read; a new guest has nothing to delete.
	unknown := &Engine{cfg: Config{BackupKeepDeletes: func(context.Context, string, int) (int, error) { return -1, nil }}}
	// The new keep's count is known (0), the old one's is not: the
	// fallback by keep decides, never a negative difference.
	oldUnknownNone := &Engine{cfg: Config{BackupKeepDeletes: func(_ context.Context, _ string, keep int) (int, error) {
		if keep == 7 {
			return -1, nil
		}
		return 0, nil
	}}}
	// The new keep's count is known, the old one's is not: fail closed too.
	oldUnknown := &Engine{cfg: Config{BackupKeepDeletes: func(_ context.Context, _ string, keep int) (int, error) {
		if keep == 7 {
			return -1, nil
		}
		return 1, nil
	}}}
	for name, tc := range map[string]struct {
		eng       *Engine
		old, nu   map[string]any
		newGuest  bool
		wantDestr bool
	}{
		"re-added, -1":       {unknown, nil, daily(1), false, true},
		"re-added, no hook":  {noCount, nil, daily(1), false, true},
		"lower, -1":          {unknown, daily(7), daily(3), false, true},
		"lower, old -1":      {oldUnknown, daily(7), daily(3), false, true},
		"same keep, old -1":  {oldUnknown, daily(7), map[string]any{"schedule": "daily", "time": "04:00", "keep": float64(7)}, false, false},
		"higher, old -1":     {oldUnknownNone, daily(7), daily(9), false, false},
		"same keep, -1":      {unknown, daily(7), map[string]any{"schedule": "weekly", "weekday": "sun", "time": "03:00", "keep": float64(7)}, false, false},
		"higher, -1":         {unknown, daily(3), daily(7), false, false},
		"new guest, no hook": {noCount, nil, daily(1), true, false},
	} {
		c, _, err := tc.eng.backupChange(ctx, r(tc.old), r(tc.nu), "db1", tc.newGuest)
		if err != nil || c == nil || (c.Risk == wire.RiskDestructive) != tc.wantDestr {
			t.Errorf("%s: %+v %v", name, c, err)
		}
	}
	// Removed: read, deletes nothing.
	c, notes, _ = e.backupChange(ctx, r(daily(7)), r(nil), "db1", false)
	if c == nil || c.Risk != wire.RiskRead || c.To != nil || len(notes) != 1 || !strings.Contains(notes[0], "deletes none") {
		t.Fatalf("removed: %+v %v", c, notes)
	}
	// driverChanges never gives it to Update.
	if got := driverChanges([]wire.Change{{Attr: AttrBackup, Risk: wire.RiskDestructive}}); len(got) != 0 {
		t.Fatalf("driver got %v", got)
	}
}
