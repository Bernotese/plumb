package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// A network resource with mac sets a more NIC N of a VM (slice 7, D-403,
// D-419): the render makes one per guest.nics item, with Plumb's MAC of
// that NIC (D-400). The agent finds the NIC by that MAC with the rule of
// D-370 (the permanent MAC when the kernel reports one, else the current
// one), never the main NIC, and writes its file under the netplan entry
// of that NIC: the one entry that names it (cloud-init's ethN), or, when
// none does, an own entry plumb-<name> that matches the MAC. No match, or
// more than one, refuses the step with nothing changed.

// nicKeyPrefix is the key of an own entry (no entry names the NIC).
const nicKeyPrefix = "plumb-"

// nicMode reports whether the resource sets a more NIC.
func (n *network) nicMode() bool { return n.spec.MAC != "" }

// ownNICKey is the key of the own entry of the resource.
func (n *network) ownNICKey() string { return nicKeyPrefix + n.a.Name }

// nicLink returns the interface whose MAC is the resource's MAC.
func (n *network) nicLink(ctx context.Context) (string, error) {
	out, err := n.run(ctx, "ip", "-j", "link", "show")
	if err != nil {
		return "", fmt.Errorf("ip link: %w", err)
	}
	var links []struct {
		Ifname   string  `json:"ifname"`
		Address  string  `json:"address"`
		Permaddr *string `json:"permaddr"`
		LinkType string  `json:"link_type"`
	}
	if err := json.Unmarshal(out, &links); err != nil {
		return "", fmt.Errorf("ip link: %w", err)
	}
	var devs []string
	for _, l := range links {
		if l.LinkType != "ether" || l.Ifname == "" {
			continue
		}
		address := l.Address
		if l.Permaddr != nil {
			address = *l.Permaddr // D-370: netplan matches the permanent MAC
		}
		mac, err := net.ParseMAC(address)
		if err != nil || len(mac) != 6 || bytes.Equal(mac, make([]byte, 6)) {
			continue
		}
		if mac.String() == n.spec.MAC {
			devs = append(devs, l.Ifname)
		}
	}
	switch len(devs) {
	case 0:
		return "", fmt.Errorf("no network card of the VM has the MAC %s (the NIC of %s is missing). Nothing was changed", n.spec.MAC, n.res.ID)
	case 1:
		return devs[0], nil
	}
	return "", fmt.Errorf("%d network cards of the VM have the MAC %s. Nothing was changed", len(devs), n.spec.MAC)
}

// nicKey returns the netplan key of the NIC dev with the MAC: the single
// entry that names it by key, set-name, match.name or match.macaddress
// (an entry that names it only by a glob such as en* refuses),
// or the own key when none does. own reports that the key is the own one
// (then the file holds the MAC match).
func (n *network) nicKey(view map[string]npView, dev string) (key string, own bool, err error) {
	var keys []string
	for k, v := range view {
		name, _ := v.Match["name"].(string)
		vmac, _ := v.Match["macaddress"].(string)
		exact := k == dev || v.SetName == dev || name == dev || strings.EqualFold(vmac, n.spec.MAC)
		// match.name may be a glob (en*): such an entry names the card
		// too, and maybe other cards: Plumb never writes under it (two
		// cards would share their values).
		if glob, _ := path.Match(name, dev); !exact && name != "" && glob {
			return "", false, fmt.Errorf("the netplan entry %s names the network card %s by the pattern %q, maybe with other cards: Plumb does not change it. Nothing was changed", k, dev, name)
		}
		if exact {
			keys = append(keys, k)
		}
	}
	switch len(keys) {
	case 0:
		return n.ownNICKey(), true, nil
	case 1:
		return keys[0], keys[0] == n.ownNICKey(), nil
	}
	slices.Sort(keys)
	return "", false, fmt.Errorf("netplan has %d entries for the network card %s (%s), want at most one. Nothing was changed", len(keys), dev, strings.Join(keys, ", "))
}

