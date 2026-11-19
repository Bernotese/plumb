//go:build guest

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Addresses of the test network: the guest side (fwHost) and a peer in its
// own network namespace (fwPeer), so that packets come in through a real
// interface and pass the input chain (traffic of the guest to itself goes
// through lo, which the table always accepts).
const (
	fwNetns = "plumbfwpeer"
	fwHost  = "198.51.100.1"
	fwPeer  = "198.51.100.2"
	// Fixed, locally administered MACs of the veth pair (T-250). A veth
	// made without an address gets a random one, and udev's default
	// MACAddressPolicy=persistent may give it a new one when it handles
	// the add event, late on a busy fresh guest: the peer then keeps
	// sending to the old MAC (its neighbour entry stays REACHABLE), and
	// every connect times out with no rule in the way. A set address
	// (addr_assign_type 3) is never changed by udev.
	fwHostMAC = "02:00:5e:fa:00:01"
	fwPeerMAC = "02:00:5e:fa:00:02"
)

func fwSh(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func liveTable(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("nft", "list", "table", "inet", "plumb").CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "No such file or directory") {
			return ""
		}
		t.Fatalf("nft list: %v\n%s", err, out)
	}
	return string(out)
}

// Commands in the peer namespace run with nsenter, not "ip netns exec":
// that remounts /sys, which an unprivileged container refuses.

// reachable reports whether the peer can open a TCP connection to the
// guest port.
func reachable(port string) bool { return reach(port) == nil }

// reach opens a TCP connection from the peer namespace to the guest port,
// in this process (T-250): a goroutine locked to its thread enters the
// namespace and dials with the fwConnectLimit deadline. A connect that works is
// "reachable"; the error says whether it timed out or was refused, and
// how long it took. The thread is left locked, so it ends with the
// goroutine and never runs other code in the peer namespace.
func reach(port string) error { return reachWithin(port, fwConnectLimit) }

// fwConnectLimit is the connect limit of every probe: TestGuestFirewall
// and its probe loop (T-260) fail at the same time, so a slow connect that
// fails the test also fails the loop (L1 of #394).
const fwConnectLimit = 3 * time.Second

// reachWithin is reach with its own connect limit.
func reachWithin(port string, limit time.Duration) error {
	type result struct {
		err  error
		took time.Duration
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread() // never unlocked: the thread exits with the goroutine
		f, err := os.Open("/run/netns/" + fwNetns)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer f.Close()
		if err := unix.Setns(int(f.Fd()), unix.CLONE_NEWNET); err != nil {
			done <- result{err: fmt.Errorf("setns: %w", err)}
			return
		}
		start := time.Now()
		c, err := net.DialTimeout("tcp", net.JoinHostPort(fwHost, port), limit)
		if err == nil {
			c.Close()
		}
		done <- result{err: err, took: time.Since(start)}
	}()
	r := <-done
	if r.err != nil {
		kind := "error"
		var ne net.Error
		switch {
		case errors.As(r.err, &ne) && ne.Timeout():
			kind = "timeout"
		case errors.Is(r.err, unix.ECONNREFUSED):
			kind = "refused"
		}
		return fmt.Errorf("%s after %s: %v", kind, r.took.Round(time.Millisecond), r.err)
	}
	return nil
}

