package resource

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
	"go.yaml.in/yaml/v3"
)

// fakeNet is the guest of the network tests: ip, resolvectl and netplan
// apply are simulated from the files under root; netplan get and generate
// run the real netplan (the merge rule is netplan's, T-150 proves it).
type fakeNet struct {
	mu      sync.Mutex
	root    string
	main    string // main address, on dev
	dev     string // live name of the main NIC (default eth0)
	mac     string // its MAC (default the MAC of cloudInit)
	calls   [][]string
	applies int
	failGet bool
	// noRoute: the next ip route get calls fail like a NIC without its
	// DHCP address (-1: all of them).
	noRoute int
	failArm bool // systemd-run fails
}

func (f *fakeNet) live() (addrs []string, routes []liveRoute, dns, search []string) {
	addrs = []string{f.main}
	files, _ := filepath.Glob(filepath.Join(f.root, "etc/netplan/60-plumb-*.yaml"))
	for _, p := range files {
		b, _ := os.ReadFile(p)
		for _, a := range fileAddrs(string(b)) {
			addrs = append(addrs, a.Addr().String())
		}
		var nf npFile
		_ = yamlUnmarshal(b, &nf)
		for _, e := range nf.Network.Ethernets {
			for _, r := range e.Routes {
				routes = append(routes, liveRoute{Dst: r.To, Gateway: r.Via, Metric: r.Metric})
			}
			if e.Nameservers != nil {
				dns = append(dns, e.Nameservers.Addresses...)
				search = append(search, e.Nameservers.Search...)
			}
		}
	}
	return
}

func (f *fakeNet) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.mu.Unlock()
	addrs, routes, dns, search := f.live()
	switch {
	case name == "ip" && slices.Equal(args[:3], []string{"-j", "route", "get"}):
		f.mu.Lock()
		down := f.noRoute != 0
		if f.noRoute > 0 {
			f.noRoute--
		}
		f.mu.Unlock()
		if down {
			return nil, &ExitError{Name: "ip", Code: 2, Stderr: "RTNETLINK answers: Network is unreachable"}
		}
		return json.Marshal([]map[string]string{{"dev": f.dev, "prefsrc": f.main}})
	case name == "ip" && slices.Equal(args[:3], []string{"-j", "link", "show"}):
		if args[len(args)-1] != f.dev {
			return nil, &ExitError{Name: "ip", Code: 1, Stderr: "Device does not exist"}
		}
		return json.Marshal([]map[string]string{{"ifname": f.dev, "address": f.mac}})
	case name == "ip" && slices.Equal(args[:2], []string{"-j", "addr"}):
		var info []map[string]string
		for _, a := range addrs {
			info = append(info, map[string]string{"local": a})
		}
		return json.Marshal([]map[string]any{{"addr_info": info}})
	case name == "ip" && args[2] == "route":
		var out []liveRoute
		for _, r := range routes {
			if strings.Contains(r.Dst, ":") == (args[1] == "-6") {
				out = append(out, r)
			}
		}
		if out == nil {
			out = []liveRoute{}
		}
		return json.Marshal(out)
	case name == "resolvectl" && args[0] == "dns":
		return []byte("Link 2 (eth0): 198.51.100.53 " + strings.Join(dns, " ") + "\n"), nil
	case name == "resolvectl" && args[0] == "domain":
		return []byte("Link 2 (eth0): " + strings.Join(search, " ") + "\n"), nil
	case name == "netplan" && args[0] == "apply":
		f.mu.Lock()
		f.applies++
		f.mu.Unlock()
		return nil, nil
	case name == "netplan" && (args[0] == "get" || args[0] == "generate"):
		if f.failGet {
			return nil, &ExitError{Name: name, Code: 1, Stderr: "bad"}
		}
		return exec.CommandContext(ctx, "netplan", args...).Output()
	case name == "systemd-run" && f.failArm:
		return nil, &ExitError{Name: name, Code: 1, Stderr: "Failed to start transient timer unit"}
	case name == "systemctl" || name == "systemd-run":
		return nil, nil
	}
	return nil, &ExitError{Name: name, Code: 2, Stderr: "unexpected call"}
}

