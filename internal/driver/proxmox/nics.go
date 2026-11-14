package proxmox

// More NICs net1 … net3 (slice 7, D-399 … D-404, T-219). net0 stays the
// NIC of guest.bridge/ip/gateway with the MAC that Proxmox picks. Each more
// NIC has Plumb's MAC (driver.NICMAC, D-400), a bridge and an optional VLAN
// tag that the install named (D-401), and for a container the address in
// netN (D-403). A VM gets the address of a more NIC through the agent's
// network resource, so its netN holds no address.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// SetExtraNets gives the driver the extra nets of the install
// (PLUMB_PVE_EXTRA_NETS). serve calls it once, before the first Plan.
func (d *Driver) SetExtraNets(x InstallExtras) {
	d.nets = append([]ExtraNet(nil), x.ExtraNets...)
}

// NetAllowed: the token has SDN.Use on the bridge (untagged) or on the
// bridge/tag path. The main bridge of the install is allowed untagged;
// a tag on it needs its own --extra-vlan line.
func (d *Driver) NetAllowed(bridge string, tag int) bool {
	if tag == 0 && bridge == d.cfg.Bridge {
		return true
	}
	return InstallExtras{ExtraNets: d.nets}.NetAllowed(bridge, tag)
}

// NetworkNotAllowedError: a NIC uses a bridge or bridge/tag that the
// install did not name (D-401). The server refuses it before any Proxmox
// call; the driver refuses it again.
type NetworkNotAllowedError struct {
	NIC    int
	Bridge string
	Tag    int
}

func (e *NetworkNotAllowedError) Error() string {
	what := "bridge " + e.Bridge
	flag := "--extra-bridge " + e.Bridge
	if e.Tag != 0 {
		what = fmt.Sprintf("bridge %s tag %d", e.Bridge, e.Tag)
		flag = fmt.Sprintf("--extra-vlan %s:%d", e.Bridge, e.Tag)
	}
	return fmt.Sprintf("proxmox: net%d: %s is not named by the install; the token has no SDN.Use there. Run install.sh with %s (D-401)", e.NIC, what, flag)
}

var reNICMAC = regexp.MustCompile(`^02(:[0-9a-f]{2}){5}$`)

// checkNICNet checks the network of NIC idx; it needs no Proxmox call.
func (d *Driver) checkNICNet(idx int, n driver.NIC) error {
	if !reBridge.MatchString(n.Bridge) {
		return fmt.Errorf("proxmox: net%d: bad bridge %q", idx, n.Bridge)
	}
	if n.Tag < 0 || n.Tag > 4094 {
		return fmt.Errorf("proxmox: net%d: bad VLAN tag %d", idx, n.Tag)
	}
	if !d.NetAllowed(n.Bridge, n.Tag) {
		return &NetworkNotAllowedError{NIC: idx, Bridge: n.Bridge, Tag: n.Tag}
	}
	return nil
}

// checkNIC checks NIC idx (1 … MaxNICs) before it is written.
func (d *Driver) checkNIC(idx int, n driver.NIC, ct bool) error {
	if idx < 1 || idx > driver.MaxNICs {
		return fmt.Errorf("proxmox: NIC %d: at most %d more NICs (D-399)", idx, driver.MaxNICs)
	}
	if err := d.checkNICNet(idx, n); err != nil {
		return err
	}
	if !reNICMAC.MatchString(n.MAC) {
		return fmt.Errorf("proxmox: net%d: MAC %q is not Plumb's (02:…, D-400)", idx, n.MAC)
	}
	if !ct {
		if n.IP != "" || n.Gateway != "" {
			return fmt.Errorf("proxmox: net%d: a VM gets the address of a more NIC through the agent, not through Proxmox (D-403)", idx)
		}
		return nil
	}
	if n.IP != "" && n.IP != "dhcp" {
		addr, bits, ok := strings.Cut(n.IP, "/")
		b, err := strconv.Atoi(bits)
		if !ok || !reIPv4.MatchString(addr) || err != nil || b < 0 || b > 32 {
			return fmt.Errorf("proxmox: net%d: bad ip %q", idx, n.IP)
		}
	}
	if n.Gateway != "" && (!reIPv4.MatchString(n.Gateway) || n.IP == "" || n.IP == "dhcp") {
		return fmt.Errorf("proxmox: net%d: a gateway needs a static ip (%q)", idx, n.Gateway)
	}
	return nil
}

