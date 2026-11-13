package plan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"time"

	"github.com/Bernotese/plumb/internal/state"
)

// The server refusals of the network resource (D-348, review G1 of #244)
// that need more than the Set: the controller and Proxmox node addresses
// (Config.ReservedAddrs). A DHCP Target has no main address in its
// document; its reported list is display data only (D-216), so it is not
// used here: the agent's duplicate-address probe covers that address.

// ReservedFunc returns the addresses that no network resource may take.
type ReservedFunc func(ctx context.Context) ([]state.ReservedAddr, error)

// resolveTimeout bounds the name lookups of one check.
const resolveTimeout = 5 * time.Second

// HostAddrs returns a ReservedFunc for the controller host names or IPs
// (the --hosts list; the agent URL uses the first) and the Proxmox URL
// (empty: none, the fake driver). Names are resolved at every check, so a
// changed DNS entry counts. A name that does not resolve fails the check
// (fail closed): a network proposal is then refused, not let through.
func HostAddrs(controller []string, nodeURL string) ReservedFunc {
	type host struct{ name, what string }
	var hosts []host
	for _, h := range controller {
		hosts = append(hosts, host{h, "the controller address"})
	}
	var bad error
	if nodeURL != "" {
		u, err := url.Parse(nodeURL)
		if err != nil || u.Hostname() == "" {
			bad = errors.New("the Proxmox URL has no host")
		} else {
			hosts = append(hosts, host{u.Hostname(), "the Proxmox node address"})
		}
	}
	return func(ctx context.Context) ([]state.ReservedAddr, error) {
		if bad != nil {
			return nil, bad
		}
		ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
		defer cancel()
		var out []state.ReservedAddr
		for _, h := range hosts {
			if a, err := netip.ParseAddr(h.name); err == nil {
				out = append(out, state.ReservedAddr{Addr: a.Unmap(), What: h.what})
				continue
			}
			addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", h.name)
			if err != nil || len(addrs) == 0 {
				return nil, fmt.Errorf("cannot resolve %s (%s)", h.what, h.name)
			}
			for _, a := range addrs {
				out = append(out, state.ReservedAddr{Addr: a.Unmap(), What: h.what})
			}
		}
		return out, nil
	}
}

// checkNetworks refuses network values of the touched Targets of next
// (state.CheckNetworks) with the reserved addresses of the controller and
// the node. It does no lookup when no touched Target has an extra address.
func (e *Engine) checkNetworks(ctx context.Context, next *state.Set, touched []string) error {
	nets, err := next.Networks()
	if err != nil {
		return &ProposalError{Msg: "the documents are not valid:\n" + err.Error(), Err: err}
	}
	need, extras := false, false
	for _, n := range nets {
		if slices.Contains(touched, n.Target) {
			need = need || n.HasNetwork || n.Main.IsValid()
			extras = extras || len(n.Extras) > 0
		}
	}
	if !need {
		return nil
	}
	var reserved []state.ReservedAddr
	if extras && e.cfg.ReservedAddrs != nil {
		if reserved, err = e.cfg.ReservedAddrs(ctx); err != nil {
			return &ProposalError{Msg: "the network addresses cannot be checked: " + err.Error()}
		}
	}
	if err := state.CheckNetworks(nets, touched, reserved); err != nil {
		return &ProposalError{Msg: "the documents are not valid:\n" + err.Error(), Err: err}
	}
	return nil
}