func (f *fakeNet) count(words ...string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if len(c) >= len(words) && slices.Equal(c[:len(words)], words) {
			n++
		}
	}
	return n
}

const cloudInit = `network:
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
`

func newNetEnv(t *testing.T) (Env, *fakeNet, *[]netip.Addr) {
	t.Helper()
	if _, err := exec.LookPath("netplan"); err != nil {
		t.Skip("netplan is not installed")
	}
	root := guestRoot(t)
	for _, d := range []string{"etc/netplan", "etc/systemd/system"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc/netplan/50-cloud-init.yaml"), []byte(cloudInit), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeNet{root: root, main: "198.51.100.5", dev: "eth0", mac: "bc:24:11:00:00:01"}
	env := NewEnv(root, f)
	env.StateDir = t.TempDir()
	env.Controller = "203.0.113.1"
	probed := &[]netip.Addr{}
	old := networkProbe
	networkProbe = func(_ context.Context, dev string, addrs []netip.Addr) error {
		*probed = append(*probed, addrs...)
		return nil
	}
	t.Cleanup(func() { networkProbe = old })
	return env, f, probed
}

func netRes(t *testing.T, env Env, attrs map[string]any) *network {
	t.Helper()
	attrs["name"] = "extra"
	r, err := New(wire.Resource{ID: "network:extra", Type: "network", Attrs: attrs}, env)
	if err != nil {
		t.Fatal(err)
	}
	return r.(*network)
}

func diffOf(t *testing.T, n *network) []wire.Change {
	t.Helper()
	st, err := n.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n.Diff(st)
}

var extra = map[string]any{"addresses": "192.0.2.10/24,2001:db8::10/64", "routes": "203.0.113.0/24 via 192.0.2.1",
	"nameservers": "192.0.2.53", "search": "plumb.invalid"}

func cloneAttrs(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// The full safe-apply path of a new network file: Prepare (probe,
// confirmed "absent", timer), Apply (file 0600 under eth0, netplan apply),
// Commit; then no drift.
func TestNetworkApplyCommit(t *testing.T) {
	ctx := context.Background()
	env, f, probed := newNetEnv(t)
	n := netRes(t, env, cloneAttrs(extra))
	ch := diffOf(t, n)
	if len(ch) != 5 || ch[0].Attr != wire.AttrState {
		t.Fatalf("create diff: %+v", ch)
	}
	if err := n.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*probed, []netip.Addr{netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("2001:db8::10")}) {
		t.Errorf("probed %v", *probed)
	}
	c, err := os.ReadFile(networkConfirmed(env, "extra"))
	if err != nil || len(c) != 0 {
		t.Fatalf("confirmed copy of absent: %q %v", c, err)
	}
	if f.count("systemd-run", "--unit="+RollbackUnit("network")) != 1 || f.applies != 0 {
		t.Fatalf("prepare changed something live or armed no timer: %v", f.calls)
	}
	if err := n.Apply(ctx, ch); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(env.Root, NetworkFile("extra")))
	if err != nil || !strings.Contains(string(b), "    eth0:\n") || strings.Contains(string(b), "macaddress") {
		t.Fatalf("file: %s %v", b, err)
	}
	if fi, _ := os.Stat(filepath.Join(env.Root, NetworkFile("extra"))); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	if f.applies != 1 {
		t.Errorf("netplan apply %d times", f.applies)
	}
	if err := n.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(networkConfirmed(env, "extra")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the confirmed copy stays after Commit")
	}
	if ch := diffOf(t, n); len(ch) != 0 {
		t.Fatalf("after the apply: %+v", ch)
	}
}

