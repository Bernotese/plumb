package agent

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/Bernotese/plumb/internal/wire"
)

// The address list of the guest (D-212, D-213). The agent reads it with
// two netlink dumps (links, then addresses), because only netlink gives
// the address flags and the link kind; net.InterfaceAddrs gives neither.
// The list is display data for the controller: it is never secret, but it
// is not logged either.

// Netlink values that package syscall does not define.
const (
	ifaFlags     = 8      // IFA_FLAGS: u32 address flags; IFA_F_* above 0xff live only here
	iflaInfoKind = 1      // IFLA_INFO_KIND inside IFLA_LINKINFO
	nlaTypeMask  = 0x3fff // drops NLA_F_NESTED and NLA_F_NET_BYTEORDER
)

// dropKinds are link kinds of tunnels, VPNs and container plumbing. Their
// addresses are not the guest's own address on its network (D-213).
// "tun" covers tap and Tailscale too.
var dropKinds = map[string]bool{
	"veth": true, "tun": true, "wireguard": true, "vxlan": true, "geneve": true, "dummy": true,
	"gre": true, "gretap": true, "ipip": true, "sit": true, "ip6gre": true, "ip6gretap": true, "ip6tnl": true,
}

// dropName matches plain bridges of Docker, Podman, LXD, Incus, CNI,
// flannel and libvirt. The guest's own bridges (br0) and bonds stay (D-213).
var dropName = regexp.MustCompile(`^(docker0|podman0|lxdbr0|incusbr0|br-[0-9a-f]{12}|cni.*|flannel.*|virbr.*)$`)

// nlLink is one interface of a link dump.
type nlLink struct {
	Index    int32
	Name     string
	Kind     string // IFLA_INFO_KIND; empty for a plain card
	Up       bool
	Loopback bool
}

// nlAddr is one address of an address dump.
type nlAddr struct {
	Index int32
	Addr  netip.Addr
	Flags uint32 // IFA_F_*, from IFA_FLAGS when present
}

// readAddrs reads the guest's address list with two netlink dumps: the
// sorted list, cut to wire.MaxAddrs, and whether it was cut (Options.Addrs).
func readAddrs() ([]string, bool, error) {
	lb, err := syscall.NetlinkRIB(syscall.RTM_GETLINK, syscall.AF_UNSPEC)
	if err != nil {
		return nil, false, fmt.Errorf("netlink link dump: %w", err)
	}
	ab, err := syscall.NetlinkRIB(syscall.RTM_GETADDR, syscall.AF_UNSPEC)
	if err != nil {
		return nil, false, fmt.Errorf("netlink address dump: %w", err)
	}
	links, err := parseLinks(lb)
	if err != nil {
		return nil, false, err
	}
	addrs, err := parseAddrs(ab)
	if err != nil {
		return nil, false, err
	}
	list, truncated := selectAddrs(links, addrs)
	return list, truncated, nil
}

// selectAddrs applies the rules of D-213 and returns the list in the form
// of wire.CheckAddrs.
//
// Interfaces: up, not loopback, kind not in dropKinds, name not dropName.
// When no interface is left (a container, whose eth0 is a veth), every up
// interface that is not loopback counts (T-038 makes the LXC rule final).
//
// Addresses: no loopback, link-local, multicast or unspecified address, and
// no tentative, deprecated or DAD-failed address; no IPv6 temporary
// (privacy) address. For IPv4 the bit of IFA_F_TEMPORARY means "secondary",
// which is a normal address.
func selectAddrs(links []nlLink, addrs []nlAddr) ([]string, bool) {
	up := map[int32]bool{}
	keep := map[int32]bool{}
	for _, l := range links {
		if !l.Up || l.Loopback {
			continue
		}
		up[l.Index] = true
		if !dropKinds[l.Kind] && !dropName.MatchString(l.Name) {
			keep[l.Index] = true
		}
	}
	if len(keep) == 0 {
		keep = up
	}
	var out []netip.Addr
	for _, a := range addrs {
		ip := a.Addr.Unmap()
		switch {
		case !keep[a.Index], !ip.IsValid(),
			ip.IsLoopback(), ip.IsLinkLocalUnicast(), ip.IsMulticast(), ip.IsUnspecified(),
			a.Flags&(syscall.IFA_F_TENTATIVE|syscall.IFA_F_DEPRECATED|syscall.IFA_F_DADFAILED) != 0,
			ip.Is6() && a.Flags&syscall.IFA_F_TEMPORARY != 0:
			continue
		}
		out = append(out, ip)
	}
	return wire.SortAddrs(out)
}

