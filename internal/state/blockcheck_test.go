package state

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestBlockCheckCases runs the shared cases of testdata/blockcheck through
// the server's Block schema. The web test runs the same file through the
// browser check (web/src/lib/blockCheck.test.ts), so both report the same
// pointers (D-264).
func TestBlockCheckCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/blockcheck/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name     string          `json:"name"`
			Doc      json.RawMessage `json:"doc"`
			Pointers []string        `json:"pointers"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) < 10 {
		t.Fatalf("only %d cases", len(file.Cases))
	}
	sch := mustSchemas().docs[KindBlock]
	for _, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) {
			inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(c.Doc))
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			if err := sch.Validate(inst); err != nil {
				for _, e := range schemaErrors(KindBlock, "x", "", err) {
					got = append(got, e.Path)
				}
			}
			slices.Sort(got)
			got = slices.Compact(got)
			want := append([]string{}, c.Pointers...)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("pointers = %q, want %q", got, want)
			}
		})
	}
}
