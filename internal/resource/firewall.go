package resource

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
)

// Paths and units of the firewall (D-295, D-300). The boot file and the
// unit are agent paths (wire.IsAgentPath, wire.IsAgentUnit), so no Block
// can write them and bypass the checks of this file (review F2 of T-111).
const (
	// FirewallBootFile holds the confirmed rules. plumb-agent-firewall.service
	// loads it at boot; the rollback timer and a restore load it too.
	FirewallBootFile = "/etc/plumb-agent/firewall.nft"
	// firewallNextFile is the script of a live apply.
	firewallNextFile = "/etc/plumb-agent/firewall.next"
	// FirewallUnit loads the boot file before the network comes up.
	FirewallUnit     = "plumb-agent-firewall.service"
	firewallUnitPath = unitDir + FirewallUnit
	// FirewallRollback is the rollback timer of the firewall
	// (RollbackUnit("firewall")): it loads the boot file.
	FirewallRollback = "plumb-agent-firewall-rollback"
	nftPath          = "/usr/sbin/nft"
)

// firewallTable is the only nftables table that the agent reads or changes.
const firewallTable = "inet plumb"

// firewallUnitContent is plumb-agent-firewall.service. It runs early and only
// when a boot file exists (review F4). After nftables.service: that unit
// loads /etc/nftables.conf, which starts with "flush ruleset".
const firewallUnitContent = `# Managed by the Plumb agent. Do not edit.
[Unit]
Description=Plumb firewall (table inet plumb, confirmed rules)
DefaultDependencies=no
After=local-fs.target nftables.service
Before=network-pre.target shutdown.target
Wants=network-pre.target
Conflicts=shutdown.target
ConditionPathExists=` + FirewallBootFile + `

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=` + nftPath + ` -f ` + FirewallBootFile + `

[Install]
WantedBy=sysinit.target
`

// Header lines of the boot file. The first holds the applied attributes
// (normalized), the second the listing of the table that nft printed
// after the apply, base64. Observe compares both with the guest.
const (
	fwHeaderAttrs = "# plumb-firewall 1 "
	fwHeaderList  = "# plumb-list "
)

// fwState are the observed and desired values of a firewall, normalized.
// It is the first header line of the boot file.
type fwState struct {
	State    string `json:"state"`
	Policy   string `json:"policy,omitempty"`
	TCPPorts string `json:"tcp_ports,omitempty"`
	UDPPorts string `json:"udp_ports,omitempty"`
	Sources  string `json:"sources,omitempty"`
	SSH      string `json:"ssh,omitempty"`
	Ping     bool   `json:"ping,omitempty"`
}

// Attribute names of the changes. ssh is the effective SSH rule, so the
// Plan shows "tcp/22 from everywhere" (review of T-111, point 2). table is
// a change of the table outside Plumb (drift).
const (
	fwAttrSSH   = "ssh"
	fwAttrTable = "table"
)

const (
	fwTableOK      = "as Plumb wrote it"
	fwTableChanged = "changed outside Plumb"
)

type firewallAttrs struct {
	Name       string          `json:"name"`
	Policy     string          `json:"policy"`
	TCPPorts   string          `json:"tcp_ports"`
	UDPPorts   string          `json:"udp_ports"`
	Sources    string          `json:"sources"`
	SSHPort    json.RawMessage `json:"ssh_port"`
	SSHSources string          `json:"ssh_sources"`
	AllowNoSSH bool            `json:"allow_no_ssh"`
	Ping       *bool           `json:"ping"`
	State      string          `json:"state"`
}

// firewall is the input filter of the guest (schema/firewall.json). Apply
// changes only the live table; the confirmed rules in the boot file
// change at Commit. The agent runs it with safe apply (SafeApplier).
type firewall struct {
	base
	want   fwState
	script string // the nft script of want
}

// Limits of the schema.
const (
	maxPortList   = 1024
	maxSourceList = 4096
)

