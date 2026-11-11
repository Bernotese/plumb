package wire

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
)

// MaxAddrs is the longest address list an agent sends (D-212).
const MaxAddrs = 16

// Errors of CheckAddrs. They never hold an address: a list comes from the
// guest and is untrusted, and addresses do not belong in logs.
var (
	ErrAddrsTooMany  = errors.New("addrs: more than 16 addresses")
	ErrAddrsForm     = errors.New("addrs: not a canonical IPv4 or IPv6 address")
	ErrAddrsKind     = errors.New("addrs: loopback, link-local, multicast or unspecified address")
	ErrAddrsOrder    = errors.New("addrs: not sorted")
	ErrAddrsDup      = errors.New("addrs: duplicate address")
	ErrAddrsTruncate = errors.New("addrs: truncated list is shorter than 16")
)

// CheckAddrs reports whether list is a valid address list (D-212): at most
// MaxAddrs entries; each one the canonical text of a netip.Addr (no zone,
// no prefix length, no IPv4-mapped IPv6); no loopback, link-local,
// multicast or unspecified address; sorted by AddrLess; no duplicate. A
// truncated list holds exactly MaxAddrs entries. The error names the
// position, never the value.
func CheckAddrs(list []string, truncated bool) error {
	if len(list) > MaxAddrs {
		return ErrAddrsTooMany
	}
	if truncated && len(list) != MaxAddrs {
		return ErrAddrsTruncate
	}
	var prev netip.Addr
	for i, s := range list {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" || a.Is4In6() || a.String() != s {
			return fmt.Errorf("%w (entry %d)", ErrAddrsForm, i)
		}
		if a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
			a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
			return fmt.Errorf("%w (entry %d)", ErrAddrsKind, i)
		}
		if i > 0 {
			switch c := a.Compare(prev); {
			case c == 0:
				return fmt.Errorf("%w (entry %d)", ErrAddrsDup, i)
			case c < 0:
				return fmt.Errorf("%w (entry %d)", ErrAddrsOrder, i)
			}
		}
		prev = a
	}
	return nil
}

// SortAddrs sorts addresses in the order of CheckAddrs (IPv4 first, then
// IPv6, each by value), drops duplicates and cuts the list to MaxAddrs. It
// reports whether it cut. It unmaps IPv4-mapped IPv6 and drops zones. The
// caller filters the kinds first. The list is never nil.
func SortAddrs(addrs []netip.Addr) (list []string, truncated bool) {
	addrs = slices.Clone(addrs)
	for i, a := range addrs {
		addrs[i] = a.Unmap().WithZone("")
	}
	slices.SortFunc(addrs, netip.Addr.Compare)
	addrs = slices.Compact(addrs)
	if len(addrs) > MaxAddrs {
		addrs, truncated = addrs[:MaxAddrs], true
	}
	list = make([]string, len(addrs))
	for i, a := range addrs {
		list[i] = a.String()
	}
	return list, truncated
}
