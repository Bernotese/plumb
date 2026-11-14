package state

import (
	"fmt"
	"net/netip"
	"sort"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// Server checks of the network resource (D-332, D-348, review G1 of #244).
// The values are parsed by the rules the agent uses
// (resource.CheckNetworkAttrs). Addresses that the server knows only from
// outside the Set (controller, node, reported DHCP addresses) are checked
// in package plan.

// NetworkType is the resource type of the extra network settings.
const NetworkType = "network"

// TargetNetwork is what the network check knows of one Target.
type TargetNetwork struct {
	Target  string
	Kind    wire.Kind
	Main    netip.Addr // static main address; invalid for dhcp
	Gateway netip.Addr // static gateway; invalid when none
	Extras  []NetworkAddr
	// HasNetwork: the Target has a network resource (also state absent).
	HasNetwork bool
}

// NetworkAddr is one extra address of a network resource.
type NetworkAddr struct {
	Resource string // "network:<name>"
	Addr     netip.Prefix
}

// Networks returns the network facts of every Target of the set, sorted
// by name. A network value that does not parse is an error that names the
// Target and the resource.
func (s *Set) Networks() ([]TargetNetwork, error) {
	names := make([]string, 0, len(s.targets))
	for n := range s.targets {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]TargetNetwork, 0, len(names))
	for _, name := range names {
		t := s.targets[name]
		tn := TargetNetwork{Target: name, Kind: wire.Kind(t.Type)}
		if ip, _ := t.Guest["ip"].(string); ip != "" && ip != "dhcp" {
			if p, err := netip.ParsePrefix(ip); err == nil {
				tn.Main = p.Addr()
			}
		}
		if gw, _ := t.Guest["gateway"].(string); gw != "" {
			if a, err := netip.ParseAddr(gw); err == nil {
				tn.Gateway = a
			}
		}
		r, err := s.Render(name)
		if err != nil {
			return nil, err
		}
		for _, res := range r.Resources {
			if res.Type != NetworkType {
				continue
			}
			tn.HasNetwork = true
			if res.Attrs[wire.AttrState] == wire.StateAbsent {
				continue
			}
			spec, err := resource.CheckNetworkAttrs(res.Attrs)
			if err != nil {
				return nil, Errors{{Kind: KindTarget, Name: name, Path: "/spec/blocks", Msg: fmt.Sprintf("resource %s: %v", res.ID, err)}}
			}
			for _, a := range spec.Addresses {
				tn.Extras = append(tn.Extras, NetworkAddr{Resource: res.ID, Addr: a})
			}
		}
		out = append(out, tn)
	}
	return out, nil
}

// ReservedAddr is an address that no network resource may take, with what
// it is (for the error text).
type ReservedAddr struct {
	Addr netip.Addr
	What string
}

// CheckNetworks refuses, for each Target of touched (the render refuses
// network on an LXC already: its type is for VMs only): an extra address that is a reserved address, the main address
// or gateway of any Target, an extra address of another Target, or twice
// on this Target; and a static main address that is an extra address of
// another Target. The errors name the Targets, never the address (D-325).
func CheckNetworks(nets []TargetNetwork, touched []string, reserved []ReservedAddr) error {
	isTouched := map[string]bool{}
	for _, t := range touched {
		isTouched[t] = true
	}
	type owner struct{ target, what string }
	taken := map[netip.Addr][]owner{}
	for _, r := range reserved {
		if r.Addr.IsValid() {
			taken[r.Addr.Unmap()] = append(taken[r.Addr.Unmap()], owner{"", r.What})
		}
	}
	for _, n := range nets {
		if n.Main.IsValid() {
			taken[n.Main] = append(taken[n.Main], owner{n.Target, "the main address of Target " + n.Target})
		}
		if n.Gateway.IsValid() {
			taken[n.Gateway] = append(taken[n.Gateway], owner{n.Target, "the gateway of Target " + n.Target})
		}
	}
	extras := map[netip.Addr][]owner{}
	for _, n := range nets {
		for _, e := range n.Extras {
			extras[e.Addr.Addr()] = append(extras[e.Addr.Addr()], owner{n.Target, e.Resource})
		}
	}
	var errs Errors
	refuse := func(target, format string, a ...any) {
		errs = append(errs, &Error{Kind: KindTarget, Name: target, Path: "/spec/blocks", Msg: fmt.Sprintf(format, a...)})
	}
	for _, n := range nets {
		if !isTouched[n.Target] {
			continue
		}
		mine := map[netip.Addr]string{}
		for _, e := range n.Extras {
			a := e.Addr.Addr()
			if prev, ok := mine[a]; ok {
				refuse(n.Target, "resource %s: an address of %s again", e.Resource, prev)
				continue
			}
			mine[a] = e.Resource
			for _, o := range taken[a] {
				refuse(n.Target, "resource %s: an address is %s", e.Resource, o.what)
			}
			for _, o := range extras[a] {
				if o.target != n.Target {
					refuse(n.Target, "resource %s: an address is an extra address of Target %s (%s)", e.Resource, o.target, o.what)
				}
			}
		}
		if n.Main.IsValid() {
			for _, o := range extras[n.Main] {
				if o.target != n.Target {
					refuse(n.Target, "the main address is an extra address of Target %s (%s)", o.target, o.what)
				}
			}
		}
	}
	if len(errs) > 0 {
		return errs.sorted()
	}
	return nil
}
