package resource

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The slice 3 types have a schema before they have an implementation
// (T-090). T-094 adds command to the type table; then its cases can move
// to TestSchemas. (T-093 moved apt_repository, unit and sysctl.)
func TestSlice3Schemas(t *testing.T) {
	tests := []struct {
		typ   string
		attrs string
		valid bool
	}{
		{"command", `{"name":"init-db","argv":["/usr/local/bin/init","--force"],"creates":"/var/lib/app/.done"}`, true},
		{"command", `{"name":"x","argv":["/bin/sh","-c","echo hi > /x"],"creates":"/x","timeout":60}`, true},
		{"command", `{"name":"x","argv":["/bin/true"]}`, false},           // creates is required
		{"command", `{"name":"x","argv":["true"],"creates":"/x"}`, false}, // absolute program
		{"command", `{"name":"x","argv":[],"creates":"/x"}`, false},
		{"command", `{"name":"x","argv":"/bin/true","creates":"/x"}`, false}, // no shell string
		{"command", `{"name":"x","argv":["/bin/true"],"creates":"x"}`, false},
		{"command", `{"name":"x","argv":["/bin/true"],"creates":"/a/../b"}`, false},
		{"command", `{"name":"x","argv":["/bin/true"],"creates":"/x","state":"absent"}`, false}, // no state
		{"command", `{"name":"x","argv":["/bin/true"],"creates":"/x","timeout":0}`, false},
		{"command", `{"name":"x","argv":["/` + strings.Repeat("a", 4096) + `"],"creates":"/x"}`, false}, // argv[0] max 4096
	}
	compiled := map[string]*jsonschema.Schema{}
	for _, tt := range tests {
		sch := compiled[tt.typ]
		if sch == nil {
			sch = compile(t, Type{Name: tt.typ})
			compiled[tt.typ] = sch
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(tt.attrs))
		if err != nil {
			t.Fatalf("%s: %v", tt.attrs, err)
		}
		err = sch.Validate(inst)
		if tt.valid && err != nil {
			t.Errorf("%s %s: want valid, got %v", tt.typ, tt.attrs, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("%s %s: want invalid, got valid", tt.typ, tt.attrs)
		}
	}
}
