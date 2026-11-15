package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// fakeDeletes models the jobs' count: n scheduled Plumb backups, and the
// next job adds one first (backup.NextJobDeletes).
func fakeDeletes(n int) func(context.Context, string, int) (int, error) {
	return func(_ context.Context, _ string, keep int) (int, error) {
		if keep < 1 {
			return 0, nil
		}
		return min(max(n+1-keep, 0), n), nil
	}
}

// T-226 and T-233 (D-417, M1 of #334): the risk comes from the deletes
// that the Plan causes: the new field's count above the old field's.
func TestQABackupChangeRiskByCount(t *testing.T) {
	ctx := context.Background()
	daily := func(keep int) map[string]any {
		return map[string]any{"schedule": "daily", "time": "03:00", "keep": float64(keep)}
	}
	at := func(keep int, time string) map[string]any {
		return map[string]any{"schedule": "daily", "time": time, "keep": float64(keep)}
	}
	r := func(b map[string]any) *state.Rendered { return &state.Rendered{Target: "db1", Backup: b} }
	for _, c := range []struct {
		name     string
		old, nu  map[string]any
		n        int // scheduled backups now
		wantRisk wire.Risk
		note     string // part of the note
	}{
		{"time only, at the limit (S1 of T-225)", daily(7), at(7, "04:00"), 7, wire.RiskRead, ""},
		{"time only, above the limit", daily(3), at(3, "04:00"), 9, wire.RiskRead, ""},
		{"raise, still above: fewer go", daily(7), daily(9), 12, wire.RiskRead, ""},
		{"raise, none goes", daily(7), daily(9), 7, wire.RiskRead, ""},
		{"lower by one at the limit", daily(7), daily(6), 7, wire.RiskDestructive, "deletes 2 backup(s), 1 more"},
		{"lower, below the count: none goes", daily(9), daily(7), 5, wire.RiskRead, ""},
		{"lower by four", daily(7), daily(3), 7, wire.RiskDestructive, "deletes 5 backup(s), 4 more"},
		{"re-added with keep 1 (M1 of #334)", nil, daily(1), 7, wire.RiskDestructive, "deletes 7 backup(s), 7 more"},
		{"re-added, nothing goes", nil, daily(9), 7, wire.RiskRead, ""},
	} {
		e := &Engine{cfg: Config{BackupKeepDeletes: fakeDeletes(c.n)}}
		ch, notes, err := e.backupChange(ctx, r(c.old), r(c.nu), "db1", false)
		if err != nil || ch == nil || ch.Risk != c.wantRisk {
			t.Errorf("%s: %+v %v", c.name, ch, err)
			continue
		}
		if c.note != "" && !strings.Contains(strings.Join(notes, " "), c.note) {
			t.Errorf("%s: notes %v", c.name, notes)
		}
	}
}
