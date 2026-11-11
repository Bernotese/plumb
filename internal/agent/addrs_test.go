package agent

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// A dump is built here message by message, in the byte layout of the
// kernel, so the parser runs on the same bytes as in a guest.

func rtattr(typ uint16, val []byte) []byte {
	n := syscall.SizeofRtAttr + len(val)
	b := make([]byte, (n+3)&^3)
	binary.NativeEndian.PutUint16(b[0:2], uint16(n))
	binary.NativeEndian.PutUint16(b[2:4], typ)
	copy(b[syscall.SizeofRtAttr:], val)
	return b
}

func nlmsg(typ uint16, body []byte) []byte {
	n := syscall.SizeofNlMsghdr + len(body)
	b := make([]byte, (n+3)&^3)
	binary.NativeEndian.PutUint32(b[0:4], uint32(n))
	binary.NativeEndian.PutUint16(b[4:6], typ)
	binary.NativeEndian.PutUint16(b[6:8], syscall.NLM_F_MULTI)
	copy(b[syscall.SizeofNlMsghdr:], body)
	return b
}

// linkMsg is one RTM_NEWLINK message. kind "" means no IFLA_LINKINFO.
func linkMsg(index int32, name, kind string, flags uint32) []byte {
	hdr := make([]byte, syscall.SizeofIfInfomsg)
	hdr[0] = syscall.AF_UNSPEC
	binary.NativeEndian.PutUint32(hdr[4:8], uint32(index))
	binary.NativeEndian.PutUint32(hdr[8:12], flags)
	body := append(hdr, rtattr(syscall.IFLA_IFNAME, append([]byte(name), 0))...)
	if kind != "" {
		// The kernel sets NLA_F_NESTED on IFLA_LINKINFO; IFLA_INFO_DATA follows the kind.
		info := append(rtattr(iflaInfoKind, append([]byte(kind), 0)), rtattr(2, []byte{1, 2, 3, 4})...)
		body = append(body, rtattr(syscall.IFLA_LINKINFO|0x8000, info)...)
	}
	return nlmsg(syscall.RTM_NEWLINK, body)
}

// addrMsg is one RTM_NEWADDR message. A flag above 0xff goes only into
// IFA_FLAGS, like in the kernel; peer is IFA_ADDRESS of a point-to-point link.
func addrMsg(index int32, addr string, flags uint32, peer string) []byte {
	a := netip.MustParseAddr(addr)
	hdr := make([]byte, syscall.SizeofIfAddrmsg)
	hdr[0] = syscall.AF_INET6
	if a.Is4() {
		hdr[0] = syscall.AF_INET
	}
	hdr[2] = byte(flags)
	binary.NativeEndian.PutUint32(hdr[4:8], uint32(index))
	body := hdr
	if a.Is4() {
		body = append(body, rtattr(syscall.IFA_LOCAL, a.AsSlice())...)
		if peer == "" {
			peer = addr
		}
		body = append(body, rtattr(syscall.IFA_ADDRESS, netip.MustParseAddr(peer).AsSlice())...)
	} else {
		body = append(body, rtattr(syscall.IFA_ADDRESS, a.AsSlice())...)
	}
	f := make([]byte, 4)
	binary.NativeEndian.PutUint32(f, flags)
	body = append(body, rtattr(ifaFlags, f)...)
	return nlmsg(syscall.RTM_NEWADDR, body)
}

func dump(msgs ...[]byte) []byte {
	var b []byte
	for _, m := range msgs {
		b = append(b, m...)
	}
	return append(b, nlmsg(syscall.NLMSG_DONE, make([]byte, 4))...)
}

const upFlags = syscall.IFF_UP | syscall.IFF_RUNNING

