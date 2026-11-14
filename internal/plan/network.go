package plan

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
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
		// The controller's own default gateways (n3 of #249): usually the
		// LAN router, the worst address for a guest to take.
		gws, err := defaultGateways()
		if err != nil {
			return nil, fmt.Errorf("cannot read the controller's default route: %w", err)
		}
		var out []state.ReservedAddr
		for _, g := range gws {
			out = append(out, state.ReservedAddr{Addr: g, What: "the controller's gateway"})
		}
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
// notes are the display-only lines for the Plan.
func (e *Engine) checkNetworks(ctx context.Context, next *state.Set, touched []string) (notes []string, err error) {
	nets, err := next.Networks()
	if err != nil {
		return nil, &ProposalError{Msg: "the documents are not valid:\n" + err.Error(), Err: err}
	}
	need, extras := false, false
	for _, n := range nets {
		if slices.Contains(touched, n.Target) {
			need = need || n.HasNetwork || n.Main.IsValid()
			extras = extras || len(n.Extras) > 0
			// Point 4 of the review of #249 (D-356): display only.
			if len(n.Extras) > 0 && !n.Main.IsValid() {
				notes = append(notes, "Target "+n.Target+" gets its main address by DHCP: the server cannot check the "+
					"network addresses against it (D-356); the agent probes each new address on the link before the change.")
			}
		}
	}
	if !need {
		return notes, nil
	}
	var reserved []state.ReservedAddr
	if extras && e.cfg.ReservedAddrs != nil {
		if reserved, err = e.cfg.ReservedAddrs(ctx); err != nil {
			return nil, &ProposalError{Msg: "the network addresses cannot be checked: " + err.Error()}
		}
	}
	if err := state.CheckNetworks(nets, touched, reserved); err != nil {
		return nil, &ProposalError{Msg: "the documents are not valid:\n" + err.Error(), Err: err}
	}
	return notes, nil
}

// The route tables of the controller; tests point them at files.
var (
	procRoute  = "/proc/net/route"
	procRoute6 = "/proc/net/ipv6_route"
)

// defaultGateways returns the gateways of the default routes of the
// controller. The IPv4 table must be readable (fail closed); a missing
// IPv6 table means IPv6 is off.
func defaultGateways() ([]netip.Addr, error) {
	var out []netip.Addr
	b, err := os.ReadFile(procRoute)
	if err != nil {
		return nil, err
	}
	for i, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue // the header, a short line, or not a default route
		}
		flags, err1 := strconv.ParseUint(f[3], 16, 32)
		gw, err2 := strconv.ParseUint(f[2], 16, 32)
		if err1 != nil || err2 != nil || flags&0x2 == 0 || gw == 0 { // RTF_GATEWAY
			continue
		}
		// The kernel prints the address in host order (little-endian).
		a := netip.AddrFrom4([4]byte{byte(gw), byte(gw >> 8), byte(gw >> 16), byte(gw >> 24)})
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	b, err = os.ReadFile(procRoute6)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[0] != strings.Repeat("0", 32) || f[1] != "00" || len(f[4]) != 32 {
			continue
		}
		raw, err := hex.DecodeString(f[4])
		if err != nil {
			continue
		}
		a := netip.AddrFrom16([16]byte(raw))
		if !a.IsUnspecified() && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out, nil
}
