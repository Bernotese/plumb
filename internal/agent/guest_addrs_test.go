//go:build guest

package agent

import (
	"net"
	"net/netip"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// TestGuestAddrs runs the real netlink dumps in the Debian and Ubuntu
// containers (D-213). There eth0 is a veth, so the container fallback
// reports it. An added and a removed address reach the controller as an
// addrs report in one round.
func TestGuestAddrs(t *testing.T) {
	ifc, err := net.InterfaceByName("eth0")
	if err != nil {
		t.Fatal(err)
	}
	ifAddrs, err := ifc.Addrs()
	if err != nil {
		t.Fatal(err)
	}
	var want string
	for _, a := range ifAddrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() {
			want = p.Addr().String()
		}
	}
	if want == "" {
		t.Fatal("eth0 has no IPv4 address")
	}

	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	startAgentWith(t, ctl, ca, "tok", func(o *Options) { o.AddrsGap = 100 * time.Millisecond })
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
	h := ctl.reportsOf(wire.ReportHello)[0].Hello
	if !slices.Contains(h.Addrs, want) {
		t.Fatalf("hello addrs %q miss the eth0 address %s", h.Addrs, want)
	}
	if err := wire.CheckAddrs(h.Addrs, h.AddrsTruncated); err != nil {
		t.Fatalf("hello addrs: %v", err) // no loopback, no link-local
	}

	ip := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v: %v: %s", args, err, out)
		}
	}
	const extra = "192.0.2.99"
	ip("addr", "add", extra+"/32", "dev", "eth0")
	t.Cleanup(func() { _ = exec.Command("ip", "addr", "del", extra+"/32", "dev", "eth0").Run() })
	last := func() []string {
		r := ctl.reportsOf(wire.ReportAddrs)
		if len(r) == 0 {
			return nil
		}
		return r[len(r)-1].Addrs
	}
	// One poll round is at most PollWait (1 s here) plus the gap.
	waitFor(t, "report with the added address", func() bool { return slices.Contains(last(), extra) })
	ip("addr", "del", extra+"/32", "dev", "eth0")
	waitFor(t, "report without it", func() bool { l := last(); return l != nil && !slices.Contains(l, extra) })
	if n := len(ctl.reportsOf(wire.ReportHello)); n != 1 {
		t.Fatalf("%d hellos", n)
	}
}
