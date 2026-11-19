package resource

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestWebResourceKinds keeps the web copy of the type table in step with
// types: the target kinds of each type (the Block picker hides Blocks that
// do not fit the target kind, T-118, D-260) and its key attribute (the
// Block form preview and the mock form ids from it).
func TestWebResourceKinds(t *testing.T) {
	raw, err := os.ReadFile("../../web/src/lib/resourceKinds.json")
	if err != nil {
		t.Fatal(err)
	}
	type webType struct {
		Kinds []string `json:"kinds"`
		Key   string   `json:"key"`
	}
	var web map[string]webType
	if err := json.Unmarshal(raw, &web); err != nil {
		t.Fatal(err)
	}
	want := map[string]webType{}
	for name, typ := range types {
		w := webType{Key: typ.Key}
		for _, k := range typ.Kinds {
			w.Kinds = append(w.Kinds, string(k))
		}
		want[name] = w
	}
	if !reflect.DeepEqual(web, want) {
		t.Fatalf("web/src/lib/resourceKinds.json = %v, want %v (from types)", web, want)
	}
}
