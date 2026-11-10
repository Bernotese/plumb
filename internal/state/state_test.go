package state

import (
	"bytes"
	"encoding/json"
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
			"guest":{"cores":1,"memory":512,"disk":8,"bridge":"vmbr0","ip":"10.0.0.7/24","gateway":"10.0.0.1","user":"plumb","ssh_keys":["ssh-ed25519 AAAA me@host"]},
			"overrides":{"file:/etc/motd":{"content":"by hand"}}}}`, true},
		{"target without guest", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"ubuntu-26.04"}}`, false},
		{"target with unknown kind of guest", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"docker","image":"x",` + guest + `}}`, false},
		{"target with zero cores", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"x","guest":{"cores":0,"memory":512,"disk":8,"bridge":"vmbr0"}}}`, false},
		{"target with two uses of one Block", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"x",` + guest + `,
			"blocks":[{"use":"deploy-user","id":"deploy","params":{"ssh_key":"k"}},{"use":"deploy-user","id":"personal","params":{"ssh_key":"k2"}}]}}`, true},
		{"target with bad block id", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"x",` + guest + `,"blocks":[{"use":"motd","id":"A B"}]}}`, false},
		{"target with ssh key options", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"x",
			"guest":{"cores":1,"memory":512,"disk":8,"bridge":"vmbr0","ssh_keys":["command=\"id\" ssh-ed25519 AAAA"]}}}`, false},
		{"target with a second line in an ssh key", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"x",
			"guest":{"cores":1,"memory":512,"disk":8,"bridge":"vmbr0","ssh_keys":["ssh-ed25519 AAAA me\nssh-ed25519 BBBB evil"]}}}`, false},
		{"target with impossible ip", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"x",
			"guest":{"cores":1,"memory":512,"disk":8,"bridge":"vmbr0","ip":"999.1.1.1/99"}}}`, false},
		{"target with gateway and dhcp", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"x",
			"guest":{"cores":1,"memory":512,"disk":8,"bridge":"vmbr0","ip":"dhcp","gateway":"10.0.0.1"}}}`, false},
		{"target with gateway and no ip", KindTarget, `{"kind":"Target","name":"web1","spec":{"type":"vm","image":"x",
			"guest":{"cores":1,"memory":512,"disk":8,"bridge":"vmbr0","gateway":"10.0.0.1"}}}`, false},
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

// The canonical form is permanent: document hashes are stored in revisions.
// If this test fails, the form changed and old hashes no longer match.
func TestCanonical(t *testing.T) {
	doc := Doc{Kind: KindTarget, Name: "web1", Spec: []byte(`{
		"type": "vm",  "image": "ubuntu-26.04",
		"guest": {"memory": 2048, "cores": 2, "disk": 20.0, "big": 12345678901234567890, "bridge": "vmbr0"},
		"blocks": [{"use": "motd", "params": {"text": "a <b> & c\u00e9"}}]
	}`)}
	const want = `{"kind":"Target","name":"web1","spec":{"blocks":[{"params":{"text":"a <b> & cé"},"use":"motd"}],` +
		`"guest":{"big":12345678901234567890,"bridge":"vmbr0","cores":2,"disk":20.0,"memory":2048},"image":"ubuntu-26.04","type":"vm"}}`
	const wantHash = "c40574d37a7a121bce33486bbccbbbf10254d5fd4ab1a194a571afb6989e9082"
	got, err := Canonical(doc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("canonical form changed:\n got %s\nwant %s", got, want)
	}
	if h := Hash(got); h != wantHash {
		t.Errorf("hash = %s, want %s", h, wantHash)
	}

	// Key order and whitespace of the input do not matter.
	same := Doc{Kind: KindTarget, Name: "web1", Spec: []byte(`{"blocks":[{"params":{"text":"a <b> & cé"},"use":"motd"}],"image":"ubuntu-26.04","guest":{"bridge":"vmbr0","big":12345678901234567890,"disk":20.0,"cores":2,"memory":2048},"type":"vm"}`)}
	again, err := Canonical(same)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != want {
		t.Errorf("same document, other input order:\n got %s\nwant %s", again, want)
	}
	// The canonical form of a canonical body is itself.
	var parsed Doc
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatal(err)
	}
	if twice, err := Canonical(parsed); err != nil || string(twice) != want {
		t.Errorf("not stable: %s, %v", twice, err)
	}

	for _, bad := range []string{``, `{"a":`, `{} {}`, `{"a":1} x`} {
		if _, err := Canonical(Doc{Kind: KindBlock, Name: "x", Spec: []byte(bad)}); err == nil {
			t.Errorf("spec %q: want an error", bad)
		}
	}
}