// No round trip: Restore removes the new file (the confirmed state was
// "absent") and runs netplan apply; the agent start does the same from
// the copy alone.
func TestNetworkRestore(t *testing.T) {
	ctx := context.Background()
	env, f, _ := newNetEnv(t)
	n := netRes(t, env, cloneAttrs(extra))
	if err := n.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := n.Apply(ctx, diffOf(t, n)); err != nil {
		t.Fatal(err)
	}
	if err := n.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.Root, NetworkFile("extra"))); !errors.Is(err, os.ErrNotExist) {
		t.Error("the new file stays after Restore")
	}
	if f.applies != 2 || f.count("systemctl", "stop", RollbackUnit("network")+".timer") == 0 {
		t.Errorf("applies %d, calls %v", f.applies, f.calls)
	}
	// A change of an existing file, then the agent restarts in the window.
	if err := n.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := n.Apply(ctx, diffOf(t, n)); err != nil {
		t.Fatal(err)
	}
	if err := n.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(filepath.Join(env.Root, NetworkFile("extra")))
	changed := cloneAttrs(extra)
	changed["addresses"] = "192.0.2.11/24"
	m := netRes(t, env, changed)
	if err := m.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, diffOf(t, m)); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, env, "network"); err != nil {
		t.Fatal(err)
	}
	if now, _ := os.ReadFile(filepath.Join(env.Root, NetworkFile("extra"))); string(now) != string(old) {
		t.Errorf("restore at start: %s, want %s", now, old)
	}
	script, err := networkRollbackScript(env, "extra")
	if err != nil || !strings.Contains(script, "install -m 600") || !strings.HasSuffix(script, "netplan apply") {
		t.Errorf("rollback script: %q %v", script, err)
	}
}

// The preflight and the probe refuse with nothing changed (D-348).
func TestNetworkRefusals(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		attrs map[string]any
		setup func(env Env, f *fakeNet)
		want  string
	}{
		{"main address", map[string]any{"addresses": "198.51.100.5/25"}, nil, "is the main address of the Target"},
		{"no entry", cloneAttrs(extra), func(env Env, f *fakeNet) { f.main = "198.51.100.99" }, "0 entries for the main NIC"},
		{"two entries", cloneAttrs(extra), func(env Env, f *fakeNet) {
			_ = os.WriteFile(filepath.Join(env.Root, "etc/netplan/70-other.yaml"),
				[]byte("network:\n  version: 2\n  ethernets:\n    eth1:\n      addresses: [\"198.51.100.5/24\"]\n"), 0o600)
		}, "2 entries for the main NIC"},
		{"probe", cloneAttrs(extra), func(env Env, f *fakeNet) {
			networkProbe = func(context.Context, string, []netip.Addr) error {
				return errors.New("new address 1: " + errAddrInUse.Error())
			}
		}, "another host on the link uses the address"},
		{"netplan get fails", cloneAttrs(extra), func(env Env, f *fakeNet) { f.failGet = true }, "netplan get"},
		{"no state dir", cloneAttrs(extra), nil, "state dir"},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, f, _ := newNetEnv(t)
			if c.name == "no state dir" {
				env.StateDir = ""
			}
			if c.setup != nil {
				c.setup(env, f)
			}
			n := netRes(t, env, c.attrs)
			err := n.Prepare(ctx)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Prepare: %v, want %q", err, c.want)
			}
			if f.applies != 0 || f.count("systemd-run") != 0 {
				t.Errorf("something changed: %v", f.calls)
			}
			if _, err := os.Stat(filepath.Join(env.Root, NetworkFile("extra"))); !errors.Is(err, os.ErrNotExist) {
				t.Error("a file was written")
			}
		})
	}
}

