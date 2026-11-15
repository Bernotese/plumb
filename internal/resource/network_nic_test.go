package resource

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

const nicMAC = "02:5a:10:20:30:40"

// T-220 (D-403, D-419): the values of a network resource of a more NIC.
func TestCheckNICAttrs(t *testing.T) {
	ok := []map[string]any{
		{"mac": nicMAC},
		{"mac": nicMAC, "dhcp": true},
		{"mac": nicMAC, "addresses": "10.9.20.5/24", "gateway": "10.9.20.1"},
		{"mac": nicMAC, "dhcp": false, "addresses": "10.9.20.5/24"},
	}
	for _, a := range ok {
		s, err := CheckNetworkAttrs(a)
		if err != nil || s.MAC != nicMAC {
			t.Errorf("%v: %v", a, err)
		}
	}
	for name, a := range map[string]map[string]any{
		"upper case mac":        {"mac": strings.ToUpper(nicMAC)},
		"not plumb's mac":       {"mac": "bc:24:11:00:00:01"},
		"multicast bit":         {"mac": "03:5a:10:20:30:40"},
		"zero rest":             {"mac": "02:00:00:00:00:00"},
		"eui64":                 {"mac": "02:5a:10:20:30:40:50:60"},
		"mac not a string":      {"mac": 2},
		"dhcp without mac":      {"dhcp": true},
		"gateway without mac":   {"addresses": "10.9.20.5/24", "gateway": "10.9.20.1"},
		"gateway with dhcp":     {"mac": nicMAC, "dhcp": true, "gateway": "10.9.20.1"},
		"gateway off the net":   {"mac": nicMAC, "addresses": "10.9.20.5/24", "gateway": "10.9.21.1"},
		"gateway no address":    {"mac": nicMAC, "gateway": "10.9.20.1"},
		"gateway loopback":      {"mac": nicMAC, "addresses": "10.9.20.5/24", "gateway": "127.0.0.1"},
		"dhcp not a bool":       {"mac": nicMAC, "dhcp": "yes"},
		"gateway not an IP":     {"mac": nicMAC, "addresses": "10.9.20.5/24", "gateway": "x"},
		"default route in list": {"mac": nicMAC, "addresses": "10.9.20.5/24", "routes": "0.0.0.0/0 via 10.9.20.1"},
	} {
		if _, err := CheckNetworkAttrs(a); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The main NIC is unchanged: no mac, no dhcp, no gateway.
	if s, err := CheckNetworkAttrs(map[string]any{"addresses": "192.0.2.10/24"}); err != nil || s.MAC != "" || s.DHCP || s.Gateway.IsValid() {
		t.Fatalf("main: %+v %v", s, err)
	}
}

func nicRes(t *testing.T, env Env, attrs map[string]any) *network {
	t.Helper()
	env = unitEnv(t, env)
	attrs["name"] = "nic1"
	r, err := New(wire.Resource{ID: "network:nic1", Type: "network", Attrs: attrs}, env)
	if err != nil {
		t.Fatal(err)
	}
	return r.(*network)
}

// The file of a more NIC: dhcp4 always written; the MAC match only in the
// own entry; the gateway as a default route. The main NIC's file has none
// of them (its form is unchanged).
func TestNICFileContent(t *testing.T) {
	n := nicRes(t, Env{}, map[string]any{"mac": nicMAC, "addresses": "10.9.20.5/24", "gateway": "10.9.20.1"})
	own, err := n.fileContent("plumb-nic1", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"plumb-nic1:", "match:", "macaddress: " + nicMAC, "dhcp4: false", "10.9.20.5/24", "to: default", "via: 10.9.20.1"} {
		if !strings.Contains(own, want) {
			t.Errorf("own file lacks %q:\n%s", want, own)
		}
	}
	if strings.Contains(own, "set-name") {
		t.Errorf("own file renames the card:\n%s", own)
	}
	other, _ := n.fileContent("eth1", false)
	if strings.Contains(other, "match") || !strings.Contains(other, "eth1:") || !strings.Contains(other, "dhcp4: false") {
		t.Errorf("file under cloud-init's key:\n%s", other)
	}
	dhcp := nicRes(t, Env{}, map[string]any{"mac": nicMAC, "dhcp": true})
	if c, _ := dhcp.fileContent("eth1", false); !strings.Contains(c, "dhcp4: true") {
		t.Errorf("dhcp file:\n%s", c)
	}
	main := netRes(t, unitEnv(t, Env{}), map[string]any{"addresses": "192.0.2.10/24"})
	if c, _ := main.fileContent("eth0", false); strings.Contains(c, "dhcp4") || strings.Contains(c, "match") {
		t.Errorf("the main NIC's file changed form:\n%s", c)
	}
}

// unitEnv is a full Env for the unit tests, with env's runner (none: every
// call fails).
func unitEnv(t *testing.T, env Env) Env {
	run := env.Run
	if run == nil {
		run = linkRunner(func(context.Context, string, ...string) ([]byte, error) {
			return nil, &ExitError{Name: "none", Code: 1}
		})
	}
	return NewEnv(t.TempDir(), run)
}

type linkRunner func(context.Context, string, ...string) ([]byte, error)

func (f linkRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

// The card is found by Plumb's MAC with the D-370 rule: the permanent MAC
// when the kernel reports one. No card, or two, is refused.
func TestNICLink(t *testing.T) {
	for _, tc := range []struct{ name, links, want string }{
		{"current", `[{"ifname":"eth0","address":"bc:24:11:00:00:01","link_type":"ether"},{"ifname":"ens19","address":"02:5A:10:20:30:40","link_type":"ether"}]`, "ens19"},
		{"permanent wins", `[{"ifname":"ens19","address":"02:00:00:00:00:99","permaddr":"02:5a:10:20:30:40","link_type":"ether"}]`, "ens19"},
		{"current is ours, permanent not", `[{"ifname":"ens19","address":"02:5a:10:20:30:40","permaddr":"02:00:00:00:00:99","link_type":"ether"}]`, ""},
		{"not ether", `[{"ifname":"br0","address":"02:5a:10:20:30:40","link_type":"none"}]`, ""},
		{"none", `[{"ifname":"eth0","address":"bc:24:11:00:00:01","link_type":"ether"}]`, ""},
		{"two", `[{"ifname":"a","address":"02:5a:10:20:30:40","link_type":"ether"},{"ifname":"b","address":"02:5a:10:20:30:40","link_type":"ether"}]`, ""},
		{"bad JSON", `{`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := Env{Run: linkRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "ip" || strings.Join(args, " ") != "-j link show" {
					t.Fatalf("call %s %v", name, args)
				}
				return []byte(tc.links), nil
			})}
			n := nicRes(t, env, map[string]any{"mac": nicMAC})
			dev, err := n.nicLink(context.Background())
			if tc.want == "" {
				if err == nil {
					t.Fatalf("found %q", dev)
				}
				return
			}
			if err != nil || dev != tc.want {
				t.Fatalf("got %q %v", dev, err)
			}
		})
	}
}