// checkNICs checks the NICs of a new guest: no gap, at most MaxNICs.
func (d *Driver) checkNICs(nics []driver.NIC, ct bool) error {
	if len(nics) > driver.MaxNICs {
		return fmt.Errorf("proxmox: %d more NICs; at most %d (D-399)", len(nics), driver.MaxNICs)
	}
	for i, n := range nics {
		if err := d.checkNIC(i+1, n, ct); err != nil {
			return err
		}
	}
	return nil
}

// vmNetValue is the netN value of a VM: virtio with Plumb's MAC.
func vmNetValue(n driver.NIC) string {
	v := "virtio=" + strings.ToUpper(n.MAC) + ",bridge=" + n.Bridge
	if n.Tag != 0 {
		v += ",tag=" + strconv.Itoa(n.Tag)
	}
	return v
}

// ctNetValue is the netN value of a container: device ethN, Plumb's MAC,
// and the address (none: up without an address).
func ctNetValue(idx int, n driver.NIC) string {
	v := "name=eth" + strconv.Itoa(idx) + ",bridge=" + n.Bridge + ",hwaddr=" + strings.ToUpper(n.MAC)
	if n.Tag != 0 {
		v += ",tag=" + strconv.Itoa(n.Tag)
	}
	if n.IP != "" {
		v += ",ip=" + n.IP
	}
	if n.Gateway != "" {
		v += ",gw=" + n.Gateway
	}
	return v
}

// addNICParams puts net1 … netN of a new guest into params.
func addNICParams(params url.Values, nics []driver.NIC, ct bool) {
	for i, n := range nics {
		if ct {
			params.Set("net"+strconv.Itoa(i+1), ctNetValue(i+1, n))
		} else {
			params.Set("net"+strconv.Itoa(i+1), vmNetValue(n))
		}
	}
}

// nicsOf reads net1 … net3 of a config. A missing netN is a gap; the list
// ends at the last NIC (nil without one).
func nicsOf(cfg map[string]string, ct bool) []driver.NIC {
	out := make([]driver.NIC, driver.MaxNICs)
	last := 0
	for i := 1; i <= driver.MaxNICs; i++ {
		v := cfg["net"+strconv.Itoa(i)]
		if v == "" {
			continue
		}
		p := parseProps(v)
		n := driver.NIC{Bridge: p["bridge"], MAC: net0MAC(v)}
		n.Tag, _ = strconv.Atoi(p["tag"])
		if ct {
			n.IP, n.Gateway = p["ip"], p["gw"]
		}
		out[i-1] = n
		last = i
	}
	if last == 0 {
		return nil
	}
	return out[:last]
}

// precheckNICChanges checks the network of every NIC change before the
// first Proxmox call (like the bridge rule of D-184).
func (d *Driver) precheckNICChanges(changes []wire.Change) error {
	for _, c := range changes {
		idx := driver.NICIndex(c.Attr)
		if idx == 0 {
			continue
		}
		n, err := driver.ParseNICValue(c.To)
		if err != nil {
			return err
		}
		if n.Bridge != "" {
			if err := d.checkNICNet(idx, n); err != nil {
				return err
			}
		}
	}
	return nil
}

// nicChange puts one nicN change into params: a new netN value, or netN
// into dels (removed). The MAC is the one of the change: Plumb's, the same
// on every update (D-400, D-402).
func (d *Driver) nicChange(params url.Values, dels *[]string, c wire.Change, ct bool) error {
	idx := driver.NICIndex(c.Attr)
	n, err := driver.ParseNICValue(c.To)
	if err != nil {
		return err
	}
	key := "net" + strconv.Itoa(idx)
	if n.Bridge == "" {
		*dels = append(*dels, key)
		return nil
	}
	if err := d.checkNIC(idx, n, ct); err != nil {
		return err
	}
	if ct {
		params.Set(key, ctNetValue(idx, n))
	} else {
		params.Set(key, vmNetValue(n))
	}
	return nil
}

