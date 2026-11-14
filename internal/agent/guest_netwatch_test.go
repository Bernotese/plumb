//go:build guest

package agent

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// TestGuestNetwatch proves the live drift of ip and nft hand changes
// (D-367, T-181) with a real agent, real netlink, netplan and nftables:
// the full check is one hour away, so only the netlink trigger can send
// the reports, each within 10 s of the hand change:
//   - ip addr del of a managed address → drift on addresses;
//   - ip route del, then ip route add of the managed route → drift, then
//     the drift is resolved;
//   - nft add rule in the Plumb table → drift on the firewall.
//
// It runs in the cloud VMs of lxd.sh (PLUMB_NETWATCH_GUEST=1), after
// TestGuestNetwork, which needs the same netplan set-up.
func TestGuestNetwatch(t *testing.T) {
	if os.Getenv("PLUMB_NETWATCH_GUEST") != "1" {
		t.Skip("live watch needs a cloud VM with netplan and nftables; only lxd.sh runs it (PLUMB_NETWATCH_GUEST=1)")
	}
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires root in the systemd guest")
	}
	for _, tool := range []string{"netplan", "nft"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal("requires " + tool)
		}
	}
	dev, gw, _ := guestDefault(t)
	t.Cleanup(func() {
		for _, kind := range []string{"network", "firewall"} {
			_ = exec.Command("systemctl", "stop", resource.RollbackUnit(kind)+".timer").Run()
		}
		_ = exec.Command("systemctl", "disable", resource.NetworkBootUnit, resource.FirewallUnit).Run()
		os.Remove("/etc/systemd/system/" + resource.NetworkBootUnit)
		os.Remove("/etc/systemd/system/" + resource.FirewallUnit)
		os.Remove(resource.FirewallBootFile)
		_ = exec.Command("nft", "delete", "table", "inet", "plumb").Run()
		_ = exec.Command("systemctl", "daemon-reload").Run()
		os.Remove(resource.NetworkFile("ci"))
		_ = exec.Command("netplan", "apply").Run()
	})
	if err := os.MkdirAll("/etc/plumb-agent", 0o755); err != nil {
		t.Fatal(err)
	}

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
	ts := httptest.NewUnstartedServer(ctl.Handler())
	ts.TLS = ctl.TLSConfig()
	ts.StartTLS()
	t.Cleanup(ts.Close)
	srv := ts.URL
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
	// Debounce 0: the default of 2 s, as in a real agent.
	a, err := New(Options{Config: Config{URL: srv, CA: string(ctl.CA().CertPEM),
		TokenFile: filepath.Join(dir, "token"), StateDir: state, FullCheck: Duration(time.Hour)},
		Root: "/", Hostname: "guest", CloudInitFlag: filepath.Join(dir, "cloud-init.disabled"),
		PollWait: 1, ConfirmWindow: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	a.env.Controller = gw.String() // the real NIC, not the loopback of the test controller
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	// The hello says that both types are watched live.
	deadline := time.After(30 * time.Second)
	for hello := false; !hello; {
		select {
		case d := <-deliveries:
			if d.Report.Type != wire.ReportHello {
				continue
			}
			lw := d.Report.Hello.LiveWatch
			if lw == nil || !lw.Network || !lw.Firewall {
				t.Fatalf("hello live_watch = %+v, want both true in a VM", lw)
			}
			hello = true
		case <-deadline:
			t.Fatal("no hello")
		}
	}

	apply := func(r wire.Resource) {
		t.Helper()
		id, err := ctl.Send("guest", wire.CommandApply, fwApply(r))
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.After(90 * time.Second)
		for {
			select {
			case d := <-deliveries:
				if d.Report.Type == wire.ReportDrift {
					t.Fatalf("drift during the own apply: %+v", d.Report.Items)
				}
				if d.Report.Type == wire.ReportResult && d.Report.CommandID == id {
					if d.Report.Error != "" || len(d.Report.Items) != 1 || d.Report.Items[0].Status != wire.StatusApplied {
						t.Fatalf("apply %s: %+v", r.ID, d.Report)
					}
					return
				}
			case <-deadline:
				t.Fatalf("no result for %s", id)
			}
		}
	}
	// within runs the hand change and waits up to 10 s for a drift item of
	// res with the status; for differs, the changes must hold attr.
	within := func(step, res string, status wire.Status, attr string, hand ...string) {
		t.Helper()
		start := time.Now()
		fwSh(t, hand[0], hand[1:]...)
		deadline := time.After(10 * time.Second)
		for {
			select {
			case d := <-deliveries:
				if d.Report.Type != wire.ReportDrift {
					continue
				}
				for _, it := range d.Report.Items {
					if it.Resource != res || it.Status != status {
						continue
					}
					if attr != "" && !slices.ContainsFunc(it.Changes, func(c wire.Change) bool { return c.Attr == attr }) {
						continue
					}
					t.Logf("%s: %s %s after %v", step, res, status, time.Since(start).Round(100*time.Millisecond))
					return
				}
			case <-deadline:
				t.Fatalf("%s: no %s report for %s within 10 s (full check is 1 h away)", step, status, res)
			}
		}
	}

	// Let the agent's own apply events pass before the hand changes.
	settle := func() {
		t.Helper()
		quiet := time.After(netGap + 3*time.Second)
		for {
			select {
			case d := <-deliveries:
				if d.Report.Type == wire.ReportDrift {
					t.Fatalf("drift without a hand change: %+v", d.Report.Items)
				}
			case <-quiet:
				return
			}
		}
	}

	nw := netResource(map[string]any{"addresses": "192.0.2.10/24", "routes": "198.51.100.0/24 via 192.0.2.1"})
	apply(nw)
	settle()
	within("addr del", nw.ID, wire.StatusDiffers, "addresses", "ip", "addr", "del", "192.0.2.10/24", "dev", dev)
	fwSh(t, "ip", "addr", "add", "192.0.2.10/24", "dev", dev)
	// The address is back; its route went with it when it was removed.
	if liveHas(t, dev, "198.51.100.0/24 via 192.0.2.1") {
		fwSh(t, "ip", "route", "del", "198.51.100.0/24", "via", "192.0.2.1", "dev", dev)
	}
	within("route gone", nw.ID, wire.StatusDiffers, "routes", "true")
	within("route add", nw.ID, wire.StatusUnchanged, "", "ip", "route", "add", "198.51.100.0/24", "via", "192.0.2.1", "dev", dev)

	fw := fwResource(map[string]any{"tcp_ports": "8080"})
	apply(fw)
	settle()
	within("nft add rule", fw.ID, wire.StatusDiffers, "", "nft", "add", "rule", "inet", "plumb", "input", "tcp", "dport", "8081", "accept")
}
