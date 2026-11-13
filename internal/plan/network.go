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
	"github.com/Bernotese/plumb/internal/store"
)

// The server refusals of the network resource (D-348, review G1 of #244)
// that need more than the Set: the controller and Proxmox node addresses
// (Config.ReservedAddrs) and the addresses that the agents of other
// Targets report (a DHCP Target has no main address in its document).

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
// (state.CheckNetworks) with the reserved addresses of the controller,
// the node and the reports of the other Targets' agents. It does no
// lookup when no touched Target has a network address.
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
	for _, n := range nets {
		if !extras {
			break // only extra addresses are compared with the reserved ones
		}
		r, known, err := e.st.ReportedAddrs(ctx, n.Target)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if !known {
			continue
		}
		for _, s := range r.List {
			p, err := netip.ParsePrefix(s)
			var a netip.Addr
			if err == nil {
				a = p.Addr()
			} else if a, err = netip.ParseAddr(s); err != nil {
				continue
			}
			if slices.ContainsFunc(n.Extras, func(x state.NetworkAddr) bool { return x.Addr.Addr() == a }) {
				continue // its own extra address: CheckNetworks names it better
			}
			reserved = append(reserved, state.ReservedAddr{Addr: a, What: reportedBy(n.Target)})
		}
	}
	// A touched Target's own reports are not reserved for itself: its
	// main address is guarded by the agent's check in a temporary root.
	var errs state.Errors
	for _, t := range touched {
		var mine []state.ReservedAddr
		for _, r := range reserved {
			if r.What != reportedBy(t) {
				mine = append(mine, r)
			}
		}
		if err := state.CheckNetworks(nets, []string{t}, mine); err != nil {
			var es state.Errors
			if errors.As(err, &es) {
				errs = append(errs, es...)
			} else {
				return err
			}
		}
	}
	if len(errs) > 0 {
		return &ProposalError{Msg: "the documents are not valid:\n" + errs.Error(), Err: errs}
	}
	return nil
}

func reportedBy(target string) string {
	return "an address that the agent of Target " + target + " reports"
}
