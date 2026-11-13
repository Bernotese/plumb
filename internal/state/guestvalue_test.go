package state

import (
	"encoding/json"
	"slices"
	"testing"
)

// T-148 (D-331, D-350): the parts of a value that the copy rule compares.
func TestGuestValueElems(t *testing.T) {
	for _, c := range []struct {
		name, typ, attr string
		v               any
		want            []string
	}{
		{"string", "file", "content", "a b\n", []string{"a b\n"}},
		{"list", "user", "ssh_keys", []any{"k2", "k1", "k1"}, []string{"k1", "k2"}},
		{"ports normalized", "firewall", "tcp_ports", "443,80,81", []string{"443", "80-81"}},
		{"networks masked", "firewall", "sources", "10.0.0.5/24,192.0.2.1/32", []string{"10.0.0.0/24", "192.0.2.1/32"}},
		{"ssh sources", "firewall", "ssh_sources", "198.51.100.7/32", []string{"198.51.100.7/32"}},
		{"bad list kept", "firewall", "tcp_ports", "99999", []string{"99999"}},
		{"enum skipped", "firewall", "policy", "drop", nil},
		{"const skipped", "firewall", "name", "main", nil},
		{"bool skipped", "firewall", "ping", true, nil},
		{"number skipped", "file", "mode", json.Number("420"), nil},
		{"empty", "file", "content", "", nil},
		{"object", "guest", "x", map[string]any{"b": 1, "a": "x"}, []string{`{"a":"x","b":1}`}},
		{"unknown type", "nope", "x", "v", []string{"v"}},
	} {
		got := GuestValueElems(c.typ, c.attr, c.v)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestGuestValueElemsBounded(t *testing.T) {
	list := make([]any, MaxGuestElems+10)
	for i := range list {
		list[i] = string(rune('a'+i%26)) + string(rune(0x100+i))
	}
	if n := len(GuestValueElems("user", "ssh_keys", list)); n != MaxGuestElems {
		t.Fatalf("%d elements, want %d", n, MaxGuestElems)
	}
}
