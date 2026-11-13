//go:build guest

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
func reachable(port string) bool {
	err := exec.Command("nsenter", "--net=/run/netns/"+fwNetns, "timeout", "3", "bash", "-c",
		"exec 3<>/dev/tcp/"+fwHost+"/"+port).Run()
	return err == nil
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
	_ = exec.Command("ip", "netns", "del", fwNetns).Run()
	_ = exec.Command("ip", "link", "del", "plumbfw0").Run()
	fwSh(t, "ip", "netns", "add", fwNetns)
	t.Cleanup(func() {
		_ = exec.Command("ip", "link", "del", "plumbfw0").Run()
		_ = exec.Command("ip", "netns", "del", fwNetns).Run()
	})
	fwSh(t, "ip", "link", "add", "plumbfw0", "type", "veth", "peer", "name", "plumbfw1")
	fwSh(t, "ip", "link", "set", "plumbfw1", "netns", fwNetns)
	fwSh(t, "ip", "addr", "add", fwHost+"/24", "dev", "plumbfw0")
	fwSh(t, "ip", "link", "set", "plumbfw0", "up")
	fwSh(t, "nsenter", "--net=/run/netns/"+fwNetns, "ip", "addr", "add", fwPeer+"/24", "dev", "plumbfw1")
	fwSh(t, "nsenter", "--net=/run/netns/"+fwNetns, "ip", "link", "set", "plumbfw1", "up")
	fwSh(t, "nsenter", "--net=/run/netns/"+fwNetns, "ip", "link", "set", "lo", "up")
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
				t.Fatalf("port %s reachable = %v, want %v\n%s", port, !want, want, liveTable(t))
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
