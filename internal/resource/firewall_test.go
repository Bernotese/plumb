package resource

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// fakeNft simulates nft (one table, inet plumb) and records systemd calls.
// The listing of the table is the table block of the last loaded script.
type fakeNft struct {
	mu        sync.Mutex
	table     string // "" = no table
	calls     [][]string
	missing   bool // nft is not installed
	failLoad  bool // nft -f fails
	loaded    []string
	listExtra string // appended to the listing (a change outside Plumb)
}

func (f *fakeNft) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	switch name {
	case "nft":
		if f.missing {
			return nil, &execNotFound{}
		}
		switch {
		case slices.Equal(args, []string{"list", "table", "inet", "plumb"}):
			if f.table == "" {
				return nil, &ExitError{Name: "nft", Code: 1, Stderr: "Error: No such file or directory\nlist table inet plumb"}
			}
			return []byte(f.table + f.listExtra), nil
		case len(args) == 2 && args[0] == "-f":
			if f.failLoad {
				return nil, &ExitError{Name: "nft", Code: 1, Stderr: "Error: syntax error"}
			}
			b, err := os.ReadFile(args[1])
			if err != nil {
				return nil, err
			}
			s := string(b)
			f.loaded = append(f.loaded, s)
			f.listExtra = ""
			if i := strings.Index(s, "table inet plumb {"); i >= 0 {
				f.table = s[i:]
			} else {
				f.table = ""
			}
			return nil, nil
		}
	case "systemctl", "systemd-run":
		return nil, nil
	}
	return nil, &ExitError{Name: name, Code: 2, Stderr: "unexpected call"}
}

