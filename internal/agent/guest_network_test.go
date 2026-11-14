//go:build guest

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	// D-358 (review of #261): when netplan generate fails on a file it
	// cannot parse, the files of the earlier run stay, so a failing boot
	// unit does not leave the network without configuration.
	if out, err := exec.Command("netplan", "generate", "--root-dir", root).CombinedOutput(); err != nil {
		t.Fatalf("netplan generate: %v\n%s", err, out)
	}
	generated, _ := filepath.Glob(filepath.Join(root, "run/systemd/network/*.network"))
	write("70-broken.yaml", "network:\n  version: 2\n  ethernets:\n    eth0:\n      dhcp4: [broken\n")
	if err := exec.Command("netplan", "generate", "--root-dir", root).Run(); err == nil {
		t.Fatal("netplan generate took a broken file")
	}
	after2, _ := filepath.Glob(filepath.Join(root, "run/systemd/network/*.network"))
	if len(generated) == 0 || !slices.Equal(generated, after2) {
		t.Fatalf("a failed netplan generate changed the generated files: %v → %v", generated, after2)
	}
	t.Logf("a failed netplan generate keeps the generated files: %v", after2)
}

// guestDefault returns the NIC, gateway and main address of the default
// route of the guest.
func guestDefault(t *testing.T) (dev string, gw, main netip.Addr) {
	t.Helper()
	// A test before may have ended with a netplan apply: on a DHCP NIC the
	// default route comes back only after the lease (CI run 43589617651,
	// Ubuntu VM: TestGuestNetwatch saw no route 0 s after TestGuestNetwork).
	rs, out, err := waitDefaultRoute(60 * time.Second)
	if len(rs) == 0 {
		t.Fatalf("no default route within 60 s: %v %s", err, out)
	}
	gw = netip.MustParseAddr(rs[0].Gateway)
	out, err = exec.Command("ip", "-j", "route", "get", gw.String()).Output()
	var get []struct{ Prefsrc string }
	if err != nil || json.Unmarshal(out, &get) != nil || len(get) != 1 {
		t.Fatalf("route get: %v %s", err, out)
	}
	return rs[0].Dev, gw, netip.MustParseAddr(get[0].Prefsrc)
}

