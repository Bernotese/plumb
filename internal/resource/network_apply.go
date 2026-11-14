package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
	"go.yaml.in/yaml/v3"
)

// The network resource on the guest (D-332, D-348, D-349; ARCHITECTURE
// §17): one netplan file /etc/netplan/60-plumb-<name>.yaml (0600) under
// the key of the main NIC, applied with netplan apply inside the safe
// apply. Values: network.go; duplicate-address probe: network_probe.go.

const (
	netplanEtc = "/etc/netplan"
	// networkConfirmedDir is below the agent state dir: the confirmed
	// file of a safe apply (D-349).
	networkConfirmedDir = "safe-apply"
	// networkFileOK and networkFileChanged are the values of the observed
	// attribute "file".
	networkFileOK      = "as Plumb wrote it"
	networkFileChanged = "changed outside Plumb"
	networkAttrFile    = "file"
)

var networkNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// networkProbe is the duplicate-address probe; the unit tests replace it.
var networkProbe = probeDuplicates

// NetworkFile is the netplan file of the network resource name.
func NetworkFile(name string) string { return netplanEtc + "/60-plumb-" + name + ".yaml" }

func networkConfirmed(env Env, name string) string {
	return filepath.Join(env.StateDir, networkConfirmedDir, "network-"+name+".confirmed")
}

type networkAttrs struct {
	Name        string `json:"name"`
	Addresses   string `json:"addresses"`
	Routes      string `json:"routes"`
	Nameservers string `json:"nameservers"`
	Search      string `json:"search"`
	State       string `json:"state"`
}

// network is one network resource of a VM.
type network struct {
	base
	a    networkAttrs
	spec NetworkSpec
	// content is the file that Prepare made for Apply.
	content string
}

func newNetwork(r wire.Resource, env Env) (Resource, error) {
	n := &network{base: base{res: r, env: env}}
	n.a.State = wire.StatePresent
	if err := decode(r, "network", &n.a, func() string { return n.a.Name }); err != nil {
		return nil, err
	}
	if !networkNameRE.MatchString(n.a.Name) {
		return nil, fmt.Errorf("resource %s: bad name", r.ID)
	}
	if n.a.State != wire.StatePresent && n.a.State != wire.StateAbsent {
		return nil, fmt.Errorf("resource %s: bad state", r.ID)
	}
	spec, err := CheckNetworkAttrs(r.Attrs)
	if err != nil {
		return nil, fmt.Errorf("resource %s: %w", r.ID, err)
	}
	n.spec = spec
	return n, nil
}

// --- canonical text of the values (the form of Diff) ---

func (r NetworkRoute) String() string {
	s := r.To.String() + " via " + r.Via.String()
	if r.Metric > 0 {
		s += " metric " + strconv.Itoa(r.Metric)
	}
	return s
}

func joinStrings[T fmt.Stringer](list []T, sep string) string {
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = v.String()
	}
	return strings.Join(out, sep)
}

// attrs returns the desired values in the form that Observe reports.
func (s NetworkSpec) attrs() map[string]string {
	return map[string]string{
		"addresses":   joinStrings(s.Addresses, ","),
		"routes":      joinStrings(s.Routes, ";"),
		"nameservers": joinStrings(s.Nameservers, ","),
		"search":      strings.Join(s.Search, ","),
	}
}

var networkAttrOrder = []string{"addresses", "routes", "nameservers", "search", networkAttrFile}

// --- the netplan file ---

type npRoute struct {
	To     string `yaml:"to"`
	Via    string `yaml:"via"`
	Metric int    `yaml:"metric,omitempty"`
}

type npNameservers struct {
	Addresses []string `yaml:"addresses,omitempty"`
	Search    []string `yaml:"search,omitempty"`
}

type npEntry struct {
	Addresses   []string       `yaml:"addresses,omitempty"`
	Routes      []npRoute      `yaml:"routes,omitempty"`
	Nameservers *npNameservers `yaml:"nameservers,omitempty"`
}

type npFile struct {
	Network struct {
		Version   int                `yaml:"version"`
		Ethernets map[string]npEntry `yaml:"ethernets"`
	} `yaml:"network"`
}