// nicPrepare is Prepare's check of a more NIC: the main NIC and its entry
// stay as they are; it returns the NIC, its key and the entry before.
func (n *network) nicPrepare(ctx context.Context, view map[string]npView, mainDev string, main netip.Addr) (dev, key string, own bool, mainKeyName string, err error) {
	mac, err := n.liveMAC(ctx, mainDev)
	if err != nil {
		return "", "", false, "", err
	}
	if mainKeyName, err = mainKey(view, mainDev, mac, main); err != nil {
		return "", "", false, "", err
	}
	if dev, err = n.nicLink(ctx); err != nil {
		return "", "", false, "", err
	}
	if dev == mainDev {
		return "", "", false, "", errors.New("the MAC is the main NIC's: a network card of guest.nics is never the main NIC. Nothing was changed")
	}
	if key, own, err = n.nicKey(view, dev); err != nil {
		return "", "", false, "", err
	}
	if key == mainKeyName {
		return "", "", false, "", errors.New("the netplan entry of the network card is the main NIC's. Nothing was changed")
	}
	if n.spec.Gateway.IsValid() {
		m := view[mainKeyName]
		if m.DHCP4 || m.DHCP6 || len(m.defaults()) > 0 {
			return "", "", false, "", errors.New("the main NIC has the default route: a gateway on another network card would add a second one. Nothing was changed")
		}
	}
	return dev, key, own, mainKeyName, nil
}

// nicUp brings the card up when it is administratively down (a card that
// no netplan entry names yet): the duplicate-address probe needs the link
// (the VM job of #336: "network is down"). It sets no address; netplan
// apply manages the card from then on.
func (n *network) nicUp(ctx context.Context, dev string) error {
	out, err := n.run(ctx, "ip", "-j", "link", "show", "dev", dev)
	if err != nil {
		return fmt.Errorf("ip link: %w", err)
	}
	var links []struct {
		Flags []string `json:"flags"`
	}
	if err := json.Unmarshal(out, &links); err != nil || len(links) != 1 {
		return fmt.Errorf("ip link: no single link %s", dev)
	}
	if slices.Contains(links[0].Flags, "UP") {
		return nil
	}
	if _, err := n.run(ctx, "ip", "link", "set", "dev", dev, "up"); err != nil {
		return fmt.Errorf("bring the network card %s up for the address probe: %w", dev, err)
	}
	return nil
}

// nicPreflight checks the merged result for a more NIC: the main entry
// keeps its addresses, default routes, match and set-name; the NIC entry
// keeps its match and set-name and has every value of the resource.
func nicPreflight(n *network, before, after map[string]npView, mainKeyName, key string, own bool) error {
	b, a := before[mainKeyName], after[mainKeyName]
	addrs := func(v npView) []string {
		var out []string
		for _, p := range v.addrs() {
			out = append(out, p.String())
		}
		slices.Sort(out)
		return out
	}
	switch {
	case !slices.Equal(addrs(b), addrs(a)):
		return errors.New("preflight: the addresses of the main NIC would change")
	case !slices.Equal(b.defaults(), a.defaults()):
		return errors.New("preflight: a default route of the main NIC would change")
	case fmt.Sprint(b.Match) != fmt.Sprint(a.Match) || b.SetName != a.SetName:
		return errors.New("preflight: match or set-name of the main NIC would change")
	}
	if n.a.State != "present" {
		return nil
	}
	nic, ok := after[key]
	if !ok {
		return fmt.Errorf("preflight: the entry %s of the network card is missing", key)
	}
	if prev, had := before[key]; had && !own && (fmt.Sprint(prev.Match) != fmt.Sprint(nic.Match) || prev.SetName != nic.SetName) {
		return errors.New("preflight: match or set-name of the network card would change")
	}
	if own {
		vmac, _ := nic.Match["macaddress"].(string)
		if !strings.EqualFold(vmac, n.spec.MAC) || nic.SetName != "" {
			return errors.New("preflight: the own entry of the network card does not match its MAC")
		}
	}
	if nic.DHCP4 != n.spec.DHCP {
		return errors.New("preflight: dhcp of the network card is not as desired in the merged netplan")
	}
	if n.spec.Gateway.IsValid() && !slices.ContainsFunc(nic.Routes, func(r npRoute) bool {
		via, err := netip.ParseAddr(r.Via)
		return r.To == "default" && err == nil && via == n.spec.Gateway
	}) {
		return errors.New("preflight: the gateway of the network card is missing in the merged netplan")
	}
	return nil
}

// nicLiveGateway reports whether dev has a default route via gw.
func nicLiveGateway(routes []liveRoute, gw netip.Addr) bool {
	return slices.ContainsFunc(routes, func(r liveRoute) bool {
		via, err := netip.ParseAddr(r.Gateway)
		return r.Dst == "default" && err == nil && via == gw
	})
}

// fileDHCP reads dhcp4 of the single entry of a file of ours.
func fileDHCP(content string) bool {
	var f npFile
	if yaml.Unmarshal([]byte(content), &f) != nil {
		return false
	}
	for _, e := range f.Network.Ethernets {
		return e.DHCP4 != nil && *e.DHCP4
	}
	return false
}