// The key: the single entry that names the card (key, set-name,
// match.name, match.macaddress), else the own key; two entries refuse.
func TestNICKey(t *testing.T) {
	n := nicRes(t, Env{}, map[string]any{"mac": nicMAC})
	eth0 := npView{Match: map[string]any{"macaddress": "bc:24:11:00:00:01"}, SetName: "eth0"}
	for _, tc := range []struct {
		name string
		view map[string]npView
		key  string
		own  bool
	}{
		{"none", map[string]npView{"eth0": eth0}, "plumb-nic1", true},
		{"cloud-init by MAC", map[string]npView{"eth0": eth0, "eth1": {Match: map[string]any{"macaddress": strings.ToUpper(nicMAC)}, SetName: "eth1"}}, "eth1", false},
		{"by set-name", map[string]npView{"eth0": eth0, "x": {SetName: "ens19"}}, "x", false},
		{"by key", map[string]npView{"eth0": eth0, "ens19": {}}, "ens19", false},
		{"own from before", map[string]npView{"eth0": eth0, "plumb-nic1": {Match: map[string]any{"macaddress": nicMAC}}}, "plumb-nic1", true},
	} {
		key, own, err := n.nicKey(tc.view, "ens19")
		if err != nil || key != tc.key || own != tc.own {
			t.Errorf("%s: %q %v %v", tc.name, key, own, err)
		}
	}
	// A glob of cloud-init that names the card refuses: its entry may hold
	// other cards, which would share the values.
	if _, _, err := n.nicKey(map[string]npView{"eth0": eth0, "all-en": {Match: map[string]any{"name": "ens*"}}}, "ens19"); err == nil || !strings.Contains(err.Error(), "by the pattern") {
		t.Errorf("glob: %v", err)
	}
	if key, _, err := n.nicKey(map[string]npView{"eth0": eth0, "all-en": {Match: map[string]any{"name": "enp*"}}}, "ens19"); err != nil || key != "plumb-nic1" {
		t.Errorf("a glob that does not match: %q %v", key, err)
	}
	two := map[string]npView{"eth1": {Match: map[string]any{"macaddress": nicMAC}}, "ens19": {}}
	if _, _, err := n.nicKey(two, "ens19"); err == nil {
		t.Fatal("two entries for one card accepted")
	}
}