// waitDefaultRoute polls the IPv4 default route for at most d.
func waitDefaultRoute(d time.Duration) (rs []struct{ Dev, Gateway string }, out []byte, err error) {
	for deadline := time.Now().Add(d); ; time.Sleep(500 * time.Millisecond) {
		rs = nil
		out, err = exec.Command("ip", "-j", "-4", "route", "show", "default").Output()
		if err == nil && json.Unmarshal(out, &rs) == nil && len(rs) > 0 {
			return rs, out, nil
		}
		if time.Now().After(deadline) {
			return nil, out, err
		}
	}
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
//
// It runs in the cloud VMs of lxd.sh, which set PLUMB_NETWORK_GUEST=1:
// there a missing netplan or systemd fails the test. Elsewhere (the
// Docker containers of run.sh have no netplan and no NIC of their own to
// change) it skips with the reason.
func TestGuestNetwork(t *testing.T) {
	if os.Getenv("PLUMB_NETWORK_GUEST") != "1" {
		t.Skip("network needs a cloud VM with netplan; only lxd.sh runs it (PLUMB_NETWORK_GUEST=1)")
	}
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires root in the systemd guest")
	}
	if _, err := exec.LookPath("netplan"); err != nil {
		t.Fatal("requires netplan")
	}
	dev, gw, main := guestDefault(t)
	file := resource.NetworkFile("ci")
	t.Cleanup(func() {
		_ = exec.Command("systemctl", "stop", resource.RollbackUnit("network")+".timer").Run()
		_ = exec.Command("systemctl", "disable", resource.NetworkBootUnit).Run()
		os.Remove("/etc/systemd/system/" + resource.NetworkBootUnit)
		_ = exec.Command("systemctl", "daemon-reload").Run()
		os.Remove(file)
		_ = exec.Command("netplan", "apply").Run()
		// The next test needs the default route again (DHCP renews it).
		if rs, _, _ := waitDefaultRoute(60 * time.Second); len(rs) == 0 {
			t.Errorf("no default route 60 s after the cleanup's netplan apply")
		}
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
	meter := meterNetwork(dev, gw, main)
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
	t.Logf("step 1 ok: applied on %s after %d confirm(s), values live, no diff; %s", dev, confirms.Load(), meter.end(gw))

	// 2. Drift: the address removed by hand.
	fwSh(t, "ip", "addr", "del", "192.0.2.10/24", "dev", dev)
	if ch := observeDiff(want); len(ch) != 1 || ch[0].Attr != "addresses" {
		t.Fatalf("drift: %+v", ch)
	}
	fwSh(t, "netplan", "apply")
	if ch := observeDiff(want); len(ch) != 0 {
		t.Fatalf("after netplan apply: %+v", ch)
	}
	t.Log("step 2 ok: a hand-removed address is drift on addresses")

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
	t.Logf("step 3 ok: forced rollback: status %s, rolled_back %v, output %q; file and live addresses as confirmed", it.Status, it.RolledBack, it.Output)

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
	meter = meterNetwork(dev, gw, main) // the timer's netplan apply, without the agent
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
	t.Log("step 4 ok: the rollback timer restored the confirmed file without the agent")
	_ = os.Remove(filepath.Join(state, "safe-apply", "network-ci.confirmed")) // the timer does not drop it; the agent start would

	// 5. The duplicate-address probe: the gateway answers for its address.
	taken := map[string]any{"addresses": "192.0.2.10/24," + gw.String() + "/32"}
	if it = apply(taken); it.Status != wire.StatusFailed || it.RolledBack || !strings.Contains(it.Output, "another host") {
		t.Fatalf("probe: %+v", it)
	}
	if got, _ := os.ReadFile(file); string(got) != string(confirmed) {
		t.Fatal("the probe changed the file")
	}
	// Step 5 started right after the timer's netplan apply: on a DHCP NIC
	// the route can still be missing, and the resource waits for it.
	t.Logf("step 5 ok: duplicate-address probe refused: %q; around steps 4-5: %s", it.Output, meter.end(gw))

	// 5b. The boot unit (n1 of #249): a change that was never confirmed
	// (the agent did not come back after a reboot) is put back before
	// systemd-networkd starts. The unit runs here without a reboot.
	resource.SetFirewallRollbackAfter(600) // the timer must not race the unit
	boot, err := resource.New(netResource(map[string]any{"addresses": "192.0.2.10/24,192.0.2.12/24"}), a.env)
	if err != nil {
		t.Fatal(err)
	}
	if err := boot.(resource.SafeApplier).Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	obs, _ = boot.Observe(ctx)
	if err := boot.Apply(ctx, boot.Diff(obs)); err != nil {
		t.Fatal(err)
	}
	if out, _ := exec.Command("systemctl", "is-enabled", resource.NetworkBootUnit).Output(); strings.TrimSpace(string(out)) != "enabled" {
		t.Fatalf("%s is %q", resource.NetworkBootUnit, out)
	}
	fwSh(t, "systemctl", "start", resource.NetworkBootUnit)
	if got, _ := os.ReadFile(file); string(got) != string(confirmed) {
		t.Fatalf("the boot unit did not put back the confirmed file: %s", got)
	}
	fwSh(t, "netplan", "apply") // at boot systemd-networkd starts after the unit
	if liveHas(t, dev, "192.0.2.12/24") || !liveHas(t, dev, "192.0.2.10/24") {
		t.Fatal("after the boot unit the live addresses are not the confirmed ones")
	}
	if err := boot.(resource.SafeApplier).Restore(ctx); err != nil { // the agent start: drops the copy
		t.Fatal(err)
	}
	fwSh(t, "systemctl", "stop", resource.NetworkBootUnit)
	t.Log("step 5b ok: the boot unit put back the unconfirmed change")

	// 6. state absent.
	if it := apply(map[string]any{"state": "absent"}); it.Status != wire.StatusApplied {
		t.Fatalf("absent: %+v", it)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) || liveHas(t, dev, "192.0.2.10/24") {
		t.Fatal("absent left the file or the address")
	}
	t.Log("step 6 ok: state absent removed the file and the address")

	// 7. T-167 (host F3 of T-158): the entry of the main NIC has another key
	// than the live name and names the NIC only by match.macaddress, like
	// cloud-init's eth0 entry on an Ubuntu VM that kept ens18. network
	// applies without a reboot and never copies the MAC.
	key := macOnlyBase(t, dev, gw)
	if it := apply(want); it.Status != wire.StatusApplied {
		t.Fatalf("MAC-only entry: %+v", it)
	}
	if !liveHas(t, dev, "192.0.2.10/24") || !liveHas(t, dev, "198.51.100.0/24 via 192.0.2.1") {
		t.Fatal("MAC-only entry: the values are not live")
	}
	b, _ := os.ReadFile(file)
	mac, _ := os.ReadFile("/sys/class/net/" + dev + "/address")
	if !strings.Contains(string(b), "    "+key+":\n") || strings.Contains(strings.ToLower(string(b)), strings.TrimSpace(string(mac))) {
		t.Fatalf("MAC-only entry: file %s", b)
	}
	if ch := observeDiff(want); len(ch) != 0 {
		t.Fatalf("MAC-only entry, after the apply: %+v", ch)
	}
	if it := apply(map[string]any{"state": "absent"}); it.Status != wire.StatusApplied {
		t.Fatalf("MAC-only entry, absent: %+v", it)
	}
	t.Logf("step 7 ok: entry %q matched by MAC only, NIC %s: applied without a reboot, no MAC in the file", key, dev)
}

// macOnlyBase rewrites the base netplan of the guest (T-167): the entry of
// dev moves to the key "plumb-mac0" with match.macaddress only (no
// set-name, no match.name), the rest stays. Cleanup puts the old files
// back and runs netplan apply. It returns the new key.
func macOnlyBase(t *testing.T, dev string, gw netip.Addr) string {
	t.Helper()
	const key = "plumb-mac0"
	b, err := os.ReadFile("/sys/class/net/" + dev + "/address")
	if err != nil {
		t.Fatal(err)
	}
	mac := strings.TrimSpace(string(b))
	out, err := exec.Command("netplan", "get").Output()
	if err != nil {
		t.Fatalf("netplan get: %v", err)
	}
	var tree map[string]any
	if err := yaml.Unmarshal(out, &tree); err != nil {
		t.Fatal(err)
	}
	nw, _ := tree["network"].(map[string]any)
	eth, _ := nw["ethernets"].(map[string]any)
	var found []string
	for k, v := range eth {
		e, _ := v.(map[string]any)
		m, _ := e["match"].(map[string]any)
		name, _ := m["name"].(string)
		vmac, _ := m["macaddress"].(string)
		if k == dev || e["set-name"] == dev || name == dev || strings.EqualFold(vmac, mac) {
			found = append(found, k)
		}
	}
	if len(found) != 1 || found[0] == key {
		t.Fatalf("entries of %s: %v", dev, found)
	}
	e := eth[found[0]].(map[string]any)
	delete(e, "set-name")
	e["match"] = map[string]any{"macaddress": mac}
	delete(eth, found[0])
	eth[key] = e

	files, _ := filepath.Glob("/etc/netplan/*.yaml")
	saved := map[string][]byte{}
	for _, f := range files {
		if c, err := os.ReadFile(f); err == nil {
			saved[f] = c
		}
	}
	waitRoute := func() {
		for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
			if exec.Command("ip", "route", "get", gw.String()).Run() == nil {
				return
			}
		}
		t.Errorf("no route to %s after netplan apply", gw)
	}
	t.Cleanup(func() {
		files, _ := filepath.Glob("/etc/netplan/*.yaml")
		for _, f := range files {
			os.Remove(f)
		}
		for f, c := range saved {
			_ = os.WriteFile(f, c, 0o600)
		}
		_ = exec.Command("netplan", "apply").Run()
		waitRoute()
	})
	nb, err := yaml.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	for f := range saved {
		os.Remove(f)
	}
	write(t, "/etc/netplan/50-cloud-init.yaml", string(nb))
	if err := os.Chmod("/etc/netplan/50-cloud-init.yaml", 0o600); err != nil {
		t.Fatal(err)
	}
	fwSh(t, "netplan", "apply")
	waitRoute()
	if _, err := os.Stat("/sys/class/net/" + dev); err != nil {
		t.Fatalf("the NIC %s is gone after the rewrite: %v", dev, err)
	}
	t.Logf("base netplan: entry %q of %s moved to %q with match.macaddress only", found[0], dev, key)
	return key
}

// gapMeter samples every 20 ms whether the route to the gateway and the
// main address exist (VM job of #249: a DHCP NIC loses them for a moment
// after netplan apply). It reports the total and the longest gap.
type gapMeter struct {
	stop chan struct{}
	done chan string
}

func meterNetwork(dev string, gw, main netip.Addr) *gapMeter {
	m := &gapMeter{stop: make(chan struct{}), done: make(chan string, 1)}
	go func() {
		var routeTotal, routeLong, addrTotal, addrLong, routeRun, addrRun time.Duration
		last := time.Now()
		for {
			select {
			case <-m.stop:
				m.done <- fmt.Sprintf("route to the gateway missing %v (longest %v), main address missing %v (longest %v)",
					routeTotal.Round(time.Millisecond), routeLong.Round(time.Millisecond),
					addrTotal.Round(time.Millisecond), addrLong.Round(time.Millisecond))
				return
			case <-time.After(20 * time.Millisecond):
			}
			now := time.Now()
			d := now.Sub(last)
			last = now
			if exec.Command("ip", "route", "get", gw.String()).Run() != nil {
				routeTotal, routeRun = routeTotal+d, routeRun+d
				routeLong = max(routeLong, routeRun)
			} else {
				routeRun = 0
			}
			out, _ := exec.Command("ip", "-o", "-4", "addr", "show", "dev", dev).Output()
			if !strings.Contains(string(out), " "+main.String()+"/") {
				addrTotal, addrRun = addrTotal+d, addrRun+d
				addrLong = max(addrLong, addrRun)
			} else {
				addrRun = 0
			}
		}
	}()
	return m
}

// end waits until the route is back (at most 30 s), 3 s more for a late
// drop, and returns the report.
func (m *gapMeter) end(gw netip.Addr) string {
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if exec.Command("ip", "route", "get", gw.String()).Run() == nil {
			break
		}
	}
	time.Sleep(3 * time.Second)
	close(m.stop)
	return <-m.done
}
