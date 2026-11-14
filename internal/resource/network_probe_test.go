package resource

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"
)

var (
	ownMAC   = net.HardwareAddr{0xbc, 0x24, 0x11, 0, 0, 1}
	otherMAC = net.HardwareAddr{0xbc, 0x24, 0x11, 0, 0, 2}
	probe4   = netip.MustParseAddr("192.0.2.10")
	probe6   = netip.MustParseAddr("2001:db8::10")
)

func TestARPProbeRoundTrip(t *testing.T) {
	p, ok := parseARP(arpProbe(probe4, ownMAC))
	if !ok || p.op != arpOpRequest || !p.spa.IsUnspecified() || p.tpa != probe4 || p.sha.String() != ownMAC.String() {
		t.Fatalf("probe: %+v %v", p, ok)
	}
	// Our own probe is no conflict; another host's probe or reply is.
	if arpConflict(p, probe4, ownMAC) {
		t.Error("own probe is a conflict")
	}
	if !arpConflict(p, probe4, otherMAC) {
		t.Error("another host probing the address is no conflict")
	}
	reply := arpPacket{op: arpOpReply, sha: otherMAC, spa: probe4, tpa: netip.MustParseAddr("0.0.0.0")}
	if !arpConflict(reply, probe4, ownMAC) {
		t.Error("a reply from the address is no conflict")
	}
	if arpConflict(arpPacket{op: arpOpReply, sha: otherMAC, spa: netip.MustParseAddr("192.0.2.11")}, probe4, ownMAC) {
		t.Error("a reply for another address is a conflict")
	}
	if arpConflict(arpPacket{op: 9, sha: otherMAC, spa: probe4}, probe4, ownMAC) {
		t.Error("an unknown operation is a conflict")
	}
}

func TestNSProbe(t *testing.T) {
	ns := nsProbe(probe6, ownMAC)
	if ns[0] != icmp6NS || len(ns) != 32 || netip.AddrFrom16([16]byte(ns[8:24])) != probe6 {
		t.Fatalf("ns: %x", ns)
	}
	na := append([]byte{icmp6NA, 0, 0, 0, 0x20, 0, 0, 0}, ns[8:24]...)
	if got, ok := parseNA(na); !ok || got != probe6 {
		t.Fatalf("na: %v %v", got, ok)
	}
	if _, ok := parseNA(ns); ok {
		t.Error("an NS read as an NA")
	}
	if got := solicitedNode(probe6); got != netip.MustParseAddr("ff02::1:ff00:10") {
		t.Errorf("solicited node: %v", got)
	}
}

// Review of T-150 (pl-lead): a short or odd packet never panics the agent.
func FuzzParseARP(f *testing.F) {
	f.Add(arpProbe(probe4, ownMAC))
	f.Add([]byte{})
	f.Add(make([]byte, arpLen-1))
	f.Fuzz(func(t *testing.T, b []byte) {
		if p, ok := parseARP(b); ok {
			_ = arpConflict(p, probe4, ownMAC)
		}
	})
}

func FuzzParseNA(f *testing.F) {
	f.Add(append([]byte{icmp6NA, 0, 0, 0, 0, 0, 0, 0}, probe6.AsSlice()...))
	f.Add([]byte{icmp6NA})
	f.Add(make([]byte, icmp6NALen-1))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = parseNA(b)
	})
}

// A probe on a NIC that does not exist fails at once; a cancelled context
// ends the wait (bounded, review of T-150).
func TestProbeBounded(t *testing.T) {
	if err := probeDuplicates(context.Background(), "plumb-no-such-nic", []netip.Addr{probe4}); err == nil {
		t.Error("probe on a missing NIC passed")
	}
	if got := listenUntil(context.Background()); time.Until(got) > probeListen {
		t.Errorf("wait %v longer than %v", time.Until(got), probeListen)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if got := listenUntil(ctx); time.Until(got) > 20*time.Millisecond {
		t.Errorf("the context deadline does not bound the wait: %v", time.Until(got))
	}
}
