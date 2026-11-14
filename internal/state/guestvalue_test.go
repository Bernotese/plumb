package state

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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
		// K3 of #248: the observed SSH rule gives the ssh_sources elements.
		{"ssh sources", "firewall", "ssh", "tcp/22 from 203.0.113.7/32,10.0.0.5/24", []string{"10.0.0.0/24", "203.0.113.7/32"}},
		{"ssh everywhere", "firewall", "ssh", "tcp/2222 from everywhere", nil},
		{"ssh closed", "firewall", "ssh", "closed (allow_no_ssh)", nil},
	} {
		got, over := GuestValueElems(c.typ, c.attr, c.v)
		if over {
			t.Errorf("%s: over the limit", c.name)
		}
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
	got, over := GuestValueElems("user", "ssh_keys", list)
	if len(got) != MaxGuestElems || !over {
		t.Fatalf("%d elements, over %v; want %d and over (K1 of #248)", len(got), over, MaxGuestElems)
	}
	if _, over := GuestValueElems("user", "ssh_keys", list[:MaxGuestElems]); over {
		t.Fatal("exactly the limit is not over")
	}
}

// D-359, review O2 of #264: new lines of a text count, bounded on the new
// lines only.
func TestGuestValueLines(t *testing.T) {
	got, over := GuestValueLines("file", "content", "Keep  me as is\n  PermitRootLogin yes \n}\nfi\npermitrootlogin\tYES\r\n", "keep me as is\n")
	if over || len(got) != 1 || got[0] != "permitrootlogin yes" {
		t.Fatalf("lines %q over %v", got, over)
	}
	if got, _ := GuestValueLines("file", "path", "/etc/a-long-path\n", ""); got != nil {
		t.Fatalf("not a lines attribute: %q", got)
	}
	big := strings.Repeat("an unchanged line here\n", 10)
	for i := 0; i < MaxGuestElems+10; i++ {
		big += fmt.Sprintf("old line number %06d\n", i)
	}
	if _, over := GuestValueLines("file", "content", big+"one new line here\n", big); over {
		t.Fatal("a big unchanged file overflowed")
	}
	if _, over := GuestValueLines("unit", "content", big, ""); !over {
		t.Fatal("too many new lines did not overflow")
	}
}
