package wire

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestMissing(t *testing.T) {
	base := BaselineCapabilities()
	list := []Resource{
		{ID: "file:/etc/motd", Type: "file", Attrs: map[string]any{"path": "/etc/motd", "content": "x"}},
		{ID: GuestConfigID, Type: "guest", Attrs: map[string]any{"cores": 2}},
	}
	if got := base.Missing(list); len(got) != 0 {
		t.Fatalf("baseline misses %v", got)
	}
	list = append(list,
		Resource{ID: "unit:a.service", Type: "unit", Attrs: map[string]any{"name": "a.service"}},
		Resource{ID: "unit:b.service", Type: "unit", Attrs: map[string]any{"name": "b.service"}},
		Resource{ID: "file:/x", Type: "file", Attrs: map[string]any{"path": "/x", "acl": "y"}},
	)
	if got := strings.Join(base.Missing(list), ","); got != "file.acl,unit" {
		t.Fatalf("Missing = %q", got)
	}
}

func TestCapabilitiesOf(t *testing.T) {
	var old Hello
	if err := json.Unmarshal([]byte(`{"version":"v0.2.0","hostname":"h","os":"debian 13","addrs":[]}`), &old); err != nil {
		t.Fatal(err)
	}
	c := CapabilitiesOf(old)
	if c.CanUpdate() || !slices.Equal(c.Resources["file"], BaselineCapabilities().Resources["file"]) {
		t.Fatalf("old agent = %+v", c)
	}
	newer := Hello{Capabilities: &Capabilities{Commands: []CommandType{CommandUpdate}, Resources: map[string][]string{}}}
	if !CapabilitiesOf(newer).CanUpdate() {
		t.Fatal("update not seen")
	}
	// The baseline lists are sorted, as an agent sends them.
	for typ, attrs := range BaselineCapabilities().Resources {
		if !slices.IsSorted(attrs) {
			t.Errorf("%s: not sorted", typ)
		}
	}
}

func TestCapabilitiesValid(t *testing.T) {
	if b := BaselineCapabilities(); !b.Valid() {
		t.Fatal("baseline is not valid")
	}
	many := map[string][]string{}
	for i := range MaxCapabilityTypes + 1 {
		many[strings.Repeat("a", i+1)] = nil
	}
	bad := []Capabilities{
		{Resources: many},
		{Resources: map[string][]string{"File": nil}},
		{Resources: map[string][]string{"file": {"a.b"}}},
		{Resources: map[string][]string{"file": {strings.Repeat("a", MaxCapabilityName+1)}}},
		{Resources: map[string][]string{"file": make([]string, MaxCapabilityAttrs+1)}},
		{Commands: []CommandType{""}},
		{Commands: make([]CommandType, MaxCapabilityCommands+1)},
	}
	for i, c := range bad {
		if c.Valid() {
			t.Errorf("case %d: valid", i)
		}
		if got := CapabilitiesOf(Hello{Capabilities: &c}); !slices.Equal(got.Commands, BaselineCapabilities().Commands) {
			t.Errorf("case %d: CapabilitiesOf did not fall back to the baseline", i)
		}
	}
}