// Drift: a listed address gone live, or the file edited by hand.
func TestNetworkDrift(t *testing.T) {
	ctx := context.Background()
	env, f, _ := newNetEnv(t)
	n := netRes(t, env, cloneAttrs(extra))
	if err := n.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := n.Apply(ctx, diffOf(t, n)); err != nil {
		t.Fatal(err)
	}
	_ = n.Commit(ctx)
	p := filepath.Join(env.Root, NetworkFile("extra"))
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), "192.0.2.10/24", "192.0.2.99/24", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	ch := diffOf(t, n)
	attrs := []string{}
	for _, c := range ch {
		attrs = append(attrs, c.Attr)
		if c.Risk != wire.RiskDisruptive {
			t.Errorf("risk %s", c.Risk)
		}
	}
	if !slices.Equal(attrs, []string{"addresses", networkAttrFile}) {
		t.Fatalf("drift: %+v", ch)
	}
	_ = f
	// state absent removes the file; the confirmed copy holds it.
	gone := netRes(t, env, map[string]any{"state": "absent"})
	if ch := diffOf(t, gone); len(ch) != 1 || ch[0].To != wire.StateAbsent {
		t.Fatalf("absent diff: %+v", ch)
	}
	if err := gone.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if c, _ := os.ReadFile(networkConfirmed(env, "extra")); !strings.Contains(string(c), "192.0.2.99/24") {
		t.Errorf("confirmed copy: %s", c)
	}
	if err := gone.Apply(ctx, diffOf(t, gone)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Error("absent kept the file")
	}
}

func yamlUnmarshal(b []byte, v any) error { return yaml.Unmarshal(b, v) }

// VM job of #249: right after a netplan apply a DHCP NIC can have no
// route for a moment. The step waits for it (bounded) instead of failing.
func TestNetworkWaitsForRoute(t *testing.T) {
	ctx := context.Background()
	oldWait, oldRetry := routeWait, routeRetry
	routeWait, routeRetry = 2*time.Second, 10*time.Millisecond
	t.Cleanup(func() { routeWait, routeRetry = oldWait, oldRetry })
	env, f, _ := newNetEnv(t)
	f.noRoute = 3
	n := netRes(t, env, cloneAttrs(extra))
	if err := n.Prepare(ctx); err != nil {
		t.Fatalf("Prepare after a short route gap: %v", err)
	}
	if got := f.count("ip", "-j", "route", "get"); got < 4 {
		t.Errorf("route get %d times, want the 3 failures and a success", got)
	}
	if err := n.Apply(ctx, diffOf(t, n)); err != nil { // the file exists: Observe asks the route
		t.Fatal(err)
	}
	// The route never comes: the step fails after the bound, nothing changed.
	env2, f2, _ := newNetEnv(t)
	f2.noRoute = -1
	routeWait = 100 * time.Millisecond
	start := time.Now()
	err := netRes(t, env2, cloneAttrs(extra)).Prepare(ctx)
	if err == nil || !strings.Contains(err.Error(), "Network is unreachable") || time.Since(start) > time.Second {
		t.Fatalf("no route: %v after %v", err, time.Since(start))
	}
	if f2.count("systemd-run") != 0 {
		t.Error("something was armed without a route")
	}
	// Observe (the drift loop) waits only a short bound: a gap of a DHCP
	// NIC is no error (old run of #261), a lost route fails fast.
	oldObs := observeRouteWait
	t.Cleanup(func() { observeRouteWait = oldObs })
	observeRouteWait = 2 * time.Second
	f.noRoute = 2
	if _, err := netRes(t, env, cloneAttrs(extra)).Observe(ctx); err != nil {
		t.Fatalf("Observe after a short route gap: %v", err)
	}
	observeRouteWait = 100 * time.Millisecond
	f.noRoute = -1
	start = time.Now()
	if _, err := netRes(t, env, cloneAttrs(extra)).Observe(ctx); err == nil || time.Since(start) > time.Second {
		t.Fatalf("Observe without a route: %v after %v", err, time.Since(start))
	}
}