func (f *fakeNft) count(words ...string) int {
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

// execNotFound wraps exec.ErrNotFound like ExecRunner does.
type execNotFound struct{}

func (*execNotFound) Error() string { return "nft: exec: \"nft\": executable file not found in $PATH" }
func (*execNotFound) Unwrap() error { return execErrNotFound }

func fwRes(attrs map[string]any) wire.Resource {
	a := map[string]any{"name": "main"}
	for k, v := range attrs {
		a[k] = v
	}
	return wire.Resource{ID: "firewall:main", Type: "firewall", Block: "fw", Attrs: a}
}

func newFwEnv(t *testing.T) (Env, *fakeNft) {
	t.Helper()
	root := guestRoot(t)
	for _, d := range []string{"etc/plumb-agent", "etc/systemd/system"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeNft{}
	return NewEnv(root, f), f
}

func TestFirewallChecks(t *testing.T) {
	env, _ := newFwEnv(t)
	bad := []map[string]any{
		{"tcp_ports": "0"},
		{"tcp_ports": "65536"},
		{"tcp_ports": "90-80"},
		{"tcp_ports": "80,"},
		{"udp_ports": "1-99999"},
		{"sources": "10.0.0.0/33"},
		{"sources": "::/999"},
		{"sources": "fe80::1%eth0/64"},
		{"sources": "::ffff:10.0.0.1/128"},
		{"ssh_sources": "10.0.0.0"},
		{"ssh_port": "99999"},
		{"ssh_port": 0},
		{"ssh_port": "22a"},
		{"ssh_port": "022"},
		{"allow_no_ssh": true, "ssh_port": 22},
		{"allow_no_ssh": true, "ssh_sources": "10.0.0.0/8"},
		{"state": "absent", "policy": "drop"},
		{"policy": "reject"},
		{"name": "other"},
	}
	for _, a := range bad {
		r := fwRes(a)
		if _, err := New(r, env); err == nil {
			t.Errorf("%v: accepted", a)
		}
	}
	good := map[string]fwState{
		`{}`: {State: "present", Policy: "drop", SSH: "tcp/22 from everywhere", Ping: true},
		`{"tcp_ports":"443,80,80,8000-8100,8050-8200,81","sources":"10.1.2.3/8,2001:db8::1/32,10.1.0.0/16,192.168.1.0/24"}`: {State: "present",
			Policy: "drop", TCPPorts: "80-81,443,8000-8200", Sources: "10.0.0.0/8,192.168.1.0/24,2001:db8::/32", SSH: "tcp/22 from everywhere", Ping: true},
		`{"ssh_port":"2222","ssh_sources":"192.168.0.0/16","ping":false,"policy":"accept"}`: {State: "present",
			Policy: "accept", SSH: "tcp/2222 from 192.168.0.0/16"},
		`{"ssh_port":2200}`:      {State: "present", Policy: "drop", SSH: "tcp/2200 from everywhere", Ping: true},
		`{"allow_no_ssh":true}`:  {State: "present", Policy: "drop", SSH: "closed (allow_no_ssh)", Ping: true},
		`{"state":"absent"}`:     {State: "absent"},
		`{"udp_ports":"53,1-5"}`: {State: "present", Policy: "drop", UDPPorts: "1-5,53", SSH: "tcp/22 from everywhere", Ping: true},
	}
	for js, want := range good {
		var a map[string]any
		mustJSON(t, js, &a)
		rs, err := New(fwRes(a), env)
		if err != nil {
			t.Errorf("%s: %v", js, err)
			continue
		}
		if got := rs.(*firewall).want; got != want {
			t.Errorf("%s:\n got %+v\nwant %+v", js, got, want)
		}
	}
}

func TestFirewallScript(t *testing.T) {
	s := fwState{State: "present", Policy: "drop", TCPPorts: "80,443", UDPPorts: "53", Sources: "10.0.0.0/8,fd00::/8",
		SSH: "tcp/2222 from 192.168.0.0/16", Ping: true}
	want := `table inet plumb
delete table inet plumb
table inet plumb {
	chain input {
		type filter hook input priority filter; policy drop;
		iif "lo" accept
		ct state established,related accept
		icmpv6 type { nd-neighbor-solicit, nd-neighbor-advert, nd-router-advert } accept
		ip6 saddr fe80::/10 udp dport 546 accept
		icmp type echo-request accept
		icmpv6 type echo-request accept
		ip saddr { 192.168.0.0/16 } tcp dport { 2222 } accept
		ip saddr { 10.0.0.0/8 } tcp dport { 80, 443 } accept
		ip6 saddr { fd00::/8 } tcp dport { 80, 443 } accept
		ip saddr { 10.0.0.0/8 } udp dport { 53 } accept
		ip6 saddr { fd00::/8 } udp dport { 53 } accept
	}
}
`
	if got := fwScript(s); got != want {
		t.Errorf("script:\n%s\nwant:\n%s", got, want)
	}
	noSSH := fwScript(fwState{State: "present", Policy: "accept", SSH: "closed (allow_no_ssh)"})
	if strings.Contains(noSSH, "dport") && strings.Contains(noSSH, "tcp dport") || strings.Contains(noSSH, "echo-request") {
		t.Errorf("allow_no_ssh without ping has a tcp or ping rule:\n%s", noSSH)
	}
	if got := fwScript(fwState{State: "absent"}); got != "table inet plumb\ndelete table inet plumb\n" {
		t.Errorf("absent: %q", got)
	}
}

func TestFirewallLifecycle(t *testing.T) {
	ctx := context.Background()
	env, nft := newFwEnv(t)
	rs, err := New(fwRes(map[string]any{"tcp_ports": "80"}), env)
	if err != nil {
		t.Fatal(err)
	}
	fw := rs.(*firewall)
	st, err := rs.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ch := rs.Diff(st)
	if wire.ActionOf(ch) != wire.ActionCreate || wire.MaxRisk(ch) != wire.RiskDisruptive {
		t.Fatalf("create: %+v", ch)
	}
	if !slices.ContainsFunc(ch, func(c wire.Change) bool { return c.Attr == "ssh" && c.To == "tcp/22 from everywhere" }) {
		t.Errorf("the Plan does not show the SSH rule: %+v", ch)
	}
	// Prepare: delete-only boot file, unit, timer; nothing live.
	if err := fw.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	boot := readFile(t, filepath.Join(env.Root, FirewallBootFile))
	if s, _, ok := parseBootFile(boot); !ok || s.State != wire.StateAbsent {
		t.Fatalf("boot file after prepare: %q", boot)
	}
	if !strings.Contains(readFile(t, filepath.Join(env.Root, firewallUnitPath)), "ConditionPathExists="+FirewallBootFile) {
		t.Error("unit not written")
	}
	if nft.count("systemctl", "enable", FirewallUnit) != 1 || nft.count("systemd-run", "--unit="+FirewallRollback) != 1 {
		t.Errorf("unit or timer missing: %v", nft.calls)
	}
	if nft.table != "" {
		t.Fatal("prepare changed the live table")
	}
	if err := rs.Apply(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nft.table, "tcp dport { 80 } accept") {
		t.Fatalf("live table: %q", nft.table)
	}
	if _, err := os.Stat(filepath.Join(env.Root, firewallNextFile)); !os.IsNotExist(err) {
		t.Error("next file left")
	}
	// Restore: back to no table, timer stopped.
	stops := nft.count("systemctl", "stop", FirewallRollback+".timer")
	if err := fw.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if nft.table != "" || nft.count("systemctl", "stop", FirewallRollback+".timer") != stops+1 {
		t.Fatalf("restore: table %q", nft.table)
	}
	// Apply again and commit.
	if err := fw.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rs.Apply(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if err := fw.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	st, err = rs.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ch := rs.Diff(st); len(ch) != 0 {
		t.Fatalf("after commit: %+v (state %v)", ch, st)
	}
	// The boot file loads the same table.
	if err := fw.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := rs.Observe(ctx); len(rs.Diff(st)) != 0 {
		t.Fatalf("after a restore of the committed file: %v", st)
	}
	// A rule added outside Plumb is drift of table.
	nft.listExtra = "\n# extra"
	st, _ = rs.Observe(ctx)
	if ch := rs.Diff(st); len(ch) != 1 || ch[0].Attr != "table" || ch[0].From != fwTableChanged {
		t.Fatalf("drift: %+v", ch)
	}
	// A changed desired port list.
	rs2, _ := New(fwRes(map[string]any{"tcp_ports": "80,443"}), env)
	nft.listExtra = ""
	st, _ = rs2.Observe(ctx)
	if ch := rs2.Diff(st); len(ch) != 1 || ch[0].Attr != "tcp_ports" || ch[0].From != "80" || ch[0].To != "80,443" {
		t.Fatalf("change: %+v", ch)
	}
	// absent: delete the table, the boot file and disable the unit.
	gone, _ := New(fwRes(map[string]any{"state": "absent"}), env)
	st, _ = gone.Observe(ctx)
	ch = gone.Diff(st)
	if wire.ActionOf(ch) != wire.ActionDelete {
		t.Fatalf("delete: %+v", ch)
	}
	g := gone.(*firewall)
	if err := g.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := gone.Apply(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if nft.table != "" {
		t.Fatal("table left")
	}
	if err := g.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.Root, FirewallBootFile)); !os.IsNotExist(err) {
		t.Error("boot file left after absent")
	}
	if nft.count("systemctl", "disable", FirewallUnit) != 1 {
		t.Error("unit not disabled")
	}
	// Restore without a boot file deletes the table.
	nft.table = "table inet plumb { }"
	if err := Restore(ctx, env, "firewall"); err != nil || nft.table != "" {
		t.Fatalf("restore without boot file: %v %q", err, nft.table)
	}
}

func TestFirewallForeignTable(t *testing.T) {
	ctx := context.Background()
	env, nft := newFwEnv(t)
	nft.table = "table inet plumb {\n}\n"
	rs, _ := New(fwRes(nil), env)
	st, err := rs.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ch := rs.Diff(st)
	if len(ch) == 0 || ch[0].Attr != "table" {
		t.Fatalf("a table without a boot file must differ: %+v", ch)
	}
}

func TestFirewallNoNft(t *testing.T) {
	ctx := context.Background()
	env, nft := newFwEnv(t)
	nft.missing = true
	rs, _ := New(fwRes(nil), env)
	if _, err := rs.Observe(ctx); err == nil || !strings.Contains(err.Error(), "nftables") {
		t.Fatalf("observe: %v", err)
	}
	if err := rs.(*firewall).Prepare(ctx); err == nil || !strings.Contains(err.Error(), "nftables") {
		t.Fatalf("prepare: %v", err)
	}
	if nft.count("systemd-run") != 0 {
		t.Error("timer armed without nft")
	}
}

func TestFirewallIsAgentPath(t *testing.T) {
	for _, p := range []string{FirewallBootFile, firewallNextFile, firewallUnitPath, "/etc/systemd/system/sysinit.target.wants/" + FirewallUnit,
		"/run/systemd/transient/" + FirewallRollback + ".timer"} {
		if !wire.IsAgentPath(p) {
			t.Errorf("%s is not an agent path", p)
		}
	}
	for _, u := range []string{FirewallUnit, FirewallRollback + ".timer", FirewallRollback + ".service"} {
		if !wire.IsAgentUnit(u) {
			t.Errorf("%s is not an agent unit", u)
		}
	}
	if !slices.Equal(wire.SafeApplyTypes, []string{"firewall"}) {
		t.Errorf("SafeApplyTypes = %v", wire.SafeApplyTypes)
	}
	typ, _ := Lookup("firewall")
	if !typ.NotInImage || !typ.Supports(wire.KindLXC) || !typ.Supports(wire.KindVM) {
		t.Errorf("type: %+v", typ)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var execErrNotFound = exec.ErrNotFound

func mustJSON(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatal(err)
	}
}
