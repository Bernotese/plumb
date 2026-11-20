//go:build guest

package agent

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// mainAddrBound is how long the main address may be missing after the
// apply of the second card. The agent writes only the card's entry
// (D-419), but netplan apply makes networkd ask DHCP again on the main
// NIC: a refreshed lease with the same address and no gap is accepted
// (D-443, CI of #419: valid_lft 3595 s before, 3600 s after, no gap). A
// longer gap, or another address, fails.
const mainAddrBound = 2 * time.Second

// lftForever is the valid_life_time of an address without a lease.
const lftForever = 4294967295

// mainValidLft returns the valid_life_time (seconds) of the address a on
// dev, and whether dev has it.
func mainValidLft(t *testing.T, dev string, a netip.Addr) (uint64, bool) {
	t.Helper()
	var links []struct {
		AddrInfo []struct {
			Local         string `json:"local"`
			ValidLifeTime uint64 `json:"valid_life_time"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal(mustOut(t, "ip", "-j", "addr", "show", "dev", dev), &links); err != nil {
		t.Fatalf("ip -j addr show dev %s: %v", dev, err)
	}
	for _, l := range links {
		for _, ai := range l.AddrInfo {
			if ai.Local == a.String() {
				return ai.ValidLifeTime, true
			}
		}
	}
	return 0, false
}

// TestGuestNetworkNIC proves the network resource of a more NIC (T-220,
// D-403) with real netplan apply: lxd.sh hot-plugs a second virtio card
// with a MAC of Plumb's form (PLUMB_NIC_MAC); the resource finds it by
// that MAC (netplan matches the permanent MAC, D-370), writes its own
// entry and sets the address; the main NIC and its default route stay.
// With PLUMB_NETWORK_GUEST=1 a missing card fails the test, never skips.
func TestGuestNetworkNIC(t *testing.T) {
	if os.Getenv("PLUMB_NETWORK_GUEST") != "1" {
		t.Skip("network needs a cloud VM with netplan; only lxd.sh runs it (PLUMB_NETWORK_GUEST=1)")
	}
	mac := os.Getenv("PLUMB_NIC_MAC")
	if mac == "" {
		t.Fatal("PLUMB_NIC_MAC is not set: lxd.sh adds the second card")
	}
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires root in the systemd guest")
	}
	if _, err := exec.LookPath("netplan"); err != nil {
		t.Fatal("requires netplan")
	}
	dev, gw, main := guestDefault(t)
	card := cardOf(t, mac)
	for end := time.Now().Add(30 * time.Second); card == "" && time.Now().Before(end); card = cardOf(t, mac) {
		time.Sleep(time.Second) // the hot-plugged card shows up a moment later
	}
	if card == "" || card == dev {
		t.Fatalf("no second card with MAC %s (main %s, found %q)", mac, dev, card)
	}
	file := resource.NetworkFile("ci-nic")
	t.Cleanup(func() {
		_ = exec.Command("systemctl", "stop", resource.RollbackUnit("network")+".timer").Run()
		os.Remove(file)
		_ = exec.Command("netplan", "apply").Run()
		if rs, _, _ := waitDefaultRoute(60 * time.Second); len(rs) == 0 {
			t.Errorf("no default route 60 s after the cleanup's netplan apply")
		}
	})
	ctx := context.Background()
	env := resource.NewEnv("/", resource.ExecRunner{})
	env.StateDir = t.TempDir()
	env.Controller = gw.String()
	res := func(m string, addr string) resource.Resource {
		t.Helper()
		r, err := resource.New(wire.Resource{ID: "network:ci-nic", Type: "network", Block: "guest.nics",
			Attrs: map[string]any{"name": "ci-nic", "mac": m, "dhcp": false, "addresses": addr}}, env)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	apply := func(r resource.Resource) {
		t.Helper()
		obs, err := r.Observe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sa := r.(resource.SafeApplier)
		if err := sa.Prepare(ctx); err != nil {
			t.Fatal(err)
		}
		if err := r.Apply(ctx, r.Diff(obs)); err != nil {
			t.Fatal(err)
		}
		if err := sa.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	diff := func(r resource.Resource) []wire.Change {
		t.Helper()
		obs, err := r.Observe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return r.Diff(obs)
	}
	waitLive := func(what string, want bool) {
		t.Helper()
		for end := time.Now().Add(20 * time.Second); liveHas(t, card, what) != want; time.Sleep(500 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("%s on %s: want %v", what, card, want)
			}
		}
	}

	// 1. Apply: the own entry matches the MAC; the address is live on the
	// card; the main NIC keeps its address and default route.
	r := res(mac, "10.99.20.5/24")
	lft0, ok := mainValidLft(t, dev, main)
	if !ok {
		t.Fatalf("the main address %s is not on %s before the apply", main, dev)
	}
	// A lease that a test before just renewed has the full time, and a new
	// lease at the apply would show the same value. So wait until it
	// counted down by 5 s first; the log then tells a new lease (a higher
	// value) from the same one (D-443: logged, not a failure).
	if lft0 != lftForever {
		first := lft0
		for end := time.Now().Add(15 * time.Second); lft0+5 > first; time.Sleep(500 * time.Millisecond) {
			if time.Now().After(end) {
				t.Logf("valid_lft of %s did not count down by 5 s: %d s, then %d s", main, first, lft0)
				break
			}
			if lft0, ok = mainValidLft(t, dev, main); !ok {
				t.Fatalf("the main address %s left %s before the apply", main, dev)
			}
		}
	}
	apply(r)
	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), "plumb-ci-nic:") || !strings.Contains(string(b), "macaddress: "+mac) {
		t.Fatalf("file: %s %v", b, err)
	}
	waitLive("10.99.20.5/24", true)
	// The main address may be gone for a moment (mainAddrBound): wait for
	// it, and log how long it was missing.
	start := time.Now()
	lft1, ok := mainValidLft(t, dev, main)
	for ; !ok; lft1, ok = mainValidLft(t, dev, main) {
		if time.Since(start) > mainAddrBound {
			t.Fatalf("the main address %s of %s is gone or changed for more than %v (D-443)", main, dev, mainAddrBound)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if d := time.Since(start); d > 20*time.Millisecond {
		t.Logf("the main address of %s was back after %v", dev, d.Round(time.Millisecond))
	}
	// D-443: the same address within the bound is enough; a new lease is
	// only logged (valid_lft starts again at the full lease time).
	lease := "the same lease"
	if lft0 != lftForever && lft1 > lft0 {
		lease = "a new lease (D-443: accepted, same address, no gap)"
	}
	t.Logf("main address %s: valid_lft %d s before the apply, %d s after: %s", main, lft0, lft1, lease)
	if rs, _, _ := waitDefaultRoute(30 * time.Second); len(rs) == 0 || rs[0].Dev != dev {
		t.Fatalf("the default route moved: %+v", rs)
	}
	if ch := diff(r); len(ch) != 0 {
		t.Fatalf("after the apply: %+v", ch)
	}
	t.Logf("step 1 ok: %s (%s) has its address; %s and its default route unchanged", card, mac, dev)

	// 2. Drift: the address removed by hand.
	mustOut(t, "ip", "addr", "del", "10.99.20.5/24", "dev", card)
	if ch := diff(r); len(ch) != 1 || ch[0].Attr != "addresses" {
		t.Fatalf("drift: %+v", ch)
	}
	mustOut(t, "netplan", "apply")
	waitLive("10.99.20.5/24", true)
	t.Log("step 2 ok: a hand-removed address on the card is drift")

	// 3. A MAC that no card has: refused, nothing changed.
	before, _ := os.ReadFile(file)
	other := res("02:5a:10:20:30:4f", "10.99.21.5/24")
	if err := other.(resource.SafeApplier).Prepare(ctx); err == nil || !strings.Contains(err.Error(), "no network card of the VM has the MAC") {
		t.Fatalf("unknown MAC: %v", err)
	}
	if now, _ := os.ReadFile(file); string(now) != string(before) {
		t.Fatal("the file changed after a refused Prepare")
	}
	t.Log("step 3 ok: an unknown MAC is refused with nothing changed")

	// 4. The card leaves guest.nics: the render sends nic<N> absent
	// (review M1 of #336); the file goes, the address goes, the main NIC
	// stays.
	gone, err := resource.New(wire.Resource{ID: "network:ci-nic", Type: "network", Block: "guest.nics",
		Attrs: map[string]any{"name": "ci-nic", "state": "absent"}}, env)
	if err != nil {
		t.Fatal(err)
	}
	apply(gone)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the file stays after absent: %v", err)
	}
	waitLive("10.99.20.5/24", false)
	if rs, _, _ := waitDefaultRoute(30 * time.Second); len(rs) == 0 || rs[0].Dev != dev {
		t.Fatalf("the default route moved: %+v", rs)
	}
	if ch := diff(gone); len(ch) != 0 {
		t.Fatalf("after the removal: %+v", ch)
	}
	t.Log("step 4 ok: absent removes the file and the address of the card")
}

// cardOf returns the interface whose permanent (else current) MAC is mac.
func cardOf(t *testing.T, mac string) string {
	t.Helper()
	var links []struct {
		Ifname   string  `json:"ifname"`
		Address  string  `json:"address"`
		Permaddr *string `json:"permaddr"`
	}
	if err := json.Unmarshal(mustOut(t, "ip", "-j", "link", "show"), &links); err != nil {
		t.Fatal(err)
	}
	for _, l := range links {
		a := l.Address
		if l.Permaddr != nil {
			a = *l.Permaddr
		}
		if strings.EqualFold(a, mac) {
			return l.Ifname
		}
	}
	return ""
}

func mustOut(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return out
}
