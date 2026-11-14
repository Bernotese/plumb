package resource

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// valueTypes are the types whose drift reports carry the guest's value
// for every attribute a hand change can touch, so the copy rule (D-331)
// can hash it. Reviewed for T-166; a new type must be added here with a
// reason, or declare Opaque (D-359).
var valueTypes = map[string]string{
	"file":      "content, mode, owner, group: the observed values",
	"directory": "mode, owner, group: the observed values",
	"package":   "state and version: the observed values",
	"service":   "state and enabled: enums, no guest data",
	"user":      "shell, home, groups, ssh_keys: the observed values (ssh_keys as list elements)",
	"symlink":   "target: the observed link target",
	"unit":      "content: the observed unit text",
	"group":     "gid: the observed number",
	"command":   "NoDrift: never reports drift",
}

// TestEveryTypeClassifiedForCopyRule: host finding F2 of T-158 must not
// repeat with a new type. Each type is either a value type or names its
// opaque attributes; an opaque attribute is no schema attribute (it is an
// observed status, not a desired value).
func TestEveryTypeClassifiedForCopyRule(t *testing.T) {
	for _, name := range Names() {
		typ, _ := Lookup(name)
		_, isValue := valueTypes[name]
		if isValue == (len(typ.Opaque) > 0) {
			t.Errorf("type %s: classify it for the copy rule: either in valueTypes (its drift carries values) or with Opaque attributes (D-359), not both", name)
		}
		var head struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(typ.Schema(), &head); err != nil {
			t.Fatal(err)
		}
		for _, a := range typ.Opaque {
			if _, ok := head.Properties[a]; ok {
				t.Errorf("type %s: opaque attribute %s is a schema attribute", name, a)
			}
		}
	}
	for name := range valueTypes {
		if _, ok := Lookup(name); !ok {
			t.Errorf("valueTypes names the unknown type %s", name)
		}
	}
}

// The opaque attributes are the ones that Diff really reports for a hand
// change behind them.
func TestOpaqueAttrsAreReported(t *testing.T) {
	for _, c := range []struct {
		typ   string
		attrs map[string]any
		obs   State
	}{
		{"firewall", map[string]any{"name": "main", "tcp_ports": "80"},
			State{wire.AttrState: wire.StatePresent, fwAttrTable: fwTableChanged}},
		{"network", map[string]any{"name": "extra", "addresses": "192.0.2.10/24"},
			State{wire.AttrState: wire.StatePresent, networkAttrFile: networkFileChanged}},
		{"apt_repository", map[string]any{"name": "x", "uris": []any{"https://deb.example.org/"}, "suites": []any{"stable"}, "components": []any{"main"}, "key": testKey},
			State{wire.AttrState: wire.StatePresent, "sources": "Types: deb\nURIs: https://evil.example.org/\n"}},
		{"sysctl", map[string]any{"name": "net.ipv4.ip_forward", "value": "1"},
			State{wire.AttrState: wire.StatePresent, "file": "net.ipv4.ip_forward = 1\nnet.core.x = 2\n"}},
	} {
		typ, _ := Lookup(c.typ)
		r, err := typ.New(wire.Resource{ID: wire.ID(c.typ, c.attrs["name"].(string)), Type: c.typ, Attrs: c.attrs}, NewEnv(t.TempDir(), nil))
		if err != nil {
			t.Fatal(err)
		}
		// Every desired attribute as observed, so only the opaque one differs.
		obs := c.obs
		d, _ := r.(interface{ Diff(State) []wire.Change })
		for _, ch := range d.Diff(State{wire.AttrState: wire.StatePresent}) {
			if _, set := obs[ch.Attr]; !set && ch.To != nil {
				obs[ch.Attr] = ch.To
			}
		}
		var attrs []string
		for _, ch := range r.Diff(obs) {
			attrs = append(attrs, ch.Attr)
		}
		for _, a := range typ.Opaque {
			if !slices.Contains(attrs, a) {
				t.Errorf("%s: a hand change gives %v, want the opaque attribute %s", c.typ, attrs, a)
			}
		}
	}
}
