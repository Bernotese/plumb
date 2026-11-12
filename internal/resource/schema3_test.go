package resource

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The slice 3 types have a schema before they have an implementation
// (T-090). T-093 and T-094 add them to the type table; then their cases
// can move to TestSchemas.
func TestSlice3Schemas(t *testing.T) {
	const key = `-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmQINBFit2ioBEADh\n-----END PGP PUBLIC KEY BLOCK-----\n`
	const apt = `"name":"docker","uris":["https://download.docker.com/linux/debian"],"suites":["trixie"],"components":["stable"]`
	tests := []struct {
		typ   string
		attrs string
		valid bool
	}{
		{"apt_repository", `{` + apt + `,"key":"` + key + `"}`, true},
		{"apt_repository", `{` + apt + `}`, false},                                    // present needs the key
		{"apt_repository", `{"name":"docker","state":"absent"}`, true},                // absent needs nothing else
		{"apt_repository", `{` + apt + `,"key":"https://x/key.gpg"}`, false},          // never a URL
		{"apt_repository", `{` + apt + `,"key":"` + key + `","trusted":true}`, false}, // no Trusted
		{"apt_repository", `{` + apt + `,"key":"` + key + `","options":"x"}`, false},  // no free options
		{"apt_repository", `{"name":"../x","state":"absent"}`, false},                 // name is part of a path
		{"apt_repository", `{"name":"d","uris":["file:///srv"],"suites":["a"],"key":"` + key + `"}`, false},
		{"apt_repository", `{"name":"d","uris":["https://x"],"suites":["a b"],"key":"` + key + `"}`, false},
		{"unit", `{"name":"backup.timer","content":"[Timer]\nOnCalendar=daily\n"}`, true},
		{"unit", `{"name":"app.service"}`, false}, // present needs content
		{"unit", `{"name":"app.service","state":"absent"}`, true},
		{"unit", `{"name":"app","content":"x"}`, false},                 // no unit suffix
		{"unit", `{"name":"plumb-agent.service","content":"x"}`, false}, // the agent unit is Plumb's
		{"unit", `{"name":"-x.service","content":"x"}`, false},
		{"unit", `{"name":"a/b.service","content":"x"}`, false},
		{"sysctl", `{"name":"net.ipv4.ip_forward","value":"1"}`, true},
		{"sysctl", `{"name":"net.ipv4.ip_forward","value":1}`, true},
		{"sysctl", `{"name":"vm.swappiness"}`, false},
		{"sysctl", `{"name":"vm.swappiness","state":"absent"}`, true},
		{"sysctl", `{"name":"kernel","value":"1"}`, false},          // needs a dot
		{"sysctl", `{"name":"net/ipv4/x","value":"1"}`, false},      // no slash
		{"sysctl", `{"name":"vm.swappiness","value":"1\n"}`, false}, // one line
		{"sysctl", `{"name":"vm.swappiness","value":" "}`, false},
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