// mergeDeletes adds keys to the delete parameter (one comma list).
func mergeDeletes(params url.Values, keys []string) {
	if len(keys) == 0 {
		return
	}
	if v := params.Get("delete"); v != "" {
		keys = append([]string{v}, keys...)
	}
	params.Set("delete", strings.Join(keys, ","))
}

// nicUpdateKeys are the more NICs that Update may write or delete.
var nicUpdateKeys = map[string]bool{"net1": true, "net2": true, "net3": true}

// checkUpdateNICs is the last guard of an update: delete only the tags and
// more NICs, and every netN with Plumb's MAC.
func checkUpdateNICs(params url.Values) error {
	if del, ok := params["delete"]; ok {
		if len(del) != 1 {
			return fmt.Errorf("proxmox: Update may delete only the tags and more NICs, not %q", del)
		}
		for _, k := range strings.Split(del[0], ",") {
			if k != "tags" && !nicUpdateKeys[k] {
				return fmt.Errorf("proxmox: Update may delete only the tags and more NICs, not %q", del)
			}
		}
	}
	for k := range nicUpdateKeys {
		v, ok := params[k]
		if !ok {
			continue
		}
		if len(v) != 1 || !reNICMAC.MatchString(strings.ToLower(net0MAC(v[0]))) {
			return fmt.Errorf("proxmox: %s must have Plumb's MAC (D-400)", k)
		}
	}
	return nil
}

// errNICNotApplied: a NIC write did not take effect in the running guest
// (for example a failed hot-plug, D-402). The next Plan converges.
var errNICNotApplied = errors.New("proxmox: a NIC change is not applied")

// checkNICsApplied reads the running config again after a write of NICs
// and checks each written or deleted netN (D-402: a failed hot-plug ends
// the Run with the config shown). read is the config read of the guest.
func checkNICsApplied(ctx context.Context, params url.Values, read func(context.Context) (map[string]string, error)) error {
	want := map[string]string{}
	for k := range nicUpdateKeys {
		if v := params.Get(k); v != "" {
			want[k] = v
		}
	}
	if del := params.Get("delete"); del != "" {
		for _, k := range strings.Split(del, ",") {
			if nicUpdateKeys[k] {
				want[k] = ""
			}
		}
	}
	if len(want) == 0 {
		return nil
	}
	cfg, err := read(ctx)
	if err != nil {
		return fmt.Errorf("proxmox: read the NICs back: %w", err)
	}
	var bad []string
	for _, k := range []string{"net1", "net2", "net3"} {
		w, ok := want[k]
		if !ok {
			continue
		}
		have := cfg[k]
		switch {
		case w == "" && have != "":
			bad = append(bad, fmt.Sprintf("%s is still %q (removal not applied)", k, have))
		case w != "" && !sameNIC(have, w):
			shown := have
			if shown == "" {
				shown = "missing"
			}
			bad = append(bad, fmt.Sprintf("%s is %s, want %q", k, strconv.Quote(shown), w))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("%w: %s; the guest runs with the old NICs, the next Plan converges", errNICNotApplied, strings.Join(bad, "; "))
	}
	return nil
}

// sameNIC compares a netN value that Proxmox shows with the written one:
// Proxmox adds properties (type=veth, firewall), so only the written keys
// count; the MAC without case.
func sameNIC(have, want string) bool {
	if have == "" {
		return false
	}
	ph := parseProps(have)
	for k, v := range parseProps(want) {
		hv, ok := ph[k]
		if !ok {
			return false
		}
		if k == "virtio" || k == "hwaddr" {
			if !strings.EqualFold(hv, v) {
				return false
			}
			continue
		}
		if hv != v {
			return false
		}
	}
	return true
}
