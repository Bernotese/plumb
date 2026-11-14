package plan

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// n3 of #249: the controller's default gateways are reserved; the IPv4
// table must be readable (fail closed), a missing IPv6 table is no error.
func TestDefaultGateways(t *testing.T) {
	dir := t.TempDir()
	v4 := filepath.Join(dir, "route")
	v6 := filepath.Join(dir, "ipv6_route")
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(v4, "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"+
		"eth0\t00000000\t0102000A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"+ // default via 10.0.2.1
		"eth0\t0002000A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"+ // 10.0.2.0/24, no gateway
		"eth1\t00000000\t00000000\t0001\t0\t0\t0\t00000000\t0\t0\t0\n"+ // default without gateway flag
		"short line\n")
	write(v6, "00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000400 00000001 00000000 00000003 eth0\n"+
		"20010db8000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000001 00000000 00000001 eth0\n")
	old4, old6 := procRoute, procRoute6
	t.Cleanup(func() { procRoute, procRoute6 = old4, old6 })
	procRoute, procRoute6 = v4, v6
	got, err := defaultGateways()
	want := []netip.Addr{netip.MustParseAddr("10.0.2.1"), netip.MustParseAddr("fe80::1")}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("gateways %v %v, want %v", got, err, want)
	}
	res, err := HostAddrs([]string{"192.0.2.9"}, "")(context.Background())
	if err != nil || len(res) != 3 || res[0].Addr != want[0] || res[0].What != "the controller's gateway" {
		t.Fatalf("reserved: %+v %v", res, err)
	}
	procRoute6 = filepath.Join(dir, "none")
	if got, err := defaultGateways(); err != nil || len(got) != 1 {
		t.Fatalf("no IPv6 table: %v %v", got, err)
	}
	procRoute = filepath.Join(dir, "none")
	if _, err := HostAddrs(nil, "")(context.Background()); err == nil {
		t.Fatal("an unreadable route table did not fail closed")
	}
}