// fileContent is the netplan file of spec under the NIC key.
func (n *network) fileContent(key string) (string, error) {
	var e npEntry
	for _, a := range n.spec.Addresses {
		e.Addresses = append(e.Addresses, a.String())
	}
	for _, r := range n.spec.Routes {
		e.Routes = append(e.Routes, npRoute{To: r.To.String(), Via: r.Via.String(), Metric: r.Metric})
	}
	if len(n.spec.Nameservers)+len(n.spec.Search) > 0 {
		e.Nameservers = &npNameservers{Search: n.spec.Search}
		for _, a := range n.spec.Nameservers {
			e.Nameservers.Addresses = append(e.Nameservers.Addresses, a.String())
		}
	}
	var f npFile
	f.Network.Version = 2
	f.Network.Ethernets = map[string]npEntry{key: e}
	var buf bytes.Buffer
	buf.WriteString("# Managed by the Plumb agent (" + n.res.ID + "). Do not edit.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// fileKey returns the single ethernets key of a file of ours; ok is false
// when the file is not of that form.
func fileKey(content string) (string, bool) {
	var f npFile
	if err := yaml.Unmarshal([]byte(content), &f); err != nil || len(f.Network.Ethernets) != 1 {
		return "", false
	}
	for k := range f.Network.Ethernets {
		return k, true
	}
	return "", false
}

// fileAddrs returns the addresses of a file of ours ("" or not ours: none).
func fileAddrs(content string) []netip.Prefix {
	var f npFile
	if yaml.Unmarshal([]byte(content), &f) != nil {
		return nil
	}
	var out []netip.Prefix
	for _, e := range f.Network.Ethernets {
		for _, a := range e.Addresses {
			if p, err := netip.ParsePrefix(a); err == nil {
				out = append(out, p)
			}
		}
	}
	return out
}

// --- the main NIC (D-348) ---

// mainLink returns the NIC and the source address of the route to the
// controller: the path that the agent link uses.
//
// wait bounds the wait for the route: Prepare waits routeWait; Observe
// only observeRouteWait, so the drift loop never stalls long, but a gap of
// a DHCP NIC after a netplan apply (about 25 ms in CI) is no error (T-164).
func (n *network) mainLink(ctx context.Context, wait time.Duration) (dev string, addr netip.Addr, err error) {
	host := n.env.Controller
	if host == "" {
		return "", addr, errors.New("the agent does not know the controller address")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		ips, lerr := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if lerr != nil || len(ips) == 0 {
			return "", addr, fmt.Errorf("resolve the controller: %v", lerr)
		}
		ip = ips[0].Unmap()
	}
	out, err := n.routeGet(ctx, ip, wait)
	if err != nil {
		return "", addr, fmt.Errorf("route to the controller: %w", err)
	}
	var routes []struct {
		Dev     string `json:"dev"`
		Prefsrc string `json:"prefsrc"`
	}
	if err := json.Unmarshal(out, &routes); err != nil || len(routes) != 1 || routes[0].Dev == "" {
		return "", addr, errors.New("route to the controller: no single route")
	}
	if addr, err = netip.ParseAddr(routes[0].Prefsrc); err != nil {
		return "", addr, errors.New("route to the controller: no source address")
	}
	return routes[0].Dev, addr, nil
}

// Wait for the route to the controller (T-150, VM job of #249): right
// after a netplan apply (also the rollback timer's) a DHCP NIC can lose its
// address and routes for a moment, and "ip route get" then fails with
// "Network is unreachable". The step waits for the route, bounded, instead
// of failing at once.
var (
	routeWait        = 30 * time.Second
	observeRouteWait = 3 * time.Second
	routeRetry       = 500 * time.Millisecond
)

// routeGet runs ip route get to ip, again every routeRetry until it
// answers or wait (or ctx) ends.
func (n *network) routeGet(ctx context.Context, ip netip.Addr, wait time.Duration) ([]byte, error) {
	end := time.Now().Add(wait)
	for {
		out, err := n.run(ctx, "ip", "-j", "route", "get", ip.String())
		if err == nil || time.Now().Add(routeRetry).After(end) {
			return out, err
		}
		t := time.NewTimer(routeRetry)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, err
		case <-t.C:
		}
	}
}

// npView is an ethernets entry of netplan get, as far as the checks read it.
type npView struct {
	Match       map[string]any `yaml:"match"`
	SetName     string         `yaml:"set-name"`
	DHCP4       bool           `yaml:"dhcp4"`
	DHCP6       bool           `yaml:"dhcp6"`
	Addresses   []any          `yaml:"addresses"`
	Gateway4    string         `yaml:"gateway4"`
	Gateway6    string         `yaml:"gateway6"`
	Routes      []npRoute      `yaml:"routes"`
	Nameservers npNameservers  `yaml:"nameservers"`
}

// addrs returns the addresses of the entry (an address can be a string
// or a mapping with options in netplan).
func (v npView) addrs() []netip.Prefix {
	var out []netip.Prefix
	for _, a := range v.Addresses {
		s, ok := a.(string)
		if !ok {
			if m, isMap := a.(map[string]any); isMap {
				for k := range m {
					s = k
				}
			}
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// defaults returns the default routes and gateways of the entry, sorted.
func (v npView) defaults() []string {
	var out []string
	for _, g := range []string{v.Gateway4, v.Gateway6} {
		if g != "" {
			out = append(out, "gw "+g)
		}
	}
	for _, r := range v.Routes {
		if r.To == "default" || r.To == "0.0.0.0/0" || r.To == "::/0" {
			out = append(out, r.To+" via "+r.Via+" metric "+strconv.Itoa(r.Metric))
		}
	}
	slices.Sort(out)
	return out
}

// netplanView runs netplan get on a root and returns the ethernets entries.
func (n *network) netplanView(ctx context.Context, root string) (map[string]npView, error) {
	out, err := n.run(ctx, "netplan", "get", "--root-dir", root, "ethernets")
	if err != nil {
		return nil, fmt.Errorf("netplan get: %w", err)
	}
	view := map[string]npView{}
	if err := yaml.Unmarshal(out, &view); err != nil {
		return nil, fmt.Errorf("netplan get: %w", err)
	}
	return view, nil
}

// mainKey returns the key of the single ethernets entry that holds the
// main address, or for a DHCP NIC the entry of that NIC (D-348).
func mainKey(view map[string]npView, dev string, main netip.Addr) (string, error) {
	var keys []string
	for k, v := range view {
		has := slices.ContainsFunc(v.addrs(), func(p netip.Prefix) bool { return p.Addr() == main })
		name, _ := v.Match["name"].(string)
		dhcp := (v.DHCP4 || v.DHCP6) && (k == dev || v.SetName == dev || name == dev)
		if has || dhcp {
			keys = append(keys, k)
		}
	}
	if len(keys) != 1 {
		return "", fmt.Errorf("netplan has %d entries for the main NIC %s, want exactly one", len(keys), dev)
	}
	return keys[0], nil
}

// --- Observe and Diff ---

// liveAddrs returns the addresses on dev.
func (n *network) liveAddrs(ctx context.Context, dev string) ([]netip.Addr, error) {
	out, err := n.run(ctx, "ip", "-j", "addr", "show", "dev", dev)
	if err != nil {
		return nil, fmt.Errorf("ip addr: %w", err)
	}
	var links []struct {
		AddrInfo []struct {
			Local string `json:"local"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal(out, &links); err != nil {
		return nil, fmt.Errorf("ip addr: %w", err)
	}
	var out2 []netip.Addr
	for _, l := range links {
		for _, a := range l.AddrInfo {
			if ip, err := netip.ParseAddr(a.Local); err == nil {
				out2 = append(out2, ip)
			}
		}
	}
	return out2, nil
}

type liveRoute struct {
	Dst     string `json:"dst"`
	Gateway string `json:"gateway"`
	Metric  int    `json:"metric"`
}

func (n *network) liveRoutes(ctx context.Context, dev string) ([]liveRoute, error) {
	var all []liveRoute
	for _, fam := range []string{"-4", "-6"} {
		out, err := n.run(ctx, "ip", "-j", fam, "route", "show", "dev", dev)
		if err != nil {
			return nil, fmt.Errorf("ip route: %w", err)
		}
		var rs []liveRoute
		if err := json.Unmarshal(out, &rs); err != nil {
			return nil, fmt.Errorf("ip route: %w", err)
		}
		all = append(all, rs...)
	}
	return all, nil
}

// resolved returns the words after "Link N (dev):" of resolvectl dns or
// domain; ok is false when resolvectl cannot answer.
func (n *network) resolved(ctx context.Context, what, dev string) (words []string, ok bool) {
	out, err := n.run(ctx, "resolvectl", what, dev)
	if err != nil {
		return nil, false
	}
	_, rest, found := strings.Cut(string(out), "):")
	if !found {
		return nil, false
	}
	return strings.Fields(rest), true
}

// Observe reports the file and, for each desired value, whether it is
// live. Live values that the resource does not list are not Plumb's.
func (n *network) Observe(ctx context.Context) (State, error) {
	f, err := readManaged(n.env.Root, NetworkFile(n.a.Name))
	if err != nil {
		return nil, err
	}
	if !f.exists {
		return State{wire.AttrState: wire.StateAbsent}, nil
	}
	st := State{wire.AttrState: wire.StatePresent, networkAttrFile: networkFileChanged}
	if key, ok := fileKey(f.content); ok {
		if want, err := n.fileContent(key); err == nil && want == f.content && f.mode&0o077 == 0 {
			st[networkAttrFile] = networkFileOK
		}
	}
	if n.a.State == wire.StateAbsent {
		return st, nil
	}
	dev, _, err := n.mainLink(ctx, observeRouteWait)
	if err != nil {
		return nil, err
	}
	addrs, err := n.liveAddrs(ctx, dev)
	if err != nil {
		return nil, err
	}
	routes, err := n.liveRoutes(ctx, dev)
	if err != nil {
		return nil, err
	}
	var live NetworkSpec
	for _, a := range n.spec.Addresses {
		if slices.Contains(addrs, a.Addr()) {
			live.Addresses = append(live.Addresses, a)
		}
	}
	for _, r := range n.spec.Routes {
		if slices.ContainsFunc(routes, func(l liveRoute) bool {
			dst, err := netip.ParsePrefix(l.Dst)
			if err != nil {
				if a, aerr := netip.ParseAddr(l.Dst); aerr == nil {
					dst = netip.PrefixFrom(a, a.BitLen())
				}
			}
			gw, _ := netip.ParseAddr(l.Gateway)
			return dst == r.To && gw == r.Via && (r.Metric == 0 || l.Metric == r.Metric)
		}) {
			live.Routes = append(live.Routes, r)
		}
	}
	if dns, ok := n.resolved(ctx, "dns", dev); ok {
		for _, a := range n.spec.Nameservers {
			if slices.Contains(dns, a.String()) {
				live.Nameservers = append(live.Nameservers, a)
			}
		}
	} else {
		live.Nameservers = n.spec.Nameservers // no resolvectl: no claim
	}
	if domains, ok := n.resolved(ctx, "domain", dev); ok {
		for _, d := range n.spec.Search {
			if slices.ContainsFunc(domains, func(x string) bool { return strings.EqualFold(strings.TrimPrefix(x, "~"), d) }) {
				live.Search = append(live.Search, d)
			}
		}
	} else {
		live.Search = n.spec.Search
	}
	for k, v := range live.attrs() {
		st[k] = v
	}
	return st, nil
}

// Diff: every change is disruptive, a network change can cut the link
// (D-332).
func (n *network) Diff(observed State) []wire.Change {
	present := observed[wire.AttrState] == wire.StatePresent
	if n.a.State == wire.StateAbsent {
		if present {
			return n.finish([]wire.Change{change(wire.AttrState, wire.StatePresent, wire.StateAbsent, wire.RiskDisruptive)})
		}
		return nil
	}
	want := n.spec.attrs()
	var out []wire.Change
	if !present {
		out = append(out, change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskDisruptive))
		for _, k := range networkAttrOrder[:4] {
			if want[k] != "" {
				out = append(out, change(k, nil, want[k], wire.RiskDisruptive))
			}
		}
		return n.finish(out)
	}
	for _, k := range networkAttrOrder[:4] {
		if from, ok := observed[k]; !ok || from != want[k] {
			out = append(out, change(k, observed[k], want[k], wire.RiskDisruptive))
		}
	}
	if f := observed[networkAttrFile]; f != networkFileOK {
		out = append(out, change(networkAttrFile, f, networkFileOK, wire.RiskDisruptive))
	}
	return n.finish(out)
}

// Watch: the file. Live addresses have no file event; the periodic check
// covers them.
func (n *network) Watch() []string { return []string{NetworkFile(n.a.Name)} }

// --- safe apply (D-349) ---

// preflight builds the result in a temporary root and checks it (D-348):
// the main entry keeps its address, default routes, match and set-name,
// and has every value of the resource; netplan generate passes.
//
// own are the addresses of the current file of this resource: they are
// not the main address, so their removal is no main-address change.
func (n *network) preflight(ctx context.Context, key string, before npView, own []netip.Prefix) error {
	tmp, err := os.MkdirTemp("", "plumb-netplan-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, dir := range []string{"/lib/netplan", "/etc/netplan", "/run/netplan"} {
		files, _ := filepath.Glob(filepath.Join(hostPath(n.env, dir), "*.yaml"))
		if len(files) == 0 {
			continue
		}
		if err := os.MkdirAll(filepath.Join(tmp, dir), 0o700); err != nil {
			return err
		}
		for _, f := range files {
			if filepath.Base(f) == filepath.Base(NetworkFile(n.a.Name)) && dir == netplanEtc {
				continue // ours: replaced below
			}
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(tmp, dir, filepath.Base(f)), b, 0o600); err != nil {
				return err
			}
		}
	}
	if n.a.State == wire.StatePresent {
		if err := os.MkdirAll(filepath.Join(tmp, netplanEtc), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, NetworkFile(n.a.Name)), []byte(n.content), 0o600); err != nil {
			return err
		}
	}
	view, err := n.netplanView(ctx, tmp)
	if err != nil {
		return err
	}
	after, ok := view[key]
	if !ok {
		return fmt.Errorf("preflight: the entry %s of the main NIC is gone", key)
	}
	// The main addresses: before without the current file of this
	// resource, after also without its new values.
	mainOf := func(v npView, skip ...[]netip.Prefix) []string {
		var out []string
		for _, p := range v.addrs() {
			mine := func(x netip.Prefix) bool { return x.Addr() == p.Addr() }
			if !slices.ContainsFunc(slices.Concat(skip...), mine) {
				out = append(out, p.String())
			}
		}
		slices.Sort(out)
		return out
	}
	switch {
	case !slices.Equal(mainOf(before, own), mainOf(after, own, n.spec.Addresses)):
		return errors.New("preflight: the main address of the NIC would change")
	case !slices.Equal(before.defaults(), after.defaults()):
		return errors.New("preflight: a default route would change")
	case fmt.Sprint(before.Match) != fmt.Sprint(after.Match) || before.SetName != after.SetName:
		return errors.New("preflight: match or set-name of the main NIC would change")
	}
	if n.a.State == wire.StatePresent {
		got := after.addrs()
		for i, a := range n.spec.Addresses {
			if !slices.Contains(got, a) {
				return fmt.Errorf("preflight: address %d is missing in the merged netplan", i+1)
			}
		}
		for i, r := range n.spec.Routes {
			if !slices.ContainsFunc(after.Routes, func(x npRoute) bool {
				to, err := netip.ParsePrefix(x.To)
				via, err2 := netip.ParseAddr(x.Via)
				return err == nil && err2 == nil && to.Masked() == r.To && via == r.Via && x.Metric == r.Metric
			}) {
				return fmt.Errorf("preflight: route %d is missing in the merged netplan", i+1)
			}
		}
		for i, a := range n.spec.Nameservers {
			if !slices.Contains(after.Nameservers.Addresses, a.String()) {
				return fmt.Errorf("preflight: name server %d is missing in the merged netplan", i+1)
			}
		}
		for i, d := range n.spec.Search {
			if !slices.Contains(after.Nameservers.Search, d) {
				return fmt.Errorf("preflight: search domain %d is missing in the merged netplan", i+1)
			}
		}
	}
	if _, err := n.run(ctx, "netplan", "generate", "--root-dir", tmp); err != nil {
		return fmt.Errorf("preflight: netplan generate: %w", err)
	}
	return nil
}

// Prepare checks the change and makes the confirmed state restorable
// (D-348, D-349): the main NIC, the file, the preflight, the probe of new
// addresses, the confirmed copy and the rollback timer. Nothing live
// changes; an error leaves the guest as it was.
func (n *network) Prepare(ctx context.Context) error {
	if n.env.StateDir == "" {
		return errors.New("network needs the agent state dir")
	}
	dev, main, err := n.mainLink(ctx, routeWait)
	if err != nil {
		return err
	}
	view, err := n.netplanView(ctx, n.env.Root)
	if err != nil {
		return err
	}
	key, err := mainKey(view, dev, main)
	if err != nil {
		return err
	}
	for i, a := range n.spec.Addresses {
		if a.Addr() == main {
			return fmt.Errorf("address %d is the main address of the Target", i+1)
		}
	}
	if n.content, err = n.fileContent(key); err != nil {
		return err
	}
	cur, err := readManaged(n.env.Root, NetworkFile(n.a.Name))
	if err != nil {
		return err
	}
	if err := n.preflight(ctx, key, view[key], fileAddrs(cur.content)); err != nil {
		return err
	}
	if n.a.State == wire.StatePresent {
		live, err := n.liveAddrs(ctx, dev)
		if err != nil {
			return err
		}
		var fresh []netip.Addr
		for _, a := range n.spec.Addresses {
			if !slices.Contains(live, a.Addr()) {
				fresh = append(fresh, a.Addr())
			}
		}
		if len(fresh) > 0 {
			if err := networkProbe(ctx, dev, fresh); err != nil {
				return err
			}
		}
	}
	confirmed := networkConfirmed(n.env, n.a.Name)
	if err := os.MkdirAll(filepath.Dir(confirmed), 0o700); err != nil {
		return err
	}
	// The previous file, or empty for "absent" (a file of ours is never
	// empty: it has its header line).
	var prev []byte
	if cur.exists {
		prev = []byte(cur.content)
	}
	if err := installNetworkBootUnit(ctx, n.env); err != nil {
		return err
	}
	if err := writeAtomic(confirmed, prev); err != nil {
		return err
	}
	script, err := networkRollbackScript(n.env, n.a.Name)
	if err == nil {
		err = armRollback(ctx, n.env, "network", "Plumb network rollback", "/bin/sh", "-c", script)
	}
	if err != nil {
		// Nothing half-armed stays (n4 of #249): a copy without a timer
		// would make a later agent start or boot restore it.
		_ = os.Remove(confirmed)
		return err
	}
	return nil
}

// NetworkBootUnit puts back every unconfirmed network change at boot,
// before systemd-networkd starts (n1 of #249): a reboot inside the window
// with an agent that does not start keeps no unconfirmed file. A copy
// exists only between Prepare and Commit or Restore, so the unit acts only
// then; it is the same rule as the agent's restore at start (D-349), which
// runs again afterwards (idempotent) and drops the copies.
const NetworkBootUnit = "plumb-agent-network.service"

var safeUnitPath = regexp.MustCompile(`^/[A-Za-z0-9/._-]+$`)

// networkBootUnitContent is the unit for the state dir of env. In a unit
// file "$" is written "$$".
func networkBootUnitContent(env Env) (string, error) {
	dir := filepath.Join(env.StateDir, networkConfirmedDir)
	if !safeUnitPath.MatchString(dir) {
		return "", errors.New("network: the agent state dir has characters that a unit cannot hold")
	}
	script := `for c in ` + dir + `/network-*.confirmed; do ` +
		`[ -e "$$c" ] || continue; n=$${c##*/network-}; n=$${n%.confirmed}; f=` + netplanEtc + `/60-plumb-$$n.yaml; ` +
		`if [ -s "$$c" ]; then install -m 600 "$$c" "$$f"; else rm -f "$$f"; fi; done; netplan generate`
	return `# Managed by the Plumb agent. Do not edit.
[Unit]
Description=Plumb network: put back an unconfirmed network change
DefaultDependencies=no
After=local-fs.target
Before=systemd-networkd.service network-pre.target shutdown.target
Wants=network-pre.target
Conflicts=shutdown.target
ConditionPathExistsGlob=` + dir + `/network-*.confirmed

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c '` + script + `'

[Install]
WantedBy=sysinit.target
`, nil
}

// installNetworkBootUnit writes and enables the boot unit when it differs.
func installNetworkBootUnit(ctx context.Context, env Env) error {
	want, err := networkBootUnitContent(env)
	if err != nil {
		return err
	}
	cur, err := readManaged(env.Root, unitDir+NetworkBootUnit)
	if err != nil {
		return err
	}
	if cur.exists && cur.content == want {
		return nil
	}
	if err := writeManaged(env.Root, unitDir+NetworkBootUnit, []byte(want), 0o644); err != nil {
		return err
	}
	if _, err := env.Run.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, err = env.Run.Run(ctx, "systemctl", "enable", NetworkBootUnit)
	return err
}

// networkRollbackScript puts the confirmed file back (or removes the file)
// and runs netplan apply; the rollback timer runs it without the agent.
func networkRollbackScript(env Env, name string) (string, error) {
	c, f := networkConfirmed(env, name), hostPath(env, NetworkFile(name))
	if strings.ContainsAny(c+f, "'\n") {
		return "", errors.New("network: a path holds a quote")
	}
	return "if [ -s '" + c + "' ]; then install -m 600 '" + c + "' '" + f + "'; elif [ -e '" + c + "' ]; then rm -f '" + f + "'; fi; netplan apply", nil
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Apply writes (or removes) the file and runs netplan apply: the live
// change of the safe apply.
func (n *network) Apply(ctx context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	if n.a.State == wire.StateAbsent {
		if err := removeManaged(n.env.Root, NetworkFile(n.a.Name)); err != nil {
			return err
		}
	} else {
		if n.content == "" {
			return errors.New("network: Apply without Prepare")
		}
		if err := writeManaged(n.env.Root, NetworkFile(n.a.Name), []byte(n.content), 0o600); err != nil {
			return err
		}
	}
	if _, err := n.run(ctx, "netplan", "apply"); err != nil {
		return fmt.Errorf("netplan apply: %w", err)
	}
	return nil
}

// Commit keeps the new state: the timer stops and the confirmed copy goes.
func (n *network) Commit(ctx context.Context) error {
	disarmRollback(ctx, n.env, "network")
	if err := os.Remove(networkConfirmed(n.env, n.a.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Restore puts the confirmed file back live.
func (n *network) Restore(ctx context.Context) error {
	return restoreNetworkFile(ctx, n.env, n.a.Name)
}

// restoreNetworkFile writes the confirmed copy of name back (or removes
// the file), runs netplan apply, stops the timer and drops the copy. No
// copy: nothing was prepared, only the timer stops.
func restoreNetworkFile(ctx context.Context, env Env, name string) error {
	c := networkConfirmed(env, name)
	prev, err := os.ReadFile(c)
	if errors.Is(err, os.ErrNotExist) {
		disarmRollback(ctx, env, "network")
		return nil
	}
	if err != nil {
		return err
	}
	if len(prev) > 0 {
		err = writeManaged(env.Root, NetworkFile(name), prev, 0o600)
	} else {
		err = removeManaged(env.Root, NetworkFile(name))
	}
	if err != nil {
		return fmt.Errorf("restore the confirmed network file: %w", err)
	}
	if _, err := env.Run.Run(ctx, "netplan", "apply"); err != nil {
		return fmt.Errorf("restore the confirmed network: netplan apply: %w", err)
	}
	disarmRollback(ctx, env, "network")
	return os.Remove(c)
}

// restoreNetwork is the restore at agent start (D-349): every confirmed
// copy is put back.
func restoreNetwork(ctx context.Context, env Env) error {
	if env.StateDir == "" {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(env.StateDir, networkConfirmedDir, "network-*.confirmed"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		disarmRollback(ctx, env, "network")
	}
	for _, f := range files {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "network-"), ".confirmed")
		if !networkNameRE.MatchString(name) {
			continue
		}
		if err := restoreNetworkFile(ctx, env, name); err != nil {
			return err
		}
	}
	return nil
}