// A guest with Docker, a VPN, its own bridge and bond, and every kind of
// address that must not leave it.
func TestSelectAddrsFromDump(t *testing.T) {
	links := dump(
		linkMsg(1, "lo", "", upFlags|syscall.IFF_LOOPBACK),
		linkMsg(2, "eth0", "", upFlags),
		linkMsg(3, "br0", "bridge", upFlags),
		linkMsg(4, "bond0", "bond", upFlags),
		linkMsg(5, "docker0", "bridge", upFlags),
		linkMsg(6, "br-0123456789ab", "bridge", upFlags),
		linkMsg(7, "veth1a2b", "veth", upFlags),
		linkMsg(8, "tailscale0", "tun", upFlags),
		linkMsg(9, "wg0", "wireguard", upFlags),
		linkMsg(10, "vxlan.calico", "vxlan", upFlags),
		linkMsg(11, "dummy0", "dummy", upFlags),
		linkMsg(12, "cni0", "bridge", upFlags),
		linkMsg(13, "flannel.1", "vxlan", upFlags),
		linkMsg(14, "virbr0", "bridge", upFlags),
		linkMsg(15, "eth1", "", 0), // down
		linkMsg(16, "eth0.20", "vlan", upFlags),
		linkMsg(17, "mv0", "macvlan", upFlags),
		linkMsg(18, "gen0", "geneve", upFlags),
	)
	addrs := dump(
		addrMsg(1, "127.0.0.1", syscall.IFA_F_PERMANENT, ""),
		addrMsg(1, "::1", syscall.IFA_F_PERMANENT, ""),
		addrMsg(2, "192.0.2.41", syscall.IFA_F_PERMANENT, ""),
		addrMsg(2, "192.0.2.42", syscall.IFA_F_SECONDARY, ""), // IPv4 secondary: kept
		addrMsg(2, "169.254.10.1", 0, ""),
		addrMsg(2, "fe80::1", syscall.IFA_F_PERMANENT, ""),
		addrMsg(2, "2001:db8::41", 0, ""),
		addrMsg(2, "2001:db8::abcd", syscall.IFA_F_TEMPORARY, ""),
		addrMsg(2, "2001:db8::dead", syscall.IFA_F_DEPRECATED, ""),
		addrMsg(2, "2001:db8::beef", syscall.IFA_F_TENTATIVE, ""),
		addrMsg(2, "2001:db8::bad", syscall.IFA_F_DADFAILED, ""),
		addrMsg(2, "fd00::41", 0x200, ""), // IFA_F_MANAGETEMPADDR, only in IFA_FLAGS: kept
		addrMsg(3, "10.1.0.1", 0, ""),
		addrMsg(4, "10.2.0.1", 0, ""),
		addrMsg(5, "172.17.0.1", 0, ""),
		addrMsg(6, "172.18.0.1", 0, ""),
		addrMsg(7, "172.17.0.5", 0, ""),
		addrMsg(8, "100.64.0.7", 0, ""),
		addrMsg(9, "10.99.0.1", 0, ""),
		addrMsg(10, "10.98.0.1", 0, ""),
		addrMsg(11, "10.97.0.1", 0, ""),
		addrMsg(12, "10.96.0.1", 0, ""),
		addrMsg(13, "10.95.0.1", 0, ""),
		addrMsg(14, "192.168.122.1", 0, ""),
		addrMsg(15, "10.3.0.1", 0, ""),
		addrMsg(16, "10.20.0.1", 0, ""),
		addrMsg(17, "10.30.0.1", 0, ""),
		addrMsg(18, "10.94.0.1", 0, ""),
		addrMsg(2, "224.0.0.251", 0, ""),
		addrMsg(2, "0.0.0.0", 0, ""),
	)
	l, err := parseLinks(links)
	if err != nil {
		t.Fatal(err)
	}
	a, err := parseAddrs(addrs)
	if err != nil {
		t.Fatal(err)
	}
	got, cut := selectAddrs(l, a)
	want := []string{"10.1.0.1", "10.2.0.1", "10.20.0.1", "10.30.0.1", "192.0.2.41", "192.0.2.42", "2001:db8::41", "fd00::41"}
	if !slices.Equal(got, want) || cut {
		t.Fatalf("selectAddrs = %q, %v\nwant %q", got, cut, want)
	}
	if err := wire.CheckAddrs(got, cut); err != nil {
		t.Fatal(err)
	}
}

func TestParseLinkKindAndPeer(t *testing.T) {
	l, err := parseLinks(dump(linkMsg(3, "br0", "bridge", upFlags), linkMsg(2, "eth0", "", syscall.IFF_UP|syscall.IFF_LOOPBACK)))
	if err != nil {
		t.Fatal(err)
	}
	want := []nlLink{{Index: 3, Name: "br0", Kind: "bridge", Up: true}, {Index: 2, Name: "eth0", Up: true, Loopback: true}}
	if !slices.Equal(l, want) {
		t.Fatalf("parseLinks = %+v", l)
	}
	// Point-to-point IPv4: IFA_LOCAL is the own address, IFA_ADDRESS the peer.
	a, err := parseAddrs(dump(addrMsg(2, "10.8.0.2", 0, "10.8.0.1")))
	if err != nil || len(a) != 1 || a[0].Addr != netip.MustParseAddr("10.8.0.2") {
		t.Fatalf("parseAddrs = %+v, %v", a, err)
	}
	if _, err := parseLinks(nlmsg(syscall.RTM_NEWLINK, []byte{1, 2})); err == nil {
		t.Fatal("short link message: no error")
	}
	if _, err := parseAddrs(nlmsg(syscall.RTM_NEWADDR, []byte{1, 2})); err == nil {
		t.Fatal("short address message: no error")
	}
}

