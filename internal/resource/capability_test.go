package resource

import (
	"reflect"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// The slice 2 types must keep exactly the baseline attributes: an agent
// that sends them may not claim less than the old agents had (D-259).
func TestCapabilitiesCoverBaseline(t *testing.T) {
	got := Capabilities()
	for typ, attrs := range wire.BaselineCapabilities().Resources {
		if !reflect.DeepEqual(got[typ], attrs) {
			t.Errorf("%s: %v, baseline %v", typ, got[typ], attrs)
		}
	}
	for typ := range got {
		if tt, _ := Lookup(typ); tt.New == nil {
			t.Errorf("%s has no implementation but is a capability", typ)
		}
	}
}