// The preflight of a more NIC: the main entry stays; the card's entry has
// its values and keeps cloud-init's match.
func TestNICPreflight(t *testing.T) {
	n := nicRes(t, Env{}, map[string]any{"mac": nicMAC, "addresses": "10.9.20.5/24", "gateway": "10.9.20.1"})
	mainV := npView{Match: map[string]any{"macaddress": "bc:24:11:00:00:01"}, SetName: "eth0", Addresses: []any{"198.51.100.5/24"}}
	nicOK := npView{Match: map[string]any{"macaddress": nicMAC}, Addresses: []any{"10.9.20.5/24"}, Routes: []npRoute{{To: "default", Via: "10.9.20.1"}}}
	before := map[string]npView{"eth0": mainV}
	if err := nicPreflight(n, before, map[string]npView{"eth0": mainV, "plumb-nic1": nicOK}, "eth0", "plumb-nic1", true); err != nil {
		t.Fatalf("good: %v", err)
	}
	moved := mainV
	moved.Addresses = []any{"198.51.100.6/24"}
	noGw := nicOK
	noGw.Routes = nil
	dhcpOn := nicOK
	dhcpOn.DHCP4 = true
	otherMatch := nicOK
	otherMatch.Match = map[string]any{"macaddress": "02:00:00:00:00:01"}
	mainDefault := mainV
	mainDefault.Routes = []npRoute{{To: "default", Via: "198.51.100.1"}}
	for name, after := range map[string]map[string]npView{
		"main address":   {"eth0": moved, "plumb-nic1": nicOK},
		"main default":   {"eth0": mainDefault, "plumb-nic1": nicOK},
		"no gateway":     {"eth0": mainV, "plumb-nic1": noGw},
		"dhcp on":        {"eth0": mainV, "plumb-nic1": dhcpOn},
		"own match":      {"eth0": mainV, "plumb-nic1": otherMatch},
		"entry missing":  {"eth0": mainV},
		"main entry set": {"eth0": {Match: mainV.Match, SetName: "ens18", Addresses: mainV.Addresses}, "plumb-nic1": nicOK},
	} {
		if err := nicPreflight(n, before, after, "eth0", "plumb-nic1", true); err == nil {
			t.Errorf("%s: passed", name)
		}
	}
	// cloud-init's entry keeps its match and set-name.
	ci := npView{Match: map[string]any{"macaddress": nicMAC}, SetName: "eth1"}
	b2 := map[string]npView{"eth0": mainV, "eth1": ci}
	renamed := ci
	renamed.SetName = "eth9"
	renamed.Addresses, renamed.Routes = nicOK.Addresses, nicOK.Routes
	if err := nicPreflight(n, b2, map[string]npView{"eth0": mainV, "eth1": renamed}, "eth0", "eth1", false); err == nil {
		t.Error("a changed set-name of cloud-init's entry passed")
	}
}

// Observe of a more NIC reads dhcp from the file and the gateway live.
func TestNICLiveGateway(t *testing.T) {
	gw := netip.MustParseAddr("10.9.20.1")
	if !nicLiveGateway([]liveRoute{{Dst: "default", Gateway: "10.9.20.1"}}, gw) {
		t.Fatal("live default route missed")
	}
	if nicLiveGateway([]liveRoute{{Dst: "10.0.0.0/8", Gateway: "10.9.20.1"}}, gw) {
		t.Fatal("a non-default route counted")
	}
	n := nicRes(t, Env{}, map[string]any{"mac": nicMAC, "dhcp": true})
	c, _ := n.fileContent("eth1", false)
	if !fileDHCP(c) {
		t.Fatal("dhcp from the file")
	}
	// The observed attrs of a more NIC hold dhcp and gateway.
	b, _ := json.Marshal(n.spec.attrs())
	if !strings.Contains(string(b), `"dhcp":"true"`) || !strings.Contains(string(b), `"gateway":""`) {
		t.Fatalf("attrs %s", b)
	}
}
