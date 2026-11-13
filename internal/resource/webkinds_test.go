package resource

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestWebResourceKinds keeps the web copy of the kind table in step with
// types: the Block picker hides Blocks that do not fit the target kind
// (T-118, D-260).
func TestWebResourceKinds(t *testing.T) {
	raw, err := os.ReadFile("../../web/src/lib/resourceKinds.json")
	if err != nil {
		t.Fatal(err)
	}
	var web map[string][]string
	if err := json.Unmarshal(raw, &web); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{}
	for name, typ := range types {
		for _, k := range typ.Kinds {
			want[name] = append(want[name], string(k))
		}
	}
	if !reflect.DeepEqual(web, want) {
		t.Fatalf("web/src/lib/resourceKinds.json = %v, want %v (from types)", web, want)
	}
}
