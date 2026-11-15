package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-226 (D-417, M1 of #334): the risk comes from the count, not from the
// direction of keep. A Plan that raises keep while more scheduled backups
// exist than the new keep (the next job deletes one) is destructive; a
// lower keep that deletes none is not.
func TestQABackupChangeRiskByCount(t *testing.T) {
	ctx := context.Background()
	daily := func(keep int) map[string]any {
		return map[string]any{"schedule": "daily", "time": "03:00", "keep": float64(keep)}
	}
	r := func(b map[string]any) *state.Rendered { return &state.Rendered{Target: "db1", Backup: b} }
	for _, c := range []struct {
		name     string
		old, nu  int
		deletes  int
		wantRisk wire.Risk
	}{
		{"raise, one goes", 7, 9, 1, wire.RiskDestructive},
		{"raise, none goes", 7, 9, 0, wire.RiskRead},
		{"lower, none goes", 9, 7, 0, wire.RiskRead},
		{"lower, one goes", 9, 7, 1, wire.RiskDestructive},
	} {
		e := &Engine{cfg: Config{BackupKeepDeletes: func(context.Context, string, int) (int, error) { return c.deletes, nil }}}
		ch, notes, err := e.backupChange(ctx, r(daily(c.old)), r(daily(c.nu)), "db1", false)
		if err != nil || ch == nil || ch.Risk != c.wantRisk {
			t.Errorf("%s: %+v %v", c.name, ch, err)
			continue
		}
		if c.deletes > 0 && !strings.Contains(strings.Join(notes, " "), "deletes 1 backup") {
			t.Errorf("%s: notes %v", c.name, notes)
		}
	}
}
