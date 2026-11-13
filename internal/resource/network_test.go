package resource

import (
	"strings"
	"testing"
)

// D-332, D-348: the value rules that the server and the agent share. An
// error names the attribute and position, never the value (D-325).
func TestCheckNetworkAttrs(t *testing.T) {
	good := map[string]any{"addresses": "192.0.2.10/24,2001:db8::10/64",
		"routes":      "198.51.100.0/24 via 192.0.2.1;2001:db8:1::/48 via fe80::1 metric 100",
		"nameservers": "192.0.2.53,2001:db8::53", "search": "Example.invalid,b.invalid"}
	s, err := CheckNetworkAttrs(good)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Addresses) != 2 || len(s.Routes) != 2 || s.Routes[1].Metric != 100 || len(s.Nameservers) != 2 || s.Search[0] != "example.invalid" {
		t.Fatalf("parsed: %+v", s)
	}
	if s, err := CheckNetworkAttrs(map[string]any{}); err != nil || len(s.Addresses)+len(s.Routes) != 0 {
		t.Fatalf("empty: %+v %v", s, err)
	}
	for _, c := range []struct{ attr, v, want string }{
		{"addresses", "192.0.2.10", "element 1: not an address with a prefix"},
		{"addresses", "192.0.2.10/24,127.0.0.2/8", "element 2: a loopback address"},
		{"addresses", "169.254.1.1/16", "link-local"},
		{"addresses", "fe80::5/64", "link-local"},
		{"addresses", "224.0.0.5/24", "multicast"},
		{"addresses", "0.0.0.0/8", "unspecified"},
		{"addresses", "::ffff:192.0.2.1/120", "IPv4-mapped"},
		{"addresses", "192.0.2.10/0", "prefix length 0"},
		{"addresses", "192.0.2.10/24,192.0.2.10/25", "the same address twice"},
		{"routes", "0.0.0.0/0 via 192.0.2.1", "a default route"},
		{"routes", "::/0 via 2001:db8::1", "a default route"},
		{"routes", "198.51.100.0/24 192.0.2.1", "not '<cidr> via"},
		{"routes", "198.51.100.0/24 via 2001:db8::1", "different IP versions"},
		{"routes", "198.51.100.0/24 via 169.254.0.1", "link-local"},
		{"routes", "198.51.100.0/24 via 192.0.2.1 metric 0", "metric"},
		{"routes", "198.51.100.0/24 via 192.0.2.1;198.51.100.7/24 via 192.0.2.2", "element 2: the same destination twice"},
		{"nameservers", "192.0.2.53,127.0.0.53", "element 2: a loopback address"},
		{"search", "a..b", "not a domain"},
		{"search", "a.invalid,A.invalid", "the same domain twice"},
	} {
		_, err := CheckNetworkAttrs(map[string]any{c.attr: c.v})
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), c.attr) {
			t.Errorf("%s %q: %v, want %q", c.attr, c.v, err, c.want)
			continue
		}
		for _, part := range strings.FieldsFunc(c.v, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
			if strings.ContainsAny(part, ".:") && strings.Contains(err.Error(), part) {
				t.Errorf("%s: the error repeats a value: %v", c.attr, err)
			}
		}
	}
}