func newFirewall(r wire.Resource, env Env) (Resource, error) {
	f := &firewall{base: base{res: r, env: env}}
	a := firewallAttrs{Policy: "drop", State: wire.StatePresent}
	if err := decode(r, "firewall", &a, func() string { return a.Name }); err != nil {
		return nil, err
	}
	// Every value is checked here, before the agent writes the marker or
	// changes anything (review F5 of T-111).
	want, err := checkFirewall(r, a)
	if err != nil {
		return nil, fmt.Errorf("resource %s: %w", r.ID, err)
	}
	f.want = want
	f.script = fwScript(want)
	return f, nil
}

func checkFirewall(r wire.Resource, a firewallAttrs) (fwState, error) {
	if a.Name != "main" {
		return fwState{}, errors.New("name must be main")
	}
	switch a.State {
	case wire.StateAbsent:
		for k := range r.Attrs {
			if k != "name" && k != wire.AttrState {
				return fwState{}, fmt.Errorf("%s with state absent", k)
			}
		}
		return fwState{State: wire.StateAbsent}, nil
	case wire.StatePresent:
	default:
		return fwState{}, fmt.Errorf("bad state %q", a.State)
	}
	if a.Policy != "accept" && a.Policy != "drop" {
		return fwState{}, fmt.Errorf("bad policy %q", a.Policy)
	}
	s := fwState{State: wire.StatePresent, Policy: a.Policy, Ping: a.Ping == nil || *a.Ping}
	var err error
	if s.TCPPorts, err = normPorts(a.TCPPorts); err != nil {
		return fwState{}, fmt.Errorf("tcp_ports: %w", err)
	}
	if s.UDPPorts, err = normPorts(a.UDPPorts); err != nil {
		return fwState{}, fmt.Errorf("udp_ports: %w", err)
	}
	if s.Sources, err = normSources(a.Sources); err != nil {
		return fwState{}, fmt.Errorf("sources: %w", err)
	}
	if a.AllowNoSSH {
		if len(a.SSHPort) > 0 || a.SSHSources != "" {
			return fwState{}, errors.New("ssh_port and ssh_sources must be missing with allow_no_ssh")
		}
		s.SSH = "closed (allow_no_ssh)"
		return s, nil
	}
	port, err := sshPort(a.SSHPort)
	if err != nil {
		return fwState{}, err
	}
	src, err := normSources(a.SSHSources)
	if err != nil {
		return fwState{}, fmt.Errorf("ssh_sources: %w", err)
	}
	s.SSH = "tcp/" + strconv.Itoa(port) + " from " + orEverywhere(src)
	return s, nil
}

func orEverywhere(src string) string {
	if src == "" {
		return "everywhere"
	}
	return src
}

// sshPort reads ssh_port: a string or an integer, 1 … 65535, default 22.
func sshPort(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 22, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.HasPrefix(s, "0") {
			return 0, errors.New("ssh_port must be 1 … 65535")
		}
		raw = json.RawMessage(s)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n < 1 || n > 65535 {
		return 0, errors.New("ssh_port must be 1 … 65535")
	}
	return n, nil
}

// portRange is one port or range of a list.
type portRange struct{ lo, hi int }

