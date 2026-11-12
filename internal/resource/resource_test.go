package resource

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func compile(t *testing.T, typ Type) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(typ.Schema()))
	if err != nil {
		t.Fatalf("%s: schema is not JSON: %v", typ.Name, err)
	}
	const url = "mem:///schema.json"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		t.Fatalf("%s: schema does not compile: %v", typ.Name, err)
	}
	return sch
}

func TestTypeTable(t *testing.T) {
	want := []string{"directory", "file", "package", "service", "symlink", "user"}
	if got := Names(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for _, name := range Names() {
		typ, ok := Lookup(name)
		if !ok || typ.Name != name {
			t.Fatalf("Lookup(%q) = %+v, %v", name, typ, ok)
		}
		if len(typ.Kinds) == 0 || !typ.Supports(wire.KindVM) {
			t.Errorf("%s: slice types must support VMs, kinds = %v", name, typ.Kinds)
		}
		// The key attribute must be a required string of the schema.
		var s struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
			Additional *bool                      `json:"additionalProperties"`
		}
		if err := json.Unmarshal(typ.Schema(), &s); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.Properties[typ.Key]; !ok || !contains(s.Required, typ.Key) {
			t.Errorf("%s: key %q must be a required property", name, typ.Key)
		}
		if s.Additional == nil || *s.Additional {
			t.Errorf("%s: schema must set additionalProperties to false", name)
		}
		compile(t, typ)
	}
	if _, ok := Lookup("command"); ok {
		t.Error("the command resource is not part of the first slice")
	}
}

func TestSchemas(t *testing.T) {
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFake me@host"
	tests := []struct {
		typ   string
		attrs string
		valid bool
	}{
		{"file", `{"path":"/etc/motd","content":"hi\n","mode":"0644","owner":"root","group":"root"}`, true},
		{"file", `{"path":"/etc/motd"}`, true},
		{"file", `{"content":"x"}`, false},                // path is required
		{"file", `{"path":"etc/motd"}`, false},            // not absolute
		{"file", `{"path":"/etc/../root/.ssh/x"}`, false}, // no ".." segment
		{"file", `{"path":"/etc/motd","mode":"rw-r--r--"}`, false},
		{"file", `{"path":"/etc/motd","mode":"0999"}`, false},
		{"file", `{"path":"/etc/motd","exec":"rm -rf /"}`, false}, // unknown attribute
		{"directory", `{"path":"/srv/www","mode":"0755","state":"present"}`, true},
		{"directory", `{"path":"/srv/www","state":"gone"}`, false},
		{"package", `{"name":"nginx"}`, true},
		{"package", `{"name":"nginx","strictness":"exact","version":"1.26.0-1"}`, true},
		{"package", `{"name":"nginx","strictness":"exact"}`, false}, // exact needs a version
		{"package", `{"name":"nginx; rm -rf /"}`, false},
		{"package", `{"name":"-o=Dpkg::x"}`, false}, // must not look like an option
		{"package", `{"name":"nginx-"}`, false},     // apt reads "foo-" as "remove foo"
		{"package", `{"name":"g++"}`, true},
		{"service", `{"name":"nginx","state":"running","enabled":true,"restart_on":["file:/etc/nginx/sites-enabled/default"]}`, true},
		{"service", `{"name":"nginx","restart_on":["not-an-id"]}`, false},
		{"service", `{"name":"nginx","enabled":"yes"}`, false},
		{"service", `{"name":"a b"}`, false},
		{"service", `{"name":"-Hroot@host"}`, false}, // must not look like an option
		{"service", `{"name":"getty@tty1.service"}`, true},
		{"package", `{"name":"nginx","strictness":"exact","version":"1:1.26.0-1~deb13u1+b2"}`, true},
		{"package", `{"name":"nginx","strictness":"exact","version":"--force-yes"}`, false},
		{"package", `{"name":"nginx","version":"1.0 ; reboot"}`, false},
		{"user", `{"name":"deploy","ssh_keys":["` + key + `\nssh-ed25519 AAAA evil"]}`, false}, // one key per entry
		{"user", `{"name":"deploy","shell":"/bin/bash","groups":["sudo"],"ssh_keys":["` + key + `"]}`, true},
		{"user", `{"name":"Deploy"}`, false},
		{"user", `{"name":"-x"}`, false},
		{"user", `{"name":"deploy","ssh_keys":["command=\"x\" ` + key + `"]}`, false}, // no key options
		{"user", `{"name":"deploy","password":"x"}`, false},                           // no passwords in the slice
		{"symlink", `{"path":"/etc/nginx/sites-enabled/app","target":"../sites-available/app"}`, true},
		{"symlink", `{"path":"/usr/local/bin/tool","target":"/opt/tool/bin/tool","state":"present"}`, true},
		{"symlink", `{"path":"/etc/nginx/sites-enabled/app","state":"absent"}`, true},
		{"symlink", `{"path":"/etc/nginx/sites-enabled/app"}`, false},       // present needs a target
		{"symlink", `{"path":"/etc/x","target":""}`, false},                 // empty target
		{"symlink", `{"path":"/etc/x","target":"a\u0000b"}`, false},         // NUL in target
		{"symlink", `{"path":"/etc/../x","target":"y"}`, false},             // no ".." in the path
		{"symlink", `{"path":"/etc/x","target":"y","owner":"root"}`, false}, // unknown attribute
	}
	schemas := map[string]*jsonschema.Schema{}
	for _, tt := range tests {
		sch := schemas[tt.typ]
		if sch == nil {
			typ, ok := Lookup(tt.typ)
			if !ok {
				t.Fatalf("unknown type %q", tt.typ)
			}
			sch = compile(t, typ)
			schemas[tt.typ] = sch
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

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// The list of internal/wire that link uses to refuse items of other
// types (T-070 review B1) must equal this table.
func TestWireAgentTypes(t *testing.T) {
	if got, want := wire.AgentTypes(), Names(); !slices.Equal(got, want) {
		t.Fatalf("wire.AgentTypes() = %v, resource.Names() = %v", got, want)
	}
}

// Agents before slice 3 send no capabilities; the controller assumes
// wire.BaselineCapabilities. Every type and attribute in it must still be
// in this table, or an old agent would look able to apply it (D-259).
func TestBaselineCapabilities(t *testing.T) {
	for typ, attrs := range wire.BaselineCapabilities().Resources {
		rt, ok := Lookup(typ)
		if !ok {
			t.Errorf("baseline type %s is not in the table", typ)
			continue
		}
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(rt.Schema(), &s); err != nil {
			t.Fatal(err)
		}
		for _, a := range attrs {
			if _, ok := s.Properties[a]; !ok {
				t.Errorf("baseline attribute %s.%s is not in the schema", typ, a)
			}
		}
	}
}
