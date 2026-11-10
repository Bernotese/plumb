package state

import (
	"bytes"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func compile(t *testing.T, kind string) *jsonschema.Schema {
	t.Helper()
	raw, ok := Schema(kind)
	if !ok {
		t.Fatalf("no schema for kind %q", kind)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("%s: schema is not JSON: %v", kind, err)
	}
	const url = "mem:///schema.json"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		t.Fatalf("%s: schema does not compile: %v", kind, err)
	}
	return sch
}

func TestSchemaKinds(t *testing.T) {
	for _, kind := range Kinds {
		compile(t, kind)
	}
	if _, ok := Schema("Secret"); ok {
		t.Error("unknown kind must have no schema")
	}
}

func TestDocumentSchemas(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const guest = `"guest":{"cores":2,"memory":2048,"disk":20,"bridge":"vmbr0","ip":"dhcp"}`
	tests := []struct {
		name  string
		kind  string
		doc   string
		valid bool
	}{
		{"image", KindImage, `{"kind":"Image","name":"ubuntu-26.04","spec":{"type":"vm","url":"https://cloud-images.ubuntu.com/x.img","sha256":"` + sha + `"}}`, true},
		{"image without checksum", KindImage, `{"kind":"Image","name":"ubuntu","spec":{"type":"vm","url":"https://example.org/x.img"}}`, false},
		{"image over http", KindImage, `{"kind":"Image","name":"ubuntu","spec":{"type":"vm","url":"http://example.org/x.img","sha256":"` + sha + `"}}`, false},
		{"image with short checksum", KindImage, `{"kind":"Image","name":"ubuntu","spec":{"type":"vm","url":"https://example.org/x.img","sha256":"abc"}}`, false},

		// The Block of docs/ARCHITECTURE.md §3.
		{"block", KindBlock, `{"kind":"Block","name":"deploy-user","spec":{
			"params":{"required":["ssh_key"],"properties":{"name":{"type":"string","default":"deploy"},"ssh_key":{"type":"string"}}},
			"resources":[{"user":{"name":"{{ .name }}","shell":"/bin/bash","ssh_keys":["{{ .ssh_key }}"]}}]}}`, true},
		{"block without params", KindBlock, `{"kind":"Block","name":"motd","spec":{"resources":[{"file":{"path":"/etc/motd","content":"hi"}}]}}`, true},
		{"block without resources", KindBlock, `{"kind":"Block","name":"empty","spec":{"resources":[]}}`, false},
		{"block entry with two types", KindBlock, `{"kind":"Block","name":"two","spec":{"resources":[{"file":{"path":"/a"},"user":{"name":"a"}}]}}`, false},
		{"block with template features that are not in the slice", KindBlock, `{"kind":"Block","name":"later","spec":{"uses":["motd"],"resources":[{"file":{"path":"/a"}}]}}`, false},

		// The Target of docs/ARCHITECTURE.md §3.
		{"target", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"ubuntu-26.04",` + guest + `,
			"blocks":[{"use":"deploy-user","params":{"ssh_key":"ssh-ed25519 AAAA"}},{"use":"nginx"}],"overrides":{}}}`, true},
		{"target with static ip and override", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"debian-13",
			"guest":{"cores":1,"memory":512,"disk":8,"bridge":"vmbr0","ip":"10.0.0.7/24","gateway":"10.0.0.1","user":"plumb","ssh_keys":["ssh-ed25519 AAAA"]},
			"overrides":{"file:/etc/motd":{"content":"by hand"}}}}`, true},
		{"target without guest", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"ubuntu-26.04"}}`, false},
		{"target with unknown kind of guest", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"docker","image":"x",` + guest + `}}`, false},
		{"target with zero cores", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"x","guest":{"cores":0,"memory":512,"disk":8,"bridge":"vmbr0"}}}`, false},
		{"target with bad override id", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"x",` + guest + `,"overrides":{"motd":{}}}}`, false},
		{"target with bad name", KindTarget, `{"kind":"Target","name":"Web_1","spec":{"type":"vm","image":"x",` + guest + `}}`, false},
		{"wrong kind", KindTarget, `{"kind":"Block","name":"web1","spec":{"type":"vm","image":"x",` + guest + `}}`, false},
		{"unknown top-level field", KindTarget, `{"kind":"Target","name":"web1","status":"ok","spec":{"type":"vm","image":"x",` + guest + `}}`, false},
	}
	schemas := map[string]*jsonschema.Schema{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sch := schemas[tt.kind]
			if sch == nil {
				sch = compile(t, tt.kind)
				schemas[tt.kind] = sch
			}
			inst, err := jsonschema.UnmarshalJSON(strings.NewReader(tt.doc))
			if err != nil {
				t.Fatal(err)
			}
			err = sch.Validate(inst)
			if tt.valid && err != nil {
				t.Errorf("want valid, got %v", err)
			}
			if !tt.valid && err == nil {
				t.Error("want invalid, got valid")
			}
		})
	}
}