// A container: eth0 is a veth, so no interface is left after the kind
// filter, and every up interface counts (D-213, until T-038).
func TestSelectAddrsContainerFallback(t *testing.T) {
	links := []nlLink{
		{Index: 1, Name: "lo", Up: true, Loopback: true},
		{Index: 2, Name: "eth0", Kind: "veth", Up: true},
		{Index: 3, Name: "eth1", Kind: "veth"},
	}
	addrs := []nlAddr{
		{Index: 1, Addr: netip.MustParseAddr("127.0.0.1")},
		{Index: 2, Addr: netip.MustParseAddr("172.17.0.5")},
		{Index: 2, Addr: netip.MustParseAddr("fe80::42:acff:fe11:5")},
		{Index: 3, Addr: netip.MustParseAddr("172.18.0.5")},
	}
	got, _ := selectAddrs(links, addrs)
	if !slices.Equal(got, []string{"172.17.0.5"}) {
		t.Fatalf("selectAddrs = %q", got)
	}
	// A guest with a kept interface does not fall back, also when it has no address.
	links = append(links, nlLink{Index: 4, Name: "ens18", Up: true})
	if got, _ := selectAddrs(links, addrs); len(got) != 0 || got == nil {
		t.Fatalf("selectAddrs = %#v, want []", got)
	}
}

func TestSelectAddrsTruncated(t *testing.T) {
	links := []nlLink{{Index: 2, Name: "eth0", Up: true}}
	var addrs []nlAddr
	for i := 20; i > 0; i-- {
		addrs = append(addrs, nlAddr{Index: 2, Addr: netip.AddrFrom4([4]byte{10, 0, 0, byte(i)})})
	}
	got, cut := selectAddrs(links, addrs)
	if len(got) != wire.MaxAddrs || !cut || got[0] != "10.0.0.1" {
		t.Fatalf("selectAddrs = %q, %v", got, cut)
	}
}

// The real dumps of the machine that runs the test give a valid list.
func TestReadAddrsHere(t *testing.T) {
	list, cut, err := readAddrs()
	if err != nil {
		t.Skipf("no netlink here: %v", err)
	}
	if err := wire.CheckAddrs(list, cut); err != nil {
		t.Fatalf("list of this machine: %v", err)
	}
}

// fakeAddrs is an address source that a test changes.
type fakeAddrs struct {
	mu   sync.Mutex
	list []string
	err  error
}

func (f *fakeAddrs) read() ([]string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.list), false, f.err
}

func (f *fakeAddrs) set(list []string, err error) {
	f.mu.Lock()
	f.list, f.err = list, err
	f.mu.Unlock()
}

func addrsOpts(src *fakeAddrs, every, gap time.Duration) func(*Options) {
	return func(o *Options) { o.Addrs, o.AddrsEvery, o.AddrsGap = src.read, every, gap }
}

func TestHelloHoldsAddrs(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	src := &fakeAddrs{list: []string{"192.0.2.41"}}
	startAgentWith(t, ctl, ca, "tok", addrsOpts(src, time.Hour, time.Hour))
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
	h := ctl.reportsOf(wire.ReportHello)[0].Hello
	if !slices.Equal(h.Addrs, []string{"192.0.2.41"}) || h.AddrsTruncated {
		t.Fatalf("hello addrs = %q %v", h.Addrs, h.AddrsTruncated)
	}
}

// An empty list goes out as [], a list that cannot be read as no list.
func TestHelloEmptyAndUnreadable(t *testing.T) {
	for _, c := range []struct {
		name string
		src  *fakeAddrs
		want []string
	}{
		{"empty", &fakeAddrs{}, []string{}},
		{"unreadable", &fakeAddrs{err: errors.New("no netlink")}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			ca := newCA(t)
			ctl := newController(t, ca, "tok")
			startAgentWith(t, ctl, ca, "tok", addrsOpts(c.src, time.Hour, time.Hour))
			waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
			got := ctl.reportsOf(wire.ReportHello)[0].Hello.Addrs
			if (got == nil) != (c.want == nil) || !slices.Equal(got, c.want) {
				t.Fatalf("hello addrs = %#v, want %#v", got, c.want)
			}
		})
	}
}