// bashReach is the probe before T-250 (nsenter, bash /dev/tcp, timeout
// 3), kept as a second opinion in the dump.
func bashReach(port string) error {
	out, err := exec.Command("nsenter", "--net=/run/netns/"+fwNetns, "timeout", "3", "bash", "-c",
		"exec 3<>/dev/tcp/"+fwHost+"/"+port).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// reachDiag is the state of the guest when a reach check fails (T-200):
// the whole ruleset, addresses, routes, listeners, connection tracking
// (when the tool is there) and the neighbour port, so a rare failure in CI
// gives its cause. It only reads.
func reachDiag(port string, listenAt, applyAt time.Time) string {
	var b strings.Builder
	run := func(name string, args ...string) {
		out, err := exec.Command(name, args...).CombinedOutput()
		fmt.Fprintf(&b, "--- %s %s", name, strings.Join(args, " "))
		if err != nil {
			fmt.Fprintf(&b, " (%v)", err)
		}
		fmt.Fprintf(&b, "\n%s\n", bytes.TrimSpace(out))
	}
	now := time.Now()
	fmt.Fprintf(&b, "--- times: listener started %s ago", now.Sub(listenAt).Round(time.Millisecond))
	if applyAt.IsZero() {
		b.WriteString(", no apply returned yet\n")
	} else {
		fmt.Fprintf(&b, ", last apply returned %s ago\n", now.Sub(applyAt).Round(time.Millisecond))
	}
	if err := reach(port); err != nil {
		fmt.Fprintf(&b, "--- port %s now: not reachable: %v\n", port, err)
	} else {
		fmt.Fprintf(&b, "--- port %s now: reachable\n", port)
	}
	other := "8081"
	if port == "8081" {
		other = "8080"
	}
	if err := reach(other); err != nil {
		fmt.Fprintf(&b, "--- neighbour port %s: not reachable: %v\n", other, err)
	} else {
		fmt.Fprintf(&b, "--- neighbour port %s: reachable\n", other)
	}
	if err := bashReach(port); err != nil {
		fmt.Fprintf(&b, "--- port %s by the bash probe: not reachable: %v\n", port, err)
	} else {
		fmt.Fprintf(&b, "--- port %s by the bash probe: reachable\n", port)
	}
	run("nft", "list", "ruleset")
	run("ip", "-br", "addr")
	run("ip", "route")
	run("nsenter", "--net=/run/netns/"+fwNetns, "ip", "-br", "addr")
	run("nsenter", "--net=/run/netns/"+fwNetns, "ip", "neigh")
	// T-250: the MACs (and how they were assigned: 3 = set, 1 = random),
	// both neighbour tables, policy routing, the TCP states of both sides,
	// a ping, and who may touch the link.
	run("ip", "-d", "link", "show", "plumbfw0")
	run("cat", "/sys/class/net/plumbfw0/addr_assign_type")
	run("nsenter", "--net=/run/netns/"+fwNetns, "ip", "-d", "link", "show", "plumbfw1")
	run("ip", "neigh", "show", "dev", "plumbfw0")
	run("ip", "rule")
	run("ss", "-tan")
	run("nsenter", "--net=/run/netns/"+fwNetns, "ss", "-tan")
	run("nsenter", "--net=/run/netns/"+fwNetns, "ping", "-c", "1", "-W", "1", fwHost)
	run("sysctl", "net.ipv4.conf.plumbfw0.rp_filter", "net.ipv4.conf.plumbfw0.arp_ignore", "net.ipv4.conf.all.rp_filter")
	run("systemctl", "is-active", "systemd-udevd", "systemd-networkd", "NetworkManager")
	if _, err := exec.LookPath("networkctl"); err == nil {
		run("networkctl", "status", "plumbfw0", "--no-pager", "-n", "0")
	}
	run("journalctl", "-b", "--no-pager", "-n", "30", "-u", "systemd-udevd", "-u", "systemd-networkd")
	run("ss", "-ltn")
	if _, err := exec.LookPath("conntrack"); err == nil {
		run("conntrack", "-L")
	} else {
		b.WriteString("--- conntrack: not installed\n")
	}
	return b.String()
}

// fwNetUp makes the peer namespace and the veth pair with the fixed MACs
// (T-250) and returns the function that removes them. TestGuestFirewall and
// TestGuestFirewallProbeLoop (T-260) share it, so the loop probes the same
// network as the test.
func fwNetUp(t *testing.T) func() {
	t.Helper()
	down := func() {
		_ = exec.Command("ip", "link", "del", "plumbfw0").Run()
		_ = exec.Command("ip", "netns", "del", fwNetns).Run()
	}
	down()
	t.Cleanup(down) // also when a step below fails; a second delete is a no-op
	fwSh(t, "ip", "netns", "add", fwNetns)
	fwSh(t, "ip", "link", "add", "plumbfw0", "address", fwHostMAC, "type", "veth", "peer", "name", "plumbfw1", "address", fwPeerMAC)
	// Let udev handle the add events first, where it runs (a VM; T-250).
	if exec.Command("systemctl", "is-active", "--quiet", "systemd-udevd").Run() == nil {
		_ = exec.Command("udevadm", "settle", "--timeout=10").Run()
	}
	fwSh(t, "ip", "link", "set", "plumbfw1", "netns", fwNetns)
	fwSh(t, "ip", "addr", "add", fwHost+"/24", "dev", "plumbfw0")
	fwSh(t, "ip", "link", "set", "plumbfw0", "up")
	fwSh(t, "nsenter", "--net=/run/netns/"+fwNetns, "ip", "addr", "add", fwPeer+"/24", "dev", "plumbfw1")
	fwSh(t, "nsenter", "--net=/run/netns/"+fwNetns, "ip", "link", "set", "plumbfw1", "up")
	fwSh(t, "nsenter", "--net=/run/netns/"+fwNetns, "ip", "link", "set", "lo", "up")
	// The MACs are the set ones: nothing changed them (T-250).
	for _, l := range []struct {
		mac string
		cmd []string
	}{
		{fwHostMAC, []string{"ip", "-br", "link", "show", "plumbfw0"}},
		{fwPeerMAC, []string{"nsenter", "--net=/run/netns/" + fwNetns, "ip", "-br", "link", "show", "plumbfw1"}},
	} {
		if out := fwSh(t, l.cmd[0], l.cmd[1:]...); !strings.Contains(out, l.mac) {
			t.Fatalf("the veth MAC is not the set %s: %s", l.mac, out)
		}
	}
	return down
}

// TestGuestFirewall proves the resource firewall and its safe apply with
// real nftables and systemd (T-116): Debian 13 and Ubuntu, in the
// privileged containers of run.sh and in unprivileged LXC containers and
// VMs of lxd.sh. Needs the package nftables.
func TestGuestFirewall(t *testing.T) {
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires root in the systemd guest")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Fatal("requires the package nftables")
	}
	t.Cleanup(func() {
		_ = exec.Command("systemctl", "stop", resource.FirewallRollback+".timer").Run()
		_ = exec.Command("systemctl", "disable", resource.FirewallUnit).Run()
		_ = exec.Command("nft", "delete", "table", "inet", "plumb").Run()
		os.Remove(resource.FirewallBootFile)
		os.Remove("/etc/systemd/system/" + resource.FirewallUnit)
		_ = exec.Command("systemctl", "daemon-reload").Run()
	})
	if err := os.MkdirAll("/etc/plumb-agent", 0o755); err != nil {
		t.Fatal(err)
	}

	// Keep an unrelated table through apply, rollback, boot loading and removal.
	fwSh(t, "nft", "add", "table", "inet", "plumb_qa_foreign")
	t.Cleanup(func() { _ = exec.Command("nft", "delete", "table", "inet", "plumb_qa_foreign").Run() })
	foreignSurvives := func() { t.Helper(); fwSh(t, "nft", "list", "table", "inet", "plumb_qa_foreign") }
	// The peer namespace.
	fwNetUp(t)
	listenAt := time.Now()
	var applyAt time.Time // when the last apply returned (T-200 diagnostics)
	for _, port := range []string{"8080", "8081"} {
		ln, err := net.Listen("tcp", fwHost+":"+port)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
	}
	waitReach := func(port string, want bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			if reachable(port) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("port %s reachable = %v, want %v\n%s\n%s", port, !want, want, liveTable(t), reachDiag(port, listenAt, applyAt))
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	waitReach("8080", true)
	waitReach("8081", true)

	// The controller, with a switch that refuses confirm reports.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "link.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	deliveries := make(chan link.Delivery, 64)
	ctl, err := link.New(link.Config{Dir: filepath.Join(t.TempDir(), "link"), Store: st,
		OnReport: func(_ context.Context, d link.Delivery) error { deliveries <- d; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	var refuse atomic.Bool
	var confirms atomic.Int32
	h := ctl.Handler()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wire.PathReport {
			b, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(b))
			var rep wire.Report
			if json.Unmarshal(b, &rep) == nil && rep.Type == wire.ReportConfirm {
				confirms.Add(1)
				if refuse.Load() {
					w.WriteHeader(http.StatusConflict)
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	}))
	srv.TLS = ctl.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	token, _, err := ctl.NewEnrollToken(ctx, "guest")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "token"), token)
	state := filepath.Join(dir, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{Config: Config{URL: srv.URL, CA: string(ctl.CA().CertPEM),
		TokenFile: filepath.Join(dir, "token"), StateDir: state, FullCheck: Duration(time.Hour)},
		Root: "/", Hostname: "guest", CloudInitFlag: filepath.Join(dir, "cloud-init.disabled"),
		Debounce: 500 * time.Millisecond, PollWait: 1, ConfirmWindow: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	result := func(id string) wire.Report {
		t.Helper()
		deadline := time.After(60 * time.Second)
		for {
			select {
			case d := <-deliveries:
				if d.Report.Type == wire.ReportResult && d.Report.CommandID == id {
					return d.Report
				}
			case <-deadline:
				t.Fatalf("no result for %s", id)
			}
		}
	}
	apply := func(attrs map[string]any) wire.Item {
		t.Helper()
		r := fwResource(attrs)
		id, err := ctl.Send("guest", wire.CommandApply, fwApply(r))
		if err != nil {
			t.Fatal(err)
		}
		rep := result(id)
		applyAt = time.Now()
		if rep.Error != "" || len(rep.Items) != 1 {
			t.Fatalf("apply: %+v", rep)
		}
		foreignSurvives()
		return rep.Items[0]
	}

	// 1. Policy drop, 8080 open: confirmed, filtered, boot unit enabled.
	if it := apply(map[string]any{"tcp_ports": "8080"}); it.Status != wire.StatusApplied {
		t.Fatalf("apply: %+v", it)
	}
	if confirms.Load() == 0 {
		t.Fatal("no confirm round trip")
	}
	waitReach("8080", true)
	waitReach("8081", false)
	confirmed := liveTable(t)
	if !strings.Contains(confirmed, "8080") {
		t.Fatalf("table: %s", confirmed)
	}
	boot, err := os.ReadFile(resource.FirewallBootFile)
	if err != nil || !strings.Contains(string(boot), "tcp dport { 8080 } accept") {
		t.Fatalf("boot file: %v\n%s", err, boot)
	}
	if out, _ := exec.Command("systemctl", "is-enabled", resource.FirewallUnit).Output(); strings.TrimSpace(string(out)) != "enabled" {
		t.Errorf("%s is %q", resource.FirewallUnit, out)
	}
	if exec.Command("systemctl", "is-active", "--quiet", resource.FirewallRollback+".timer").Run() == nil {
		t.Error("rollback timer still active after the commit")
	}
	if _, err := os.Stat(filepath.Join(state, safeApplyMarker)); !os.IsNotExist(err) {
		t.Error("marker left after the commit")
	}
	// Observe again: no difference (the listing of nft is stable).
	rs, err := resource.New(fwResource(map[string]any{"tcp_ports": "8080"}), a.env)
	if err != nil {
		t.Fatal(err)
	}
	if obs, err := rs.Observe(ctx); err != nil || len(rs.Diff(obs)) != 0 {
		t.Fatalf("after the apply: %v %+v", err, rs.Diff(obs))
	}

	// T-166 (host F2 of T-158): a rule added by hand shows only as the
	// opaque attribute of the type, which the controller treats as guest
	// data for the whole type (D-359). Then the agent's own table again.
	fwSh(t, "nft", "add", "rule", "inet", "plumb", "input", "tcp", "dport", "9090", "accept")
	handObs, err := rs.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fwType, _ := resource.Lookup("firewall")
	var handAttrs []string
	for _, c := range rs.Diff(handObs) {
		handAttrs = append(handAttrs, c.Attr)
	}
	if len(fwType.Opaque) == 0 || !slices.Contains(handAttrs, fwType.Opaque[0]) {
		t.Fatalf("hand rule: changes %v, want the opaque attribute %v", handAttrs, fwType.Opaque)
	}
	if it := apply(map[string]any{"tcp_ports": "8080"}); it.Status != wire.StatusApplied {
		t.Fatalf("revert of the hand rule: %+v", it)
	}
	if got := liveTable(t); got != confirmed {
		t.Fatalf("after the revert:\n%s\nwant\n%s", got, confirmed)
	}

	// 2. A forced rollback: the controller refuses the round trip.
	refuse.Store(true)
	it := apply(map[string]any{"tcp_ports": "8080,8081"})
	refuse.Store(false)
	if it.Status != wire.StatusFailed || !it.RolledBack {
		t.Fatalf("forced rollback: %+v", it)
	}
	if got := liveTable(t); got != confirmed {
		t.Fatalf("after the rollback:\n%s\nwant\n%s", got, confirmed)
	}
	waitReach("8081", false)

	// 3. The rollback timer restores when nobody else does (an agent that
	// died in the window and stays down, review F1 of T-111).
	old := resource.SetFirewallRollbackAfter(4)
	defer resource.SetFirewallRollbackAfter(old)
	open8081, err := resource.New(fwResource(map[string]any{"tcp_ports": "8080,8081"}), a.env)
	if err != nil {
		t.Fatal(err)
	}
	sa := open8081.(resource.SafeApplier)
	if err := sa.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	obs, _ := open8081.Observe(ctx)
	if err := open8081.Apply(ctx, open8081.Diff(obs)); err != nil {
		t.Fatal(err)
	}
	waitReach("8081", true)
	if err := exec.Command("systemctl", "is-active", "--quiet", resource.FirewallRollback+".timer").Run(); err != nil {
		t.Fatal("rollback timer not active in the window")
	}
	deadline := time.Now().Add(20 * time.Second)
	for liveTable(t) != confirmed {
		if time.Now().After(deadline) {
			t.Fatalf("the timer did not restore:\n%s", liveTable(t))
		}
		time.Sleep(500 * time.Millisecond)
	}
	waitReach("8081", false)
	foreignSurvives()

	// 4. The boot unit loads the confirmed rules.
	fwSh(t, "nft", "delete", "table", "inet", "plumb")
	waitReach("8081", true)
	fwSh(t, "systemctl", "restart", resource.FirewallUnit)
	if got := liveTable(t); got != confirmed {
		t.Fatalf("boot unit:\n%s\nwant\n%s", got, confirmed)
	}

	foreignSurvives()

	// 6. The starter Block firewall (T-119), rendered by the controller's own
	// code: package nftables, then the table with SSH and the listed port.
	list := starterRender(t, []any{map[string]any{"use": "firewall", "params": map[string]any{"tcp_ports": "8081"}}}, nil)
	if len(list) != 2 || list[0].ID != "package:nftables" || list[1].ID != "firewall:main" {
		t.Fatalf("rendered: %+v", list)
	}
	id, err := ctl.Send("guest", wire.CommandApply, wire.ApplyBody{PlanID: 2, Resources: list,
		Planned: unverified(list...), MaxRisk: wire.RiskDisruptive})
	if err != nil {
		t.Fatal(err)
	}
	rep := result(id)
	if rep.Error != "" || len(rep.Items) != 2 {
		t.Fatalf("starter Block: %+v", rep)
	}
	for _, it := range rep.Items {
		if it.Status != wire.StatusApplied && it.Status != wire.StatusUnchanged {
			t.Fatalf("starter Block item: %+v", it)
		}
	}
	waitReach("8081", true)
	waitReach("8080", false)
	// The boot file holds the confirmed rules in the form the agent writes.
	boot, err = os.ReadFile(resource.FirewallBootFile)
	if err != nil || !strings.Contains(string(boot), "tcp dport { 22 } accept") ||
		!strings.Contains(string(boot), "tcp dport { 8081 } accept") {
		t.Fatalf("starter Block boot file: %v\n%s", err, boot)
	}

	// 5. state absent: no table, no boot file, unit disabled.
	if it := apply(map[string]any{"state": "absent"}); it.Status != wire.StatusApplied {
		t.Fatalf("absent: %+v", it)
	}
	if got := liveTable(t); got != "" {
		t.Fatalf("table left: %s", got)
	}
	if _, err := os.Stat(resource.FirewallBootFile); !os.IsNotExist(err) {
		t.Error("boot file left")
	}
	if out, _ := exec.Command("systemctl", "is-enabled", resource.FirewallUnit).Output(); strings.TrimSpace(string(out)) == "enabled" {
		t.Error("unit still enabled")
	}
	waitReach("8081", true)
}
