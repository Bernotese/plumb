package resource

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Schema cases of firewall (T-111, D-295, D-300). T-116 adds the type to
// the type table with its code.
func TestSlice4Schemas(t *testing.T) {
	tests := []struct {
		attrs string
		valid bool
	}{
		{`{"name":"main"}`, true},
		{`{"name":"main","policy":"drop","tcp_ports":"80,443,8000-8100","udp_ports":"51820"}`, true},
		{`{"name":"main","tcp_ports":"443","sources":"10.0.0.0/8,fd00::/8","ssh_port":2222,"ssh_sources":"192.0.2.0/24"}`, true},
		{`{"name":"main","ssh_port":"{{ .port }}"}`, false}, // a template renders before the check; the raw text is no port
		{`{"name":"main","ssh_port":"2222"}`, true},         // a rendered parameter is text
		{`{"name":"main","policy":"drop","allow_no_ssh":true}`, true},
		{`{"name":"main","allow_no_ssh":true,"ssh_port":22}`, false},
		{`{"name":"main","allow_no_ssh":true,"ssh_sources":"10.0.0.0/8"}`, false},
		{`{"name":"main","allow_no_ssh":false,"ssh_port":22}`, true},
		{`{"name":"main","state":"absent"}`, true},
		{`{"name":"main","state":"absent","policy":"drop"}`, false},
		{`{"name":"other"}`, false}, // one per Target
		{`{"name":"main","policy":"reject"}`, false},
		{`{"name":"main","tcp_ports":"80;443"}`, false},
		{`{"name":"main","tcp_ports":"80,"}`, false},
		{`{"name":"main","tcp_ports":"http"}`, false},
		{`{"name":"main","sources":"10.0.0.0"}`, false}, // a CIDR needs its prefix
		{`{"name":"main","sources":"10.0.0.0/8 ; drop"}`, false},
		{`{"name":"main","ssh_port":0}`, false},
		{`{"name":"main","ssh_port":70000}`, false},
		{`{"name":"main","ssh_port":"022"}`, false},
		{`{"name":"main","output":"drop"}`, false}, // no output chain
	}
	sch := compile(t, Type{Name: "firewall"})
	for _, tt := range tests {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(tt.attrs))
		if err != nil {
			t.Fatalf("%s: %v", tt.attrs, err)
		}
		err = sch.Validate(inst)
		if tt.valid && err != nil {
			t.Errorf("%s: want valid, got %v", tt.attrs, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("%s: want invalid, got valid", tt.attrs)
		}
	}
}