// A changed list goes out in one round as an addrs report, not as a
// second Hello; an unchanged list does not go out before AddrsEvery.
func TestAddrsReportOnChange(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	src := &fakeAddrs{list: []string{"192.0.2.107"}}
	startAgentWith(t, ctl, ca, "tok", addrsOpts(src, time.Hour, 50*time.Millisecond))
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
	time.Sleep(300 * time.Millisecond)
	if n := len(ctl.reportsOf(wire.ReportAddrs)); n != 0 {
		t.Fatalf("%d addrs reports for an unchanged list", n)
	}
	src.set([]string{"192.0.2.41"}, nil)
	waitFor(t, "addrs report", func() bool { return len(ctl.reportsOf(wire.ReportAddrs)) > 0 })
	r := ctl.reportsOf(wire.ReportAddrs)[0]
	if !slices.Equal(r.Addrs, []string{"192.0.2.41"}) || r.Hello != nil {
		t.Fatalf("addrs report = %+v", r)
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(ctl.reportsOf(wire.ReportAddrs)); n != 1 {
		t.Fatalf("%d addrs reports after one change", n)
	}
	if n := len(ctl.reportsOf(wire.ReportHello)); n != 1 {
		t.Fatalf("%d hellos; an address change must not look like a restart", n)
	}
}

// The list goes out again every AddrsEvery, also when it did not change.
func TestAddrsReportPeriodic(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	src := &fakeAddrs{list: []string{"192.0.2.41"}}
	startAgentWith(t, ctl, ca, "tok", addrsOpts(src, 200*time.Millisecond, 10*time.Millisecond))
	waitFor(t, "two periodic reports", func() bool { return len(ctl.reportsOf(wire.ReportAddrs)) >= 2 })
	for _, r := range ctl.reportsOf(wire.ReportAddrs) {
		if !slices.Equal(r.Addrs, []string{"192.0.2.41"}) {
			t.Fatalf("addrs report = %q", r.Addrs)
		}
	}
}

// A list that changes all the time goes out at most once per AddrsGap.
func TestAddrsReportGap(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	src := &fakeAddrs{list: []string{"10.0.0.1"}}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 2; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				src.set([]string{fmt.Sprintf("10.0.0.%d", i%250+1)}, nil)
			}
		}
	}()
	ta := startAgentWith(t, ctl, ca, "tok", addrsOpts(src, time.Hour, 400*time.Millisecond))
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
	time.Sleep(1300 * time.Millisecond)
	ta.cancel()
	close(stop)
	<-done
	<-ta.done
	// 1.3 s after the Hello with a gap of 0.4 s: at most 3 reports.
	if n := len(ctl.reportsOf(wire.ReportAddrs)); n < 1 || n > 3 {
		t.Fatalf("%d addrs reports in 1.3 s with a gap of 0.4 s", n)
	}
}

// A refused report (400) is not sent again until the list changes.
func TestAddrsReportRefusedIsDropped(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	ctl.addrsCode = 400
	src := &fakeAddrs{list: []string{"192.0.2.41"}}
	startAgentWith(t, ctl, ca, "tok", addrsOpts(src, time.Hour, 20*time.Millisecond))
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
	src.set([]string{"192.0.2.42"}, nil)
	tries := func() int { ctl.mu.Lock(); defer ctl.mu.Unlock(); return ctl.addrsTries }
	waitFor(t, "refused report", func() bool { return tries() == 1 })
	time.Sleep(300 * time.Millisecond)
	if n := tries(); n != 1 {
		t.Fatalf("%d tries of a refused list", n)
	}
	if n := len(ctl.reportsOf(wire.ReportHello)); n != 1 {
		t.Fatalf("%d hellos: a 400 must keep the link", n)
	}
	src.set([]string{"192.0.2.43"}, nil)
	waitFor(t, "report of the next change", func() bool { return tries() == 2 })
}

// A busy controller (429) keeps the link; the list goes out again later.
func TestAddrsReportBusyRetries(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	ctl.addrsCode = 429
	src := &fakeAddrs{list: []string{"192.0.2.41"}}
	startAgentWith(t, ctl, ca, "tok", addrsOpts(src, time.Hour, 20*time.Millisecond))
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
	src.set([]string{"192.0.2.42"}, nil)
	tries := func() int { ctl.mu.Lock(); defer ctl.mu.Unlock(); return ctl.addrsTries }
	waitFor(t, "two tries", func() bool { return tries() >= 2 })
	ctl.mu.Lock()
	ctl.addrsCode = 0
	ctl.mu.Unlock()
	waitFor(t, "taken report", func() bool { return len(ctl.reportsOf(wire.ReportAddrs)) == 1 })
	if n := len(ctl.reportsOf(wire.ReportHello)); n != 1 {
		t.Fatalf("%d hellos: a 429 must keep the link", n)
	}
}