// normPorts checks a port list and returns it sorted, with overlapping and
// adjacent ranges merged: an nft interval set refuses overlaps.
func normPorts(list string) (string, error) {
	if list == "" {
		return "", nil
	}
	if len(list) > maxPortList {
		return "", fmt.Errorf("longer than %d bytes", maxPortList)
	}
	var rs []portRange
	for p := range strings.SplitSeq(list, ",") {
		lo, hi, isRange := strings.Cut(p, "-")
		a, err1 := portNum(lo)
		b, err2 := a, error(nil)
		if isRange {
			b, err2 = portNum(hi)
		}
		if err1 != nil || err2 != nil {
			return "", fmt.Errorf("bad port %q (1 … 65535)", p)
		}
		if b < a {
			return "", fmt.Errorf("range %q goes down", p)
		}
		rs = append(rs, portRange{a, b})
	}
	slices.SortFunc(rs, func(x, y portRange) int { return x.lo - y.lo })
	var merged []portRange
	for _, r := range rs {
		if n := len(merged); n > 0 && r.lo <= merged[n-1].hi+1 {
			merged[n-1].hi = max(merged[n-1].hi, r.hi)
			continue
		}
		merged = append(merged, r)
	}
	out := make([]string, len(merged))
	for i, r := range merged {
		out[i] = strconv.Itoa(r.lo)
		if r.hi != r.lo {
			out[i] += "-" + strconv.Itoa(r.hi)
		}
	}
	return strings.Join(out, ","), nil
}

func portNum(s string) (int, error) {
	if s == "" || len(s) > 5 || strings.TrimLeft(s, "0123456789") != "" {
		return 0, errors.New("not a port")
	}
	n, _ := strconv.Atoi(s)
	if n < 1 || n > 65535 {
		return 0, errors.New("not a port")
	}
	return n, nil
}

// normSources checks a CIDR list and returns it masked, IPv4 first, sorted,
// without a network that another one of the list contains (an nft
// interval set refuses overlaps).
func normSources(list string) (string, error) {
	if list == "" {
		return "", nil
	}
	if len(list) > maxSourceList {
		return "", fmt.Errorf("longer than %d bytes", maxSourceList)
	}
	var ps []netip.Prefix
	for c := range strings.SplitSeq(list, ",") {
		p, err := netip.ParsePrefix(c)
		if err != nil || p.Addr().Zone() != "" {
			return "", fmt.Errorf("bad network %q", c)
		}
		if p.Addr().Is4In6() {
			return "", fmt.Errorf("bad network %q: write IPv4 as a.b.c.d/n", c)
		}
		ps = append(ps, p.Masked())
	}
	// Wider networks first, so a contained one finds its container.
	slices.SortFunc(ps, func(x, y netip.Prefix) int {
		if x.Addr().Is4() != y.Addr().Is4() {
			if x.Addr().Is4() {
				return -1
			}
			return 1
		}
		if x.Bits() != y.Bits() {
			return x.Bits() - y.Bits()
		}
		return x.Addr().Compare(y.Addr())
	})
	var kept []netip.Prefix
	for _, p := range ps {
		if !slices.ContainsFunc(kept, func(k netip.Prefix) bool { return k.Overlaps(p) }) {
			kept = append(kept, p)
		}
	}
	slices.SortFunc(kept, func(x, y netip.Prefix) int {
		if x.Addr().Is4() != y.Addr().Is4() {
			if x.Addr().Is4() {
				return -1
			}
			return 1
		}
		if c := x.Addr().Compare(y.Addr()); c != 0 {
			return c
		}
		return x.Bits() - y.Bits()
	})
	out := make([]string, len(kept))
	for i, p := range kept {
		out[i] = p.String()
	}
	return strings.Join(out, ","), nil
}

// splitFamilies splits a normalized source list into IPv4 and IPv6.
func splitFamilies(list string) (v4, v6 []string) {
	if list == "" {
		return nil, nil
	}
	for c := range strings.SplitSeq(list, ",") {
		if strings.Contains(c, ":") {
			v6 = append(v6, c)
		} else {
			v4 = append(v4, c)
		}
	}
	return v4, v6
}

// fwDeleteScript removes the table, also when it does not exist: the
// first line creates it, both run in one transaction.
const fwDeleteScript = "table " + firewallTable + "\ndelete table " + firewallTable + "\n"

