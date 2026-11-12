package state

import (
	"strings"
	"testing"
)

// T-101: the attributes of a Block are checked without a Target.
func TestCheckBlockAttrs(t *testing.T) {
	tests := []struct {
		name, spec string
		want       []string // "path: part of the message"; empty: no error
	}{
		{"good", `{"resources":[{"file":{"path":"/etc/motd","content":"hi","mode":"0640"}}]}`, nil},
		{"unquoted mode", `{"resources":[{"file":{"path":"/etc/motd","mode":640}}]}`,
			[]string{"/spec/resources/0/file/mode: got number, want string"}},
		{"bad pattern", `{"resources":[{"file":{"path":"/etc/motd","mode":"rw"}}]}`,
			[]string{"/spec/resources/0/file/mode: "}},
		{"unknown attribute", `{"resources":[{"file":{"path":"/x"}},{"file":{"path":"/y","exec":"rm"}}]}`,
			[]string{"/spec/resources/1/file/exec: unknown attribute exec of resource type file"}},
		// A template renders to a string whose content is unknown now.
		{"template in a string", `{"params":{"type":"object","properties":{"m":{"type":"string"}}},"resources":[{"file":{"path":"/x","mode":"{{ .m }}"}}]}`, nil},
		{"template in a key", `{"params":{"type":"object","properties":{"p":{"type":"string"}}},"resources":[{"file":{"path":"{{ .p }}"}}]}`, nil},
		{"template in a list item", `{"params":{"type":"object","properties":{"p":{"type":"string"}}},"resources":[{"service":{"name":"a","restart_on":["{{ .p }}"]}}]}`, nil},
		// ... but always to a string: a boolean attribute cannot hold one.
		{"template in a boolean", `{"params":{"type":"object","properties":{"e":{"type":"string"}}},"resources":[{"service":{"name":"a","enabled":"{{ .e }}"}}]}`,
			[]string{"/spec/resources/0/service/enabled: got string, want boolean"}},
		{"bad item after a template", `{"params":{"type":"object","properties":{"p":{"type":"string"}}},"resources":[{"service":{"name":"a","restart_on":["{{ .p }}","not-an-id"]}}]}`,
			[]string{"/spec/resources/0/service/restart_on/1: "}},
		// Rules over the whole object depend on parameters: render checks them.
		{"required is for render", `{"resources":[{"symlink":{"path":"/etc/x"}}]}`, nil},
	}
	for _, tt := range tests {
		set, err := Validate([]Doc{doc(KindBlock, "b", tt.spec)})
		if err != nil {
			t.Fatalf("%s: Validate: %v", tt.name, err)
		}
		errs := set.CheckBlockAttrs([]string{"b", "nosuch"})
		var got []string
		for _, e := range errs {
			if e.Kind != KindBlock || e.Name != "b" {
				t.Errorf("%s: error of %s %q", tt.name, e.Kind, e.Name)
			}
			got = append(got, e.Path+": "+e.Msg)
		}
		if len(got) != len(tt.want) {
			t.Errorf("%s: errors %q, want %q", tt.name, got, tt.want)
			continue
		}
		for i := range got {
			if !strings.HasPrefix(got[i], tt.want[i]) && !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s: error %q, want %q", tt.name, got[i], tt.want[i])
			}
		}
	}
}

// A stored Block with a bad attribute that nothing uses still loads: the
// check runs only for the Blocks that a proposal adds or changes.
func TestStoredBadBlockLoads(t *testing.T) {
	bad := doc(KindBlock, "old", `{"resources":[{"file":{"path":"/etc/motd","mode":640}}]}`)
	if _, err := validate([]Doc{bad}, DefaultLimits, true); err != nil {
		t.Fatalf("stored load: %v", err)
	}
	if _, err := Validate([]Doc{bad}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
