package state

import (
	"fmt"
	"strconv"

	"github.com/Bernotese/plumb/internal/wire"
)

// The guest-side settings of the more NICs of a VM (slice 7, T-220,
// D-403, D-419). guest.nics is a Proxmox setting (the driver makes the
// cards, D-400); the address of card N in a VM is set by the agent's
// network resource, found by Plumb's MAC of that card. The render cannot
// know that MAC (it needs the install id), so the engine calls
// AddNICNetworks with the MAC function after each render.

// NICBlock is the Block field of the network resources of guest.nics: no
// Block use id has a ".".
const NICBlock = "guest.nics"

// NICNetworkName is the name of the network resource of card n (1 … 3).
func NICNetworkName(n int) string { return "nic" + strconv.Itoa(n) }

// AddNICNetworks appends one network resource per item of guest.nics of
// a VM: nic<N> with Plumb's MAC of card N, and its address: dhcp, a
// static address with an optional gateway, or none (the card is up
// without an address). Every card gets its resource, also one without an
// address, so a card that comes back with the same MAC never keeps an
// old file. For the indexes after the last card up to absentTo it
// appends nic<N> with state absent and no mac (review M1 of #336): the
// file of a removed card goes, also in a guest whose agent was offline at
// the removal (the absent stays in the desired list). The engine passes
// absentTo only for an agent that knows the network type: an agent
// without it never wrote such a file. A container gets none: Proxmox
// sets its address (D-403). A Block network resource with such a name is
// an error.
func (r *Rendered) AddNICNetworks(mac func(n int) string, absentTo int) error {
	if r.Kind != wire.KindVM {
		return nil
	}
	list, _ := r.Guest.Attrs["nics"].([]any)
	clash := func(n int) error {
		name := NICNetworkName(n)
		id := wire.ID(NetworkType, name)
		for _, res := range r.Resources {
			if res.ID == id {
				return Errors{{Kind: KindTarget, Name: r.Target, Path: "/spec/blocks",
					Msg: fmt.Sprintf("resource %s: the name %s is the network card %d of guest.nics; name the Block's network resource otherwise", id, name, n)}}
			}
		}
		return nil
	}
	for n := len(list) + 1; n <= absentTo; n++ {
		if err := clash(n); err != nil {
			return err
		}
	}
	for i, item := range list {
		n := i + 1
		m, _ := item.(map[string]any)
		name := NICNetworkName(n)
		id := wire.ID(NetworkType, name)
		if err := clash(n); err != nil {
			return err
		}
		attrs := map[string]any{"name": name, "mac": mac(n), "dhcp": false}
		switch ip, _ := m["ip"].(string); ip {
		case "":
		case "dhcp":
			attrs["dhcp"] = true
		default:
			attrs["addresses"] = ip
			if gw, _ := m["gateway"].(string); gw != "" {
				attrs["gateway"] = gw
			}
		}
		r.Resources = append(r.Resources, wire.Resource{ID: id, Type: NetworkType, Block: NICBlock, Attrs: attrs})
	}
	for n := len(list) + 1; n <= absentTo; n++ {
		name := NICNetworkName(n)
		r.Resources = append(r.Resources, wire.Resource{ID: wire.ID(NetworkType, name), Type: NetworkType, Block: NICBlock,
			Attrs: map[string]any{"name": name, wire.AttrState: wire.StateAbsent}})
	}
	return nil
}
