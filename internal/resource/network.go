package resource

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Resource network (D-332, D-348 … D-350; schema/network.json): extra
// addresses, routes, name servers and search domains of the main NIC of
// a VM. This file has the value rules that the server and the agent share.

// NetworkRoute is one route of a network resource.
type NetworkRoute struct {
	To     netip.Prefix
	Via    netip.Addr
	Metric int // 0: none given
}

// NetworkSpec is the parsed value of a network resource.
type NetworkSpec struct {
	Addresses   []netip.Prefix
	Routes      []NetworkRoute
	Nameservers []netip.Addr
	Search      []string
}

// Limits of the lists (the schema bounds the text; these bound the work).
const (
	maxNetworkAddresses = 64
	maxNetworkRoutes    = 64
	maxNetworkDNS       = 16
	maxNetworkSearch    = 16
	maxRouteMetric      = 1<<32 - 1
)

// usableAddr reports why a is not an address that a guest may own or
// route over; "" when it is.
func usableAddr(a netip.Addr) string {
	switch {
	case !a.IsValid():
		return "not an IP address"
	case a.Zone() != "":
		return "an address with a zone"
	case a.Is4In6():
		return "an IPv4-mapped IPv6 address (write IPv4 as a.b.c.d)"
	case a.IsUnspecified():
		return "the unspecified address"
	case a.IsLoopback():
		return "a loopback address"
	case a.IsMulticast():
		return "a multicast address"
	case a.IsLinkLocalUnicast():
		return "a link-local address"
	}
	return ""
}

// splitList splits a list-like string attribute; "" is the empty list.
func splitList(v, sep string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, sep)
}

// CheckNetworkAttrs parses the list attributes of a network resource
// (addresses, routes, nameservers, search) and checks every value. The
// errors name the attribute and the position of the element, never its
// value (D-325).
func CheckNetworkAttrs(attrs map[string]any) (NetworkSpec, error) {
	str := func(k string) (string, error) {
		v, ok := attrs[k]
		if !ok || v == nil {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("%s is not a string", k)
		}
		return s, nil
	}
	var s NetworkSpec
	bad := func(attr string, i int, why string) error {
		return fmt.Errorf("%s element %d: %s", attr, i+1, why)
	}

	v, err := str("addresses")
	if err != nil {
		return s, err
	}
	seen := map[netip.Addr]bool{}
	for i, e := range splitList(v, ",") {
		p, err := netip.ParsePrefix(e)
		if err != nil {
			return s, bad("addresses", i, "not an address with a prefix (a.b.c.d/n or x::y/n)")
		}
		if why := usableAddr(p.Addr()); why != "" {
			return s, bad("addresses", i, why)
		}
		if p.Bits() == 0 {
			return s, bad("addresses", i, "prefix length 0")
		}
		if seen[p.Addr()] {
			return s, bad("addresses", i, "the same address twice")
		}
		seen[p.Addr()] = true
		s.Addresses = append(s.Addresses, p)
	}
	if len(s.Addresses) > maxNetworkAddresses {
		return s, fmt.Errorf("addresses: more than %d", maxNetworkAddresses)
	}

	if v, err = str("routes"); err != nil {
		return s, err
	}
	seenTo := map[netip.Prefix]bool{}
	for i, e := range splitList(v, ";") {
		to, rest, ok := strings.Cut(e, " via ")
		if !ok {
			return s, bad("routes", i, "not '<cidr> via <ip>[ metric <n>]'")
		}
		via, metric, hasMetric := strings.Cut(rest, " metric ")
		var r NetworkRoute
		p, err := netip.ParsePrefix(to)
		if err != nil || p.Addr().Zone() != "" || p.Addr().Is4In6() {
			return s, bad("routes", i, "the destination is not a network (a.b.c.d/n or x::y/n)")
		}
		if p.Bits() == 0 {
			return s, bad("routes", i, "a default route (the gateway stays create-only)")
		}
		r.To = p.Masked()
		if r.Via, err = netip.ParseAddr(via); err != nil {
			return s, bad("routes", i, "the via address is not an IP address")
		}
		if why := usableAddr(r.Via); why != "" && !r.Via.IsLinkLocalUnicast() {
			return s, bad("routes", i, "the via address is "+why)
		}
		if r.Via.IsLinkLocalUnicast() && r.Via.Is4() {
			return s, bad("routes", i, "the via address is a link-local address")
		}
		if r.To.Addr().Is4() != r.Via.Is4() {
			return s, bad("routes", i, "the destination and the via address are of different IP versions")
		}
		if hasMetric {
			n, err := strconv.Atoi(metric)
			if err != nil || n < 1 || n > maxRouteMetric {
				return s, bad("routes", i, "the metric is not 1 … 4294967295")
			}
			r.Metric = n
		}
		if seenTo[r.To] {
			return s, bad("routes", i, "the same destination twice")
		}
		seenTo[r.To] = true
		s.Routes = append(s.Routes, r)
	}
	if len(s.Routes) > maxNetworkRoutes {
		return s, fmt.Errorf("routes: more than %d", maxNetworkRoutes)
	}

	if v, err = str("nameservers"); err != nil {
		return s, err
	}
	seenNS := map[netip.Addr]bool{}
	for i, e := range splitList(v, ",") {
		a, err := netip.ParseAddr(e)
		if err != nil {
			return s, bad("nameservers", i, "not an IP address")
		}
		if why := usableAddr(a); why != "" {
			return s, bad("nameservers", i, why)
		}
		if seenNS[a] {
			return s, bad("nameservers", i, "the same address twice")
		}
		seenNS[a] = true
		s.Nameservers = append(s.Nameservers, a)
	}
	if len(s.Nameservers) > maxNetworkDNS {
		return s, fmt.Errorf("nameservers: more than %d", maxNetworkDNS)
	}

	if v, err = str("search"); err != nil {
		return s, err
	}
	seenSearch := map[string]bool{}
	for i, e := range splitList(v, ",") {
		d := strings.ToLower(e)
		if e == "" || strings.Contains(d, "..") || len(d) > 253 {
			return s, bad("search", i, "not a domain")
		}
		if seenSearch[d] {
			return s, bad("search", i, "the same domain twice")
		}
		seenSearch[d] = true
		s.Search = append(s.Search, d)
	}
	if len(s.Search) > maxNetworkSearch {
		return s, fmt.Errorf("search: more than %d", maxNetworkSearch)
	}
	return s, nil
}