var errNetlink = errors.New("netlink: malformed message")

// parseLinks reads the RTM_NEWLINK messages of a link dump. Bad data is
// an error, never a panic: syscall.ParseNetlinkRouteAttr panics on an
// unaligned last attribute (review S1), and a panic would stop the agent.
func parseLinks(b []byte) (out []nlLink, err error) {
	defer func() {
		if recover() != nil {
			out, err = nil, errNetlink
		}
	}()
	msgs, err := syscall.ParseNetlinkMessage(b)
	if err != nil {
		return nil, fmt.Errorf("netlink link dump: %w", err)
	}
	for i := range msgs {
		m := &msgs[i]
		if m.Header.Type != syscall.RTM_NEWLINK {
			continue
		}
		if len(m.Data) < syscall.SizeofIfInfomsg {
			return nil, errNetlink
		}
		flags := binary.NativeEndian.Uint32(m.Data[8:12])
		l := nlLink{
			Index:    int32(binary.NativeEndian.Uint32(m.Data[4:8])),
			Up:       flags&syscall.IFF_UP != 0,
			Loopback: flags&syscall.IFF_LOOPBACK != 0,
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(m)
		if err != nil {
			return nil, fmt.Errorf("netlink link dump: %w", err)
		}
		for _, a := range attrs {
			switch a.Attr.Type & nlaTypeMask {
			case syscall.IFLA_IFNAME:
				l.Name = strings.TrimRight(string(a.Value), "\x00")
			case syscall.IFLA_LINKINFO:
				l.Kind = linkKind(a.Value)
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// linkKind returns IFLA_INFO_KIND from the nested IFLA_LINKINFO attributes.
func linkKind(b []byte) string {
	for len(b) >= syscall.SizeofRtAttr {
		n := int(binary.NativeEndian.Uint16(b[0:2]))
		typ := binary.NativeEndian.Uint16(b[2:4]) & nlaTypeMask
		if n < syscall.SizeofRtAttr || n > len(b) {
			return ""
		}
		if typ == iflaInfoKind {
			return strings.TrimRight(string(b[syscall.SizeofRtAttr:n]), "\x00")
		}
		next := (n + syscall.RTA_ALIGNTO - 1) &^ (syscall.RTA_ALIGNTO - 1)
		if next > len(b) {
			return ""
		}
		b = b[next:]
	}
	return ""
}

// parseAddrs reads the RTM_NEWADDR messages of an address dump. Bad data
// is an error, never a panic (see parseLinks).
func parseAddrs(b []byte) (out []nlAddr, err error) {
	defer func() {
		if recover() != nil {
			out, err = nil, errNetlink
		}
	}()
	msgs, err := syscall.ParseNetlinkMessage(b)
	if err != nil {
		return nil, fmt.Errorf("netlink address dump: %w", err)
	}
	for i := range msgs {
		m := &msgs[i]
		if m.Header.Type != syscall.RTM_NEWADDR {
			continue
		}
		if len(m.Data) < syscall.SizeofIfAddrmsg {
			return nil, errNetlink
		}
		a := nlAddr{
			Index: int32(binary.NativeEndian.Uint32(m.Data[4:8])),
			Flags: uint32(m.Data[2]),
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(m)
		if err != nil {
			return nil, fmt.Errorf("netlink address dump: %w", err)
		}
		var local, addr netip.Addr
		for _, at := range attrs {
			switch at.Attr.Type & nlaTypeMask {
			case syscall.IFA_LOCAL:
				local, _ = netip.AddrFromSlice(at.Value)
			case syscall.IFA_ADDRESS:
				addr, _ = netip.AddrFromSlice(at.Value)
			case ifaFlags:
				if len(at.Value) >= 4 {
					a.Flags = binary.NativeEndian.Uint32(at.Value[:4])
				}
			}
		}
		// IFA_LOCAL is the own address; IFA_ADDRESS is the peer on a
		// point-to-point link. Without IFA_LOCAL (IPv6) they are the same.
		a.Addr = local
		if !a.Addr.IsValid() {
			a.Addr = addr
		}
		out = append(out, a)
	}
	return out, nil
}

// addrList is a list that the agent sent or will send.
type addrList struct {
	List      []string
	Truncated bool
}

func (l addrList) equal(o addrList) bool {
	return l.Truncated == o.Truncated && slices.Equal(l.List, o.List)
}
