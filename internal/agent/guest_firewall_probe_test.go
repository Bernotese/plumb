//go:build guest

package agent

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestGuestFirewallProbeLoop hunts the O1 flake of TestGuestFirewall
// (release plan, section 5a, P2; T-260): in CI the first probe of that test
// twice timed out before any apply, with no rule in the way and the
// listener up (T-200, T-250). lxd.sh runs this test first after the boot of
// each instance, so the fresh-guest state is the one that failed.
//
// Each round makes the network of the test again (the netns and the veth
// pair with the fixed MACs, fwNetUp), starts a fresh listener on a new port
// and probes it once from the peer with the connect limit of
// TestGuestFirewall (fwConnectLimit, 3 s; L1 of #394). No nft table
// is loaded, so every probe must connect: a timeout or a refusal fails the
// test at once, with the T-250 dump of that moment. No retry: a slow first
// connect counts too (rounds slower than 1 s are logged).
//
// PLUMB_FW_PROBE_ROUNDS sets the rounds (default 100; lxd.sh runs four
// instances, so about 200 per instance kind and 400 per job).
func TestGuestFirewallProbeLoop(t *testing.T) {
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires root in the systemd guest")
	}
	rounds := 100
	if v := os.Getenv("PLUMB_FW_PROBE_ROUNDS"); v != "" {
		if rounds, err = strconv.Atoi(v); err != nil || rounds < 1 {
			t.Fatalf("PLUMB_FW_PROBE_ROUNDS=%q", v)
		}
	}
	if out := liveTable(t); out != "" {
		t.Fatalf("an inet plumb table is loaded before the loop; the probe needs none:\n%s", out)
	}
	var slow int
	loopStart := time.Now()
	var worst time.Duration
	for i := 1; i <= rounds; i++ {
		down := fwNetUp(t)
		ln, err := net.Listen("tcp", net.JoinHostPort(fwHost, "0"))
		if err != nil {
			t.Fatalf("round %d: listen: %v", i, err)
		}
		listenAt := time.Now()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		start := time.Now()
		err = reach(port)
		took := time.Since(start)
		if err != nil {
			// The dump while the failed state is still there, then stop:
			// the first failure is the evidence (a fix card, P2).
			t.Fatalf("round %d of %d: port %s not reachable on a fresh link: %v\n%s",
				i, rounds, port, err, reachDiag(port, listenAt, time.Time{}))
		}
		if took > worst {
			worst = took
		}
		if took > time.Second {
			slow++
			t.Logf("round %d: the connect took %s", i, took.Round(time.Millisecond))
		}
		ln.Close()
		down()
	}
	t.Logf("O1 probe loop: %d of %d rounds connected, 0 failed; slowest connect %s (limit %s); %d slower than 1 s; loop time %s",
		rounds, rounds, worst.Round(time.Millisecond), fwConnectLimit, slow, time.Since(loopStart).Round(time.Millisecond))
}