// fwScript is the nft script of s: it replaces the table atomically
// (nft -f runs it as one transaction). The rule order is fixed by D-300.
func fwScript(s fwState) string {
	if s.State == wire.StateAbsent {
		return fwDeleteScript
	}
	var b strings.Builder
	b.WriteString(fwDeleteScript)
	b.WriteString("table " + firewallTable + " {\n\tchain input {\n")
	b.WriteString("\t\ttype filter hook input priority filter; policy " + s.Policy + ";\n")
	b.WriteString("\t\tiif \"lo\" accept\n")
	b.WriteString("\t\tct state established,related accept\n")
	b.WriteString("\t\ticmpv6 type { nd-neighbor-solicit, nd-neighbor-advert, nd-router-advert } accept\n")
	b.WriteString("\t\tip6 saddr fe80::/10 udp dport 546 accept\n")
	if s.Ping {
		b.WriteString("\t\ticmp type echo-request accept\n")
		b.WriteString("\t\ticmpv6 type echo-request accept\n")
	}
	if port, src, ok := fwSSHRule(s.SSH); ok {
		fwRule(&b, "tcp", port, src)
	}
	fwRule(&b, "tcp", s.TCPPorts, s.Sources)
	fwRule(&b, "udp", s.UDPPorts, s.Sources)
	b.WriteString("\t}\n}\n")
	return b.String()
}

// fwSSHRule reads back the port and sources of the ssh value.
func fwSSHRule(ssh string) (port, src string, ok bool) {
	rest, ok := strings.CutPrefix(ssh, "tcp/")
	if !ok {
		return "", "", false
	}
	port, src, _ = strings.Cut(rest, " from ")
	if src == "everywhere" {
		src = ""
	}
	return port, src, true
}

// fwRule writes the accept rules of one protocol: one rule per address
// family when sources are set. The values are checked lists of digits,
// "-", ",", hex digits, ":", "." and "/", so nothing can break the script.
func fwRule(b *strings.Builder, proto, ports, sources string) {
	if ports == "" {
		return
	}
	set := "{ " + strings.ReplaceAll(ports, ",", ", ") + " }"
	if sources == "" {
		fmt.Fprintf(b, "\t\t%s dport %s accept\n", proto, set)
		return
	}
	v4, v6 := splitFamilies(sources)
	if len(v4) > 0 {
		fmt.Fprintf(b, "\t\tip saddr { %s } %s dport %s accept\n", strings.Join(v4, ", "), proto, set)
	}
	if len(v6) > 0 {
		fmt.Fprintf(b, "\t\tip6 saddr { %s } %s dport %s accept\n", strings.Join(v6, ", "), proto, set)
	}
}

// bootFile is the content of the boot file for s with the listing that
// nft printed after the apply.
func fwBootFile(s fwState, listing string) string {
	j, _ := json.Marshal(s)
	return "# Managed by the Plumb agent (resource firewall:main). Local changes are drift.\n" +
		fwHeaderAttrs + string(j) + "\n" +
		fwHeaderList + base64.StdEncoding.EncodeToString([]byte(listing)) + "\n" +
		fwScript(s)
}

// parseBootFile reads the header lines. ok is false for a file that the
// agent did not write.
func parseBootFile(content string) (s fwState, listing string, ok bool) {
	for line := range strings.SplitSeq(content, "\n") {
		switch {
		case strings.HasPrefix(line, fwHeaderAttrs):
			if json.Unmarshal([]byte(line[len(fwHeaderAttrs):]), &s) != nil {
				return fwState{}, "", false
			}
		case strings.HasPrefix(line, fwHeaderList):
			b, err := base64.StdEncoding.DecodeString(line[len(fwHeaderList):])
			if err != nil {
				return fwState{}, "", false
			}
			listing = string(b)
			return s, listing, s.State != ""
		case !strings.HasPrefix(line, "#"):
			return fwState{}, "", false
		}
	}
	return fwState{}, "", false
}

// errNoNft is the text when the package is missing (D-295).
var errNoNft = errors.New("nft not found: install the package nftables (a package resource before this one)")

