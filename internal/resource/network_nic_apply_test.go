package resource

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// T-220 (D-403): a more NIC with real netplan get and generate (the fake
// guest simulates ip and netplan apply). No entry names the card: the
// file holds an own entry that matches Plumb's MAC.
func TestNetworkNICApplyCommit(t *testing.T) {
	ctx := context.Background()
	env, f, probed := newNetEnv(t)
	f.cards = [][2]string{{"ens19", nicMAC}}
	n := netRes(t, env, map[string]any{"mac": nicMAC, "addresses": "10.9.20.5/24"})
	ch := diffOf(t, n)
	if len(ch) == 0 || ch[0].Attr != wire.AttrState {
		t.Fatalf("create diff: %+v", ch)
	}
	if err := n.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*probed, []netip.Addr{netip.MustParseAddr("10.9.20.5")}) {
		t.Errorf("probed %v", *probed)
	}
	// The card was down (no entry names it): up before the probe, once.
	if !slices.Equal(f.upped, []string{"ens19"}) {
		t.Errorf("brought up %v", f.upped)
	}
	if err := n.Apply(ctx, ch); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(env.Root, NetworkFile("extra")))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"plumb-extra:", "macaddress: " + nicMAC, "dhcp4: false", "10.9.20.5/24"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("file lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "eth0") || strings.Contains(string(b), "set-name") {
		t.Errorf("the file touches the main NIC or renames the card:\n%s", b)
	}
	if err := n.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if ch := diffOf(t, n); len(ch) != 0 {
		t.Fatalf("after the apply: %+v", ch)
	}
	// dhcp on: a change of the file and of the observed dhcp.
	m := netRes(t, env, map[string]any{"mac": nicMAC, "dhcp": true})
	ch = diffOf(t, m)
	if !slices.ContainsFunc(ch, func(c wire.Change) bool { return c.Attr == "dhcp" && c.From == "false" && c.To == "true" }) {
		t.Fatalf("dhcp diff: %+v", ch)
	}
	if err := m.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(env.Root, NetworkFile("extra"))); !strings.Contains(string(b), "dhcp4: true") {
		t.Fatalf("dhcp file:\n%s", b)
	}
}

// cloud-init's entry names the card (match by MAC, set-name eth1): the
// file goes under that key, without its own match.
func TestNetworkNICCloudInitKey(t *testing.T) {
	ctx := context.Background()
	env, f, _ := newNetEnv(t)
	f.cards = [][2]string{{"eth1", nicMAC}}
	ci := cloudInit + "    eth1:\n      match:\n        macaddress: \"" + nicMAC + "\"\n      set-name: \"eth1\"\n"
	if err := os.WriteFile(filepath.Join(env.Root, "etc/netplan/50-cloud-init.yaml"), []byte(ci), 0o600); err != nil {
		t.Fatal(err)
	}
	n := netRes(t, env, map[string]any{"mac": nicMAC, "addresses": "10.9.20.5/24"})
	if err := n.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := n.Apply(ctx, diffOf(t, n)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(env.Root, NetworkFile("extra")))
	if !strings.Contains(string(b), "    eth1:\n") || strings.Contains(string(b), "match") || strings.Contains(string(b), "plumb-extra") {
		t.Fatalf("file:\n%s", b)
	}
	if err := n.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if ch := diffOf(t, n); len(ch) != 0 {
		t.Fatalf("after the apply: %+v", ch)
	}
}

// Refusals of a more NIC: nothing changes (D-403).
func TestNetworkNICRefusals(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		attrs map[string]any
		setup func(env Env, f *fakeNet)
		want  string
	}{
		{"no card", map[string]any{"mac": nicMAC, "addresses": "10.9.20.5/24"}, nil, "no network card of the VM has the MAC"},
		{"two cards", map[string]any{"mac": nicMAC}, func(_ Env, f *fakeNet) { f.cards = append(f.cards, [2]string{"ens20", nicMAC}) }, "2 network cards"},
		{"the main NIC", map[string]any{"mac": nicMAC}, func(_ Env, f *fakeNet) { f.cards, f.mac = nil, nicMAC }, "never the main NIC"},
		{"gateway with the main default", map[string]any{"mac": nicMAC, "addresses": "10.9.20.5/24", "gateway": "10.9.20.1"}, nil, "the main NIC has the default route"},
		{"two entries", map[string]any{"mac": nicMAC}, func(env Env, _ *fakeNet) {
			_ = os.WriteFile(filepath.Join(env.Root, "etc/netplan/70-other.yaml"),
				[]byte("network:\n  version: 2\n  ethernets:\n    ens19:\n      dhcp4: true\n    x:\n      match:\n        macaddress: \""+nicMAC+"\"\n"), 0o600)
		}, "2 entries for the network card"},
		{"main address", map[string]any{"mac": nicMAC, "addresses": "198.51.100.5/24"}, nil, "is the main address of the Target"},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, f, _ := newNetEnv(t)
			f.cards = [][2]string{{"ens19", nicMAC}}
			if c.name == "no card" {
				f.cards = nil
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

// Review M1 of #336: the render's absent nic<N> (no mac) removes the file
// of a removed card, also one under cloud-init's key; the main NIC stays.
func TestNetworkNICAbsentRemovesFile(t *testing.T) {
	ctx := context.Background()
	env, f, _ := newNetEnv(t)
	f.cards = [][2]string{{"eth1", nicMAC}}
	ci := cloudInit + "    eth1:\n      match:\n        macaddress: \"" + nicMAC + "\"\n      set-name: \"eth1\"\n"
	if err := os.WriteFile(filepath.Join(env.Root, "etc/netplan/50-cloud-init.yaml"), []byte(ci), 0o600); err != nil {
		t.Fatal(err)
	}
	n := netRes(t, env, map[string]any{"mac": nicMAC, "addresses": "10.9.20.5/24"})
	if err := n.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := n.Apply(ctx, diffOf(t, n)); err != nil {
		t.Fatal(err)
	}
	if err := n.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// The card leaves guest.nics (and the VM): the render sends absent.
	f.cards = nil
	gone := netRes(t, env, map[string]any{"state": "absent"})
	ch := diffOf(t, gone)
	if len(ch) != 1 || ch[0].Attr != wire.AttrState || ch[0].To != wire.StateAbsent {
		t.Fatalf("absent diff: %+v", ch)
	}
	if err := gone.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := gone.Apply(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if err := gone.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.Root, NetworkFile("extra"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the file of the removed card stays")
	}
	if ch := diffOf(t, gone); len(ch) != 0 {
		t.Fatalf("after the removal: %+v", ch)
	}
	// No file: absent is unchanged, also without a card (the common case).
	if ch := diffOf(t, netRes(t, env, map[string]any{"state": "absent"})); len(ch) != 0 {
		t.Fatalf("absent without a file: %+v", ch)
	}
}