// n4 of #249: when the rollback timer cannot be armed, the confirmed copy
// is removed again: nothing half-armed stays for a later restore.
func TestNetworkArmFailureLeavesNoCopy(t *testing.T) {
	env, f, _ := newNetEnv(t)
	f.failArm = true
	err := netRes(t, env, cloneAttrs(extra)).Prepare(context.Background())
	if err == nil || !strings.Contains(err.Error(), "arm the rollback timer") {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := os.Stat(networkConfirmed(env, "extra")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the confirmed copy stays after a failed arm")
	}
	if f.applies != 0 {
		t.Error("netplan apply ran")
	}
}

// n1 of #249: Prepare installs and enables the boot unit that puts back
// an unconfirmed change before systemd-networkd starts.
func TestNetworkBootUnit(t *testing.T) {
	env, f, _ := newNetEnv(t)
	if err := netRes(t, env, cloneAttrs(extra)).Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(env.Root, unitDir+NetworkBootUnit))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(b)
	dir := filepath.Join(env.StateDir, networkConfirmedDir)
	for _, want := range []string{
		"Before=systemd-networkd.service",
		"ConditionPathExistsGlob=" + dir + "/network-*.confirmed",
		`install -m 600 "$$c" "$$f"`,
		"; netplan generate'",
		"WantedBy=sysinit.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	if f.count("systemctl", "enable", NetworkBootUnit) != 1 {
		t.Errorf("not enabled: %v", f.calls)
	}
	// A second Prepare leaves the same unit alone.
	if err := netRes(t, env, cloneAttrs(extra)).Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.count("systemctl", "enable", NetworkBootUnit) != 1 {
		t.Error("the unit was written again")
	}
	// A state dir that a unit cannot hold is refused before anything.
	env.StateDir = "/var/lib/plumb agent"
	if _, err := networkBootUnitContent(env); err == nil {
		t.Error("a state dir with a space was taken")
	}
	// The script of the unit, run by sh with "$$" as systemd passes it:
	// it puts back the copy (or removes the file) for each name.
	script := unit[strings.Index(unit, "ExecStart=/bin/sh -c '")+len("ExecStart=/bin/sh -c '"):]
	script = strings.ReplaceAll(script[:strings.Index(script, "'")], "$$", "$")
	if !strings.Contains(script, "for c in "+dir+"/network-*.confirmed") {
		t.Fatalf("script: %s", script)
	}
}