// listTable returns the listing of the table, or exists false.
func (f *firewall) listTable(ctx context.Context) (string, bool, error) {
	out, err := f.env.Run.Run(ctx, "nft", "list", "table", "inet", "plumb")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", false, errNoNft
		}
		var ee *ExitError
		if errors.As(err, &ee) && strings.Contains(ee.Stderr, "No such file or directory") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("list table inet plumb: %w", err)
	}
	return string(out), true, nil
}

// Observe reads the live table and the boot file. Without the table the
// state is absent. The attributes come from the boot file; a live table
// that differs from the listing at the last commit is drift (table). A
// table without a boot file of the agent is not Plumb's: every attribute
// is unknown.
func (f *firewall) Observe(ctx context.Context) (State, error) {
	listing, exists, err := f.listTable(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return State{wire.AttrState: wire.StateAbsent}, nil
	}
	st := State{wire.AttrState: wire.StatePresent, fwAttrTable: fwTableChanged}
	boot, err := readManaged(f.env.Root, FirewallBootFile)
	if err != nil {
		return nil, err
	}
	conf, recorded, ok := parseBootFile(boot.content)
	if !boot.exists || !ok || conf.State != wire.StatePresent {
		return st, nil
	}
	if listing == recorded {
		st[fwAttrTable] = fwTableOK
	}
	for k, v := range conf.attrs() {
		st[k] = v
	}
	return st, nil
}

// attrs returns the attributes that Diff compares.
func (s fwState) attrs() map[string]any {
	return map[string]any{"policy": s.Policy, "tcp_ports": s.TCPPorts, "udp_ports": s.UDPPorts,
		"sources": s.Sources, fwAttrSSH: s.SSH, "ping": s.Ping}
}

// fwAttrOrder is the order of the changes.
var fwAttrOrder = []string{"policy", fwAttrSSH, "tcp_ports", "udp_ports", "sources", "ping"}

// Diff: every change is disruptive, a firewall can cut the network (D-293).
func (f *firewall) Diff(observed State) []wire.Change {
	present := observed[wire.AttrState] == wire.StatePresent
	if f.want.State == wire.StateAbsent {
		if present {
			return f.finish([]wire.Change{change(wire.AttrState, wire.StatePresent, wire.StateAbsent, wire.RiskDisruptive)})
		}
		return nil
	}
	var out []wire.Change
	if !present {
		out = append(out, change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskDisruptive))
	} else if t := observed[fwAttrTable]; t != fwTableOK {
		out = append(out, change(fwAttrTable, t, fwTableOK, wire.RiskDisruptive))
	}
	want := f.want.attrs()
	for _, k := range fwAttrOrder {
		if from, ok := observed[k]; !ok || from != want[k] {
			out = append(out, change(k, from, want[k], wire.RiskDisruptive))
		}
	}
	return f.finish(out)
}

// Apply loads the desired table live (nft -f, atomic). It does not touch
// the boot file: Commit does that after the round trip.
func (f *firewall) Apply(ctx context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	return loadScript(ctx, f.env, f.script)
}

// loadScript writes script to the next file and loads it.
func loadScript(ctx context.Context, env Env, script string) error {
	if err := writeManaged(env.Root, firewallNextFile, []byte(script), 0o600); err != nil {
		return err
	}
	defer func() { _ = removeManaged(env.Root, firewallNextFile) }()
	return runNft(ctx, env, "-f", hostPath(env, firewallNextFile))
}

func runNft(ctx context.Context, env Env, args ...string) error {
	if _, err := env.Run.Run(ctx, "nft", args...); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errNoNft
		}
		return err
	}
	return nil
}

func hostPath(env Env, p string) string {
	if env.Root == "/" {
		return p
	}
	return strings.TrimSuffix(env.Root, "/") + p
}

