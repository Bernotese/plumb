package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func TestCheckAddrs(t *testing.T) {
	sixteen := func() []string {
		var l []string
		for i := range MaxAddrs {
			l = append(l, fmt.Sprintf("192.0.2.%d", i+1))
		}
		return l
	}
	ok := []struct {
		list      []string
		truncated bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"192.0.2.20"}, false},
		{[]string{"192.0.2.1", "192.0.2.41", "2001:db8::1", "2001:db8::41"}, false},
		{sixteen(), false},
		{sixteen(), true},
	}
	for _, c := range ok {
		if err := CheckAddrs(c.list, c.truncated); err != nil {
			t.Errorf("CheckAddrs(%q, %v) = %v, want nil", c.list, c.truncated, err)
		}
	}
	bad := []struct {
		list      []string
		truncated bool
		want      error
	}{
		{append(sixteen(), "192.0.2.201"), false, ErrAddrsTooMany},
		{[]string{"192.0.2.1"}, true, ErrAddrsTruncate},
		{[]string{""}, false, ErrAddrsForm},
		{[]string{"web1"}, false, ErrAddrsForm},
		{[]string{"192.0.2.20/24"}, false, ErrAddrsForm},
		{[]string{"2001:db8::1%eth0"}, false, ErrAddrsForm},
		{[]string{"::ffff:192.0.2.1"}, false, ErrAddrsForm},
		{[]string{"2001:DB8::1"}, false, ErrAddrsForm},
		{[]string{"2001:db8:0:0::1"}, false, ErrAddrsForm},
		{[]string{"192.0.2.01"}, false, ErrAddrsForm},
		{[]string{" 192.0.2.1"}, false, ErrAddrsForm},
		{[]string{"127.0.0.1"}, false, ErrAddrsKind},
		{[]string{"::1"}, false, ErrAddrsKind},
		{[]string{"169.254.1.1"}, false, ErrAddrsKind},
		{[]string{"fe80::1"}, false, ErrAddrsKind},
		{[]string{"224.0.0.1"}, false, ErrAddrsKind},
		{[]string{"ff02::1"}, false, ErrAddrsKind},
		{[]string{"0.0.0.0"}, false, ErrAddrsKind},
		{[]string{"::"}, false, ErrAddrsKind},
		{[]string{"192.0.2.2", "192.0.2.1"}, false, ErrAddrsOrder},
		{[]string{"2001:db8::1", "192.0.2.1"}, false, ErrAddrsOrder},
		{[]string{"192.0.2.1", "192.0.2.1"}, false, ErrAddrsDup},
	}
	for _, c := range bad {
		err := CheckAddrs(c.list, c.truncated)
		if !errors.Is(err, c.want) {
			t.Errorf("CheckAddrs(%q, %v) = %v, want %v", c.list, c.truncated, err, c.want)
			continue
		}
		for _, a := range c.list {
			if a != "" && strings.Contains(err.Error(), strings.TrimSpace(a)) {
				t.Errorf("error %q holds the address %q", err, a)
			}
		}
	}
}

func TestSortAddrs(t *testing.T) {
	in := []netip.Addr{
		netip.MustParseAddr("2001:db8::41"),
		netip.MustParseAddr("192.0.2.41"),
		netip.MustParseAddr("::ffff:192.0.2.1"),
		netip.MustParseAddr("192.0.2.41"),
		netip.MustParseAddr("2001:db8::1"),
		{}, // zero Addr: dropped
	}
	got, cut := SortAddrs(in)
	want := []string{"192.0.2.1", "192.0.2.41", "2001:db8::1", "2001:db8::41"}
	if !slices.Equal(got, want) || cut {
		t.Fatalf("SortAddrs = %q, %v; want %q, false", got, cut, want)
	}
	if err := CheckAddrs(got, cut); err != nil {
		t.Fatal(err)
	}
	if in[0].String() != "2001:db8::41" {
		t.Error("SortAddrs changed its input")
	}

	var many []netip.Addr
	for i := range 20 {
		many = append(many, netip.AddrFrom4([4]byte{192, 0, 2, byte(20 - i)}))
	}
	got, cut = SortAddrs(many)
	if len(got) != MaxAddrs || !cut || got[0] != "192.0.2.1" || got[MaxAddrs-1] != "192.0.2.16" {
		t.Fatalf("SortAddrs(20) = %q, %v", got, cut)
	}
	if err := CheckAddrs(got, cut); err != nil {
		t.Fatal(err)
	}

	got, _ = SortAddrs(nil)
	if got == nil {
		t.Fatal("SortAddrs(nil) is nil; a new agent must send [] (D-212)")
	}
}

// TestHelloAddrsJSON pins the wire form of D-212 and D-217: a new agent
// sends "addrs" also when empty; an old agent's Hello decodes to nil.
func TestHelloAddrsJSON(t *testing.T) {
	b, err := json.Marshal(Hello{Version: "1", Addrs: []string{}})
	if err != nil || !strings.Contains(string(b), `"addrs":[]`) || strings.Contains(string(b), "addrs_truncated") {
		t.Fatalf("empty list: %s %v", b, err)
	}
	b, _ = json.Marshal(Hello{Addrs: []string{"192.0.2.41"}, AddrsTruncated: true})
	if !strings.Contains(string(b), `"addrs":["192.0.2.41"],"addrs_truncated":true`) {
		t.Fatalf("list: %s", b)
	}
	for _, old := range []string{`{"version":"1","hostname":"h","os":"debian 13"}`, `{"version":"1","addrs":null}`} {
		var h Hello
		if err := json.Unmarshal([]byte(old), &h); err != nil || h.Addrs != nil {
			t.Fatalf("%s: Addrs = %#v, %v; want nil (unknown)", old, h.Addrs, err)
		}
	}
	var h Hello
	if err := json.Unmarshal([]byte(`{"addrs":[]}`), &h); err != nil || h.Addrs == nil {
		t.Fatalf("[]: Addrs = %#v, %v; want empty, not nil", h.Addrs, err)
	}

	b, _ = json.Marshal(Report{Type: ReportAddrs, Addrs: []string{"192.0.2.41"}})
	if string(b) != `{"type":"addrs","addrs":["192.0.2.41"]}` {
		t.Fatalf("addrs report: %s", b)
	}
}
