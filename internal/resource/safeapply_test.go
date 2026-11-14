package resource

import (
	"context"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// D-333, D-349: every safe-apply type has a restore at agent start, and the
// firewall keeps its unit name on the generic code.
func TestSafeApplyTypesHaveRestore(t *testing.T) {
	for _, typ := range wire.SafeApplyTypes {
		if _, ok := safeRestorers[typ]; !ok {
			t.Errorf("safe-apply type %s has no restore", typ)
		}
		if tp, ok := Lookup(typ); !ok || tp.New == nil {
			t.Errorf("safe-apply type %s is not a resource type", typ)
		}
	}
	if FirewallRollback != RollbackUnit("firewall") {
		t.Errorf("firewall rollback unit %q", FirewallRollback)
	}
	if err := Restore(context.Background(), Env{}, "file"); err == nil {
		t.Error("restore of a type without safe apply")
	}
}
