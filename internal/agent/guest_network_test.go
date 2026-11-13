//go:build guest

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
	"go.yaml.in/yaml/v3"
)

// netplanEntry is the part of an ethernets entry of `netplan get` that the
// network resource writes or must keep.
type netplanEntry struct {
	Match       map[string]any `yaml:"match"`
	SetName     string         `yaml:"set-name"`
	Addresses   []string       `yaml:"addresses"`
	Routes      []struct{ To, Via string }
	Nameservers struct {
		Addresses []string `yaml:"addresses"`
		Search    []string `yaml:"search"`
	} `yaml:"nameservers"`
}

func netplanGet(t *testing.T, root string) netplanEntry {
	t.Helper()
	out, err := exec.Command("netplan", "get", "--root-dir", root, "ethernets.eth0").CombinedOutput()
	if err != nil {
		t.Fatalf("netplan get: %v\n%s", err, out)
	}
	var e netplanEntry
	if err := yaml.Unmarshal(out, &e); err != nil {
		t.Fatalf("parse netplan get: %v\n%s", err, out)
	}
	return e
}

// TestGuestNetplanMerge proves the netplan merge rule that D-348 builds on
// (T-150, review G3 of #244): a second file under the same ethernets key
// appends to addresses, routes, nameservers.addresses and search of the
// cloud-init file, and keeps match and set-name. It works in a temporary
// root only (netplan get --root-dir): the guest network does not change.
// The base file has the shape that cloud-init writes on a Plumb VM (T-160).
// PLUMB_NETPLAN_MERGE=1 makes a missing netplan a failure (CI).
// PLUMB_NETPLAN_WANT checks the version prefix of netplan.io.
func TestGuestNetplanMerge(t *testing.T) {
	if _, err := exec.LookPath("netplan"); err != nil {
		if os.Getenv("PLUMB_NETPLAN_MERGE") == "1" {
			t.Fatal("netplan is not installed")
		}
		t.Skip("netplan is not installed")
	}
	out, err := exec.Command("dpkg-query", "-W", "-f", "${Version}", "netplan.io").CombinedOutput()
	version := strings.TrimSpace(string(out))
	t.Logf("netplan.io %s (%v)", version, err)
	// PLUMB_NETPLAN_WANT: the version the CI guest must have (Debian: the
	// 1.1.2 of the vendor cloud image, T-160).
	if want := os.Getenv("PLUMB_NETPLAN_WANT"); want != "" && !strings.HasPrefix(version, want) {
		t.Fatalf("netplan.io %q, want %s", version, want)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "etc", "netplan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("50-cloud-init.yaml", `network:
  version: 2
  ethernets:
    eth0:
      match:
        macaddress: "bc:24:11:00:00:01"
      set-name: "eth0"
      addresses:
      - "198.51.100.5/24"
      routes:
      - to: "default"
        via: "198.51.100.1"
      nameservers:
        addresses:
        - 198.51.100.53
        search:
        - base.invalid
`)
	before := netplanGet(t, root)
	write("60-plumb-extra.yaml", `network:
  version: 2
  ethernets:
    eth0:
      addresses:
      - "192.0.2.10/24"
      - "2001:db8::10/64"
      routes:
      - to: "203.0.113.0/24"
        via: "192.0.2.1"
      nameservers:
        addresses:
        - 192.0.2.53
        search:
        - plumb.invalid
`)
	after := netplanGet(t, root)
	t.Logf("before: %+v", before)
	t.Logf("after:  %+v", after)

	appended := func(what string, base, got, ours []string) {
		t.Helper()
		if want := append(slices.Clone(base), ours...); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q (base, then ours): netplan replaced or reordered the list", what, got, want)
		}
	}
	appended("addresses", before.Addresses, after.Addresses, []string{"192.0.2.10/24", "2001:db8::10/64"})
	appended("nameservers.addresses", before.Nameservers.Addresses, after.Nameservers.Addresses, []string{"192.0.2.53"})
	appended("nameservers.search", before.Nameservers.Search, after.Nameservers.Search, []string{"plumb.invalid"})
	var to []string
	for _, r := range after.Routes {
		to = append(to, r.To+" via "+r.Via)
	}
	appended("routes", []string{"default via 198.51.100.1"}, to, []string{"203.0.113.0/24 via 192.0.2.1"})
	if after.SetName != "eth0" || after.Match["macaddress"] != "bc:24:11:00:00:01" {
		t.Errorf("match or set-name changed: %+v", after)
	}
	if len(before.Addresses) != 1 || len(before.Routes) != 1 {
		t.Fatalf("base file not read as written: %+v", before)
	}
}

// guestDefault returns the NIC, gateway and main address of the default
// route of the guest.
func guestDefault(t *testing.T) (dev string, gw, main netip.Addr) {
	t.Helper()
	out, err := exec.Command("ip", "-j", "-4", "route", "show", "default").Output()
	var rs []struct{ Dev, Gateway string }
	if err != nil || json.Unmarshal(out, &rs) != nil || len(rs) == 0 {
		t.Fatalf("no default route: %v %s", err, out)
	}
	gw = netip.MustParseAddr(rs[0].Gateway)
	out, err = exec.Command("ip", "-j", "route", "get", gw.String()).Output()
	var get []struct{ Prefsrc string }
	if err != nil || json.Unmarshal(out, &get) != nil || len(get) != 1 {
		t.Fatalf("route get: %v %s", err, out)
	}
	return rs[0].Dev, gw, netip.MustParseAddr(get[0].Prefsrc)
}

func liveHas(t *testing.T, dev, what string) bool {
	t.Helper()
	var out []byte
	switch {
	case strings.Contains(what, " via "):
		out, _ = exec.Command("ip", "route", "show", "dev", dev).Output()
		return strings.Contains(string(out), what)
	default:
		out, _ = exec.Command("ip", "-o", "addr", "show", "dev", dev).Output()
		return strings.Contains(string(out), " "+what+" ")
	}
}

func netResource(attrs map[string]any) wire.Resource {
	a := map[string]any{"name": "ci"}
	for k, v := range attrs {
		a[k] = v
	}
	return wire.Resource{ID: "network:ci", Type: "network", Block: "net", Attrs: a}
}

// TestGuestNetwork proves the resource network and its safe apply with
// real netplan, systemd-networkd and a real agent (T-150, D-348, D-349):
// apply and confirm, drift, a forced rollback, the rollback timer, the
// duplicate-address probe and state absent. The controller runs in the
// guest (loopback), so the agent link never depends on the change; the
// main NIC is found by the route to the default gateway.
func TestGuestNetwork(t *testing.T) {
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires root in the systemd guest")
	}
	if _, err := exec.LookPath("netplan"); err != nil {
		t.Fatal("requires netplan")
	}
	dev, gw, _ := guestDefault(t)
	file := resource.NetworkFile("ci")
	t.Cleanup(func() {
		_ = exec.Command("systemctl", "stop", resource.RollbackUnit("network")+".timer").Run()
		os.Remove(file)
		_ = exec.Command("netplan", "apply").Run()
	})

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
	a.env.Controller = gw.String() // the real NIC, not the loopback of the test controller
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	apply := func(attrs map[string]any) wire.Item {
		t.Helper()
		id, err := ctl.Send("guest", wire.CommandApply, fwApply(netResource(attrs)))
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.After(90 * time.Second)
		for {
			select {
			case d := <-deliveries:
				if d.Report.Type == wire.ReportResult && d.Report.CommandID == id {
					if d.Report.Error != "" || len(d.Report.Items) != 1 {
						t.Fatalf("apply: %+v", d.Report)
					}
					return d.Report.Items[0]
				}
			case <-deadline:
				t.Fatalf("no result for %s", id)
			}
		}
	}
	observeDiff := func(attrs map[string]any) []wire.Change {
		t.Helper()
		rs, err := resource.New(netResource(attrs), a.env)
		if err != nil {
			t.Fatal(err)
		}
		obs, err := rs.Observe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return rs.Diff(obs)
	}
	want := map[string]any{"addresses": "192.0.2.10/24", "routes": "198.51.100.0/24 via 192.0.2.1",
		"nameservers": "192.0.2.53", "search": "plumb.invalid"}

	// 1. Apply, confirm, commit.
	if it := apply(want); it.Status != wire.StatusApplied {
		t.Fatalf("apply: %+v", it)
	}
	if confirms.Load() == 0 {
		t.Fatal("no confirm round trip")
	}
	if !liveHas(t, dev, "192.0.2.10/24") || !liveHas(t, dev, "198.51.100.0/24 via 192.0.2.1") {
		t.Fatal("the values are not live")
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", fi, err)
	}
	confirmed, _ := os.ReadFile(file)
	if exec.Command("systemctl", "is-active", "--quiet", resource.RollbackUnit("network")+".timer").Run() == nil {
		t.Error("rollback timer still active after the commit")
	}
	if _, err := os.Stat(filepath.Join(state, safeApplyMarker)); !os.IsNotExist(err) {
		t.Error("marker left after the commit")
	}
	if ch := observeDiff(want); len(ch) != 0 {
		t.Fatalf("after the apply: %+v", ch)
	}

	// 2. Drift: the address removed by hand.
	fwSh(t, "ip", "addr", "del", "192.0.2.10/24", "dev", dev)
	if ch := observeDiff(want); len(ch) != 1 || ch[0].Attr != "addresses" {
		t.Fatalf("drift: %+v", ch)
	}
	fwSh(t, "netplan", "apply")
	if ch := observeDiff(want); len(ch) != 0 {
		t.Fatalf("after netplan apply: %+v", ch)
	}

	// 3. A forced rollback: the controller refuses the round trip.
	more := map[string]any{"addresses": "192.0.2.10/24,192.0.2.11/24"}
	refuse.Store(true)
	it := apply(more)
	refuse.Store(false)
	if it.Status != wire.StatusFailed || !it.RolledBack {
		t.Fatalf("forced rollback: %+v", it)
	}
	if got, _ := os.ReadFile(file); string(got) != string(confirmed) || liveHas(t, dev, "192.0.2.11/24") || !liveHas(t, dev, "192.0.2.10/24") {
		t.Fatalf("after the rollback: %s", got)
	}

	// 4. The rollback timer restores when nobody else does.
	old := resource.SetFirewallRollbackAfter(4) // the delay of every rollback timer
	defer resource.SetFirewallRollbackAfter(old)
	rs, err := resource.New(netResource(more), a.env)
	if err != nil {
		t.Fatal(err)
	}
	sa := rs.(resource.SafeApplier)
	if err := sa.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	obs, _ := rs.Observe(ctx)
	if err := rs.Apply(ctx, rs.Diff(obs)); err != nil {
		t.Fatal(err)
	}
	if !liveHas(t, dev, "192.0.2.11/24") {
		t.Fatal("not live in the window")
	}
	deadline := time.Now().Add(30 * time.Second)
	for liveHas(t, dev, "192.0.2.11/24") {
		if time.Now().After(deadline) {
			t.Fatal("the timer did not restore")
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got, _ := os.ReadFile(file); string(got) != string(confirmed) {
		t.Fatalf("file after the timer: %s", got)
	}
	_ = os.Remove(filepath.Join(state, "safe-apply", "network-ci.confirmed")) // the timer does not drop it; the agent start would

	// 5. The duplicate-address probe: the gateway answers for its address.
	taken := map[string]any{"addresses": "192.0.2.10/24," + gw.String() + "/32"}
	if it := apply(taken); it.Status != wire.StatusFailed || it.RolledBack || !strings.Contains(it.Output, "another host") {
		t.Fatalf("probe: %+v", it)
	}
	if got, _ := os.ReadFile(file); string(got) != string(confirmed) {
		t.Fatal("the probe changed the file")
	}

	// 6. state absent.
	if it := apply(map[string]any{"state": "absent"}); it.Status != wire.StatusApplied {
		t.Fatalf("absent: %+v", it)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) || liveHas(t, dev, "192.0.2.10/24") {
		t.Fatal("absent left the file or the address")
	}
}