func TestMainKey(t *testing.T) {
	main := netip.MustParseAddr("198.51.100.5")
	const mac = "bc:24:11:00:00:01"
	dhcp := func(key, setName, matchName, matchMAC string) map[string]npView {
		v := npView{DHCP4: true, SetName: setName, Match: map[string]any{}}
		if matchName != "" {
			v.Match["name"] = matchName
		}
		if matchMAC != "" {
			v.Match["macaddress"] = matchMAC
		}
		return map[string]npView{key: v}
	}
	for _, c := range []struct {
		name string
		view map[string]npView
		dev  string
		want string // key, or the error text
	}{
		{"static by address", map[string]npView{"lan": {Addresses: []any{"198.51.100.5/24"}}}, "ens18", "lan"},
		{"key", dhcp("ens18", "", "", ""), "ens18", "ens18"},
		{"set-name", dhcp("lan", "ens18", "", ""), "ens18", "lan"},
		{"match.name", dhcp("lan", "", "ens18", ""), "ens18", "lan"},
		// D-192 / T-158 F3: cloud-init named it eth0, the first boot kept ens18.
		{"match.macaddress, other live name", dhcp("eth0", "eth0", "", mac), "ens18", "eth0"},
		{"match.macaddress, upper case", dhcp("eth0", "eth0", "", "BC:24:11:00:00:01"), "ens18", "eth0"},
		{"other MAC", dhcp("eth0", "eth0", "", "bc:24:11:00:00:02"), "ens18", "0 entries for the main NIC ens18"},
		{"none", map[string]npView{}, "ens18", "0 entries for the main NIC ens18"},
		{"two: by name and by MAC", func() map[string]npView {
			v := dhcp("ens18", "", "", "")
			maps.Copy(v, dhcp("eth0", "eth0", "", mac))
			return v
		}(), "ens18", "2 entries for the main NIC ens18"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := mainKey(c.view, c.dev, mac, main)
			if err != nil {
				got = err.Error()
			}
			if !strings.Contains(got, c.want) || (err == nil && got != c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
	// An empty MAC (never expected: Prepare reads it) matches no entry.
	if _, err := mainKey(dhcp("eth0", "eth0", "", ""), "ens18", "", main); err == nil {
		t.Fatal("an empty MAC matched an entry without a MAC")
	}
}

// T-158 F3: a DHCP VM whose NIC kept the kernel name ens18 while
// cloud-init's entry is eth0 with match.macaddress and set-name. The full
// safe apply works without a reboot; the Plumb file names the entry key
// and never copies the MAC (D-348).
func TestNetworkMainNICByMAC(t *testing.T) {
	ctx := context.Background()
	env, f, _ := newNetEnv(t)
	f.dev = "ens18"
	ci := "network:\n  version: 2\n  ethernets:\n    eth0:\n      match:\n        macaddress: \"BC:24:11:00:00:01\"\n      set-name: \"eth0\"\n      dhcp4: true\n"
	if err := os.WriteFile(filepath.Join(env.Root, "etc/netplan/50-cloud-init.yaml"), []byte(ci), 0o600); err != nil {
		t.Fatal(err)
	}
	n := netRes(t, env, cloneAttrs(extra))
	ch := diffOf(t, n)
	if err := n.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count("ip", "-j", "link", "show", "dev", "ens18") != 1 {
		t.Errorf("the MAC was not read from the live link: %v", f.calls)
	}
	if err := n.Apply(ctx, ch); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(env.Root, NetworkFile("extra")))
	if err != nil || !strings.Contains(string(b), "    eth0:\n") || strings.Contains(strings.ToLower(string(b)), "bc:24:11") {
		t.Fatalf("file: %s %v", b, err)
	}
	if err := n.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// The same guest with another MAC on the link: no entry, a clear refusal.
	env2, f2, _ := newNetEnv(t)
	f2.dev, f2.mac = "ens18", "bc:24:11:00:00:09"
	if err := os.WriteFile(filepath.Join(env2.Root, "etc/netplan/50-cloud-init.yaml"), []byte(ci), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := netRes(t, env2, cloneAttrs(extra)).Prepare(ctx); err == nil || !strings.Contains(err.Error(), "0 entries for the main NIC ens18") {
		t.Fatalf("other MAC: %v", err)
	}
}

// T-208 (host finding F4, D-192): a static Ubuntu VM whose first boot
// kept ens18 with the initramfs DHCP address; cloud-init's entry (eth0,
// MAC match, set-name, static address) is not active yet. The refusal
// says in plain words to restart the VM once; nothing is changed.
func TestNetworkFirstBootStaticRefusal(t *testing.T) {
	ctx := context.Background()
	env, f, _ := newNetEnv(t)
	f.dev = "ens18"
	f.main = "198.51.100.99" // the DHCP address of the first boot
	ci := "network:\n  version: 2\n  ethernets:\n    eth0:\n      match:\n        macaddress: \"BC:24:11:00:00:01\"\n      set-name: \"eth0\"\n" +
		"      addresses: [\"198.51.100.5/24\"]\n      routes:\n      - to: default\n        via: 198.51.100.1\n"
	if err := os.WriteFile(filepath.Join(env.Root, "etc/netplan/50-cloud-init.yaml"), []byte(ci), 0o600); err != nil {
		t.Fatal(err)
	}
	err := netRes(t, env, cloneAttrs(extra)).Prepare(ctx)
	if err == nil || !strings.Contains(err.Error(), "Restart the VM once") || strings.Contains(err.Error(), "netplan") {
		t.Fatalf("refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.Root, NetworkFile("extra"))); !os.IsNotExist(err) {
		t.Fatal("a file was written")
	}
	// Another MAC: not this NIC, the old text stays.
	f.mac = "bc:24:11:00:00:09"
	if err := netRes(t, env, cloneAttrs(extra)).Prepare(ctx); err == nil || !strings.Contains(err.Error(), "0 entries for the main NIC ens18") {
		t.Fatalf("other MAC: %v", err)
	}
}