// Prepare makes the confirmed state restorable before Apply changes the
// live table (D-300): the boot file exists (a first apply writes one that
// deletes the table), the boot unit is installed, and the rollback timer is
// armed (review F1 of T-111). An error leaves the live table as it was.
func (f *firewall) Prepare(ctx context.Context) error {
	if _, _, err := f.listTable(ctx); err != nil {
		return err // no nft: fail before anything changes
	}
	boot, err := readManaged(f.env.Root, FirewallBootFile)
	if err != nil {
		return err
	}
	if !boot.exists {
		if err := writeManaged(f.env.Root, FirewallBootFile, []byte(fwBootFile(fwState{State: wire.StateAbsent}, "")), 0o600); err != nil {
			return err
		}
	}
	if err := installFirewallUnit(ctx, f.env); err != nil {
		return err
	}
	return armRollback(ctx, f.env, "firewall", "Plumb firewall rollback", nftPath, "-f", FirewallBootFile)
}

// installFirewallUnit writes and enables plumb-agent-firewall.service when it
// differs.
func installFirewallUnit(ctx context.Context, env Env) error {
	cur, err := readManaged(env.Root, firewallUnitPath)
	if err != nil {
		return err
	}
	if cur.exists && cur.content == firewallUnitContent {
		return nil
	}
	if err := writeManaged(env.Root, firewallUnitPath, []byte(firewallUnitContent), 0o644); err != nil {
		return err
	}
	if _, err := env.Run.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, err = env.Run.Run(ctx, "systemctl", "enable", FirewallUnit)
	return err
}

// Commit makes the live table the confirmed one: the boot file gets the
// desired script and the listing that nft prints now. state absent removes
// the boot file and disables the unit.
func (f *firewall) Commit(ctx context.Context) error {
	disarmRollback(ctx, f.env, "firewall")
	if f.want.State == wire.StateAbsent {
		if _, err := f.env.Run.Run(ctx, "systemctl", "disable", FirewallUnit); err != nil {
			return err
		}
		return removeManaged(f.env.Root, FirewallBootFile)
	}
	listing, exists, err := f.listTable(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("table inet plumb is gone after the apply")
	}
	return writeManaged(f.env.Root, FirewallBootFile, []byte(fwBootFile(f.want, listing)), 0o600)
}

// Restore puts the confirmed state back live.
func (f *firewall) Restore(ctx context.Context) error { return restoreFirewall(ctx, f.env) }

// restoreFirewall loads the boot file, or deletes the table when there is
// none, then stops the rollback timer.
func restoreFirewall(ctx context.Context, env Env) error {
	boot, err := readManaged(env.Root, FirewallBootFile)
	if err != nil {
		return err
	}
	if boot.exists {
		err = runNft(ctx, env, "-f", hostPath(env, FirewallBootFile))
	} else {
		err = loadScript(ctx, env, fwDeleteScript)
	}
	if err != nil {
		return fmt.Errorf("restore the confirmed rules: %w", err)
	}
	disarmRollback(ctx, env, "firewall")
	return nil
}

// Watch: nft has no file event; the periodic check covers the table. The
// boot file is the agent's own.
func (f *firewall) Watch() []string { return nil }

// Summary returns the lines that a Plan shows for a desired firewall
// (ResourcePlan.summary, D-300): the effective SSH rule, then the policy.
// nil for another type, state absent or an attribute that the agent
// would refuse.
func Summary(r wire.Resource) []string {
	if r.Type != "firewall" {
		return nil
	}
	a := firewallAttrs{Policy: "drop", State: wire.StatePresent}
	if err := decode(r, "firewall", &a, func() string { return a.Name }); err != nil {
		return nil
	}
	s, err := checkFirewall(r, a)
	if err != nil || s.State != wire.StatePresent {
		return nil
	}
	ssh := "no SSH rule (allow_no_ssh)"
	if port, src, ok := fwSSHRule(s.SSH); ok {
		ssh = "SSH tcp/" + port + " open from " + orEverywhere(src)
	}
	policy := "policy drop: other input is dropped"
	if s.Policy == "accept" {
		policy = "policy accept: other input is accepted"
	}
	return []string{ssh, policy}
}
