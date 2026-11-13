package resource

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// Duplicate-address probe of the network resource (D-348, review G1 of
// #244): before any change the agent asks the link whether another host
// uses a new address. IPv4: RFC 5227 ARP probes (sender IP 0.0.0.0).
// IPv6: a neighbor solicitation for the address (RFC 4862 §5.4; sent from
// the link-local address that the kernel picks, so a neighbor answers
// with an advertisement). Every wait is bounded: a silent link means "free"
// after probeListen, never a hung apply.

// Timing of one probe. The whole probe of one address ends after
// probeListen; a context deadline ends it earlier.
const (
	probeCount   = 2
	probeSpacing = 400 * time.Millisecond
	probeListen  = 1500 * time.Millisecond
	probeRead    = 100 * time.Millisecond // one blocking read
)

// errAddrInUse is returned when another host answers for the address.
var errAddrInUse = errors.New("another host on the link uses the address")

// ARP constants (RFC 826).
const (
	arpLen        = 28
	ethPARP       = 0x0806
	ethPIPv4      = 0x0800
	arpOpRequest  = 1
	arpOpReply    = 2
	icmp6NS       = 135
	icmp6NA       = 136
	icmp6NALen    = 24 // type, code, checksum, flags, target
	ipv6MCastHops = 18 // IPV6_MULTICAST_HOPS
	ipv6MCastIf   = 17 // IPV6_MULTICAST_IF
)

// arpPacket is the part of an ARP packet that the probe reads.
type arpPacket struct {
	op       uint16
	sha      net.HardwareAddr
	spa, tpa netip.Addr
}

// parseARP reads an Ethernet/IPv4 ARP packet (without the link header).
// ok is false for anything else or a short packet; it never panics.
func parseARP(b []byte) (p arpPacket, ok bool) {
	if len(b) < arpLen {
		return p, false
	}
	if binary.BigEndian.Uint16(b[0:2]) != 1 || binary.BigEndian.Uint16(b[2:4]) != ethPIPv4 || b[4] != 6 || b[5] != 4 {
		return p, false
	}
	p.op = binary.BigEndian.Uint16(b[6:8])
	p.sha = net.HardwareAddr(append([]byte(nil), b[8:14]...))
	p.spa = netip.AddrFrom4([4]byte(b[14:18]))
	p.tpa = netip.AddrFrom4([4]byte(b[24:28]))
	return p, true
}

// arpConflict reports whether p shows another host with addr: it sends
// from addr, or it probes addr itself (RFC 5227 §2.1.1).
func arpConflict(p arpPacket, addr netip.Addr, own net.HardwareAddr) bool {
	if p.op != arpOpRequest && p.op != arpOpReply {
		return false
	}
	if p.sha.String() == own.String() {
		return false
	}
	return p.spa == addr || p.op == arpOpRequest && p.spa.IsUnspecified() && p.tpa == addr
}

// arpProbe builds an ARP probe for addr from own.
func arpProbe(addr netip.Addr, own net.HardwareAddr) []byte {
	b := make([]byte, arpLen)
	binary.BigEndian.PutUint16(b[0:2], 1)
	binary.BigEndian.PutUint16(b[2:4], ethPIPv4)
	b[4], b[5] = 6, 4
	binary.BigEndian.PutUint16(b[6:8], arpOpRequest)
	copy(b[8:14], own)
	// spa 0.0.0.0, tha 0
	a := addr.As4()
	copy(b[24:28], a[:])
	return b
}

// parseNA reads the target of an ICMPv6 neighbor advertisement (the
// payload of a raw ICMPv6 socket, without the IPv6 header). ok is false
// for anything else or a short packet; it never panics.
func parseNA(b []byte) (target netip.Addr, ok bool) {
	if len(b) < icmp6NALen || b[0] != icmp6NA || b[1] != 0 {
		return target, false
	}
	return netip.AddrFrom16([16]byte(b[8:24])), true
}

// nsProbe builds a neighbor solicitation for addr with the source
// link-layer option of own.
func nsProbe(addr netip.Addr, own net.HardwareAddr) []byte {
	b := make([]byte, 24, 32)
	b[0] = icmp6NS // code, checksum (the kernel fills it), reserved: 0
	a := addr.As16()
	copy(b[8:24], a[:])
	if len(own) == 6 {
		b = append(b, 1, 1) // source link-layer address, 1 × 8 bytes
		b = append(b, own...)
	}
	return b
}

// solicitedNode is the solicited-node multicast address of addr.
func solicitedNode(addr netip.Addr) netip.Addr {
	a := addr.As16()
	return netip.AddrFrom16([16]byte{0xff, 0x02, 10: 0, 11: 1, 12: 0xff, 13: a[13], 14: a[14], 15: a[15]})
}

// probeDuplicates probes each address on the NIC dev. It returns an error
// that names the position of the first address in use (never the value,
// D-325), or why the probe could not run.
func probeDuplicates(ctx context.Context, dev string, addrs []netip.Addr) error {
	ifi, err := net.InterfaceByName(dev)
	if err != nil {
		return fmt.Errorf("duplicate-address probe: NIC %s: %w", dev, err)
	}
	for i, a := range addrs {
		var err error
		if a.Is4() {
			err = probeARP(ctx, ifi, a)
		} else {
			err = probeNS(ctx, ifi, a)
		}
		if errors.Is(err, errAddrInUse) {
			return fmt.Errorf("new address %d: %w", i+1, err)
		}
		if err != nil {
			return fmt.Errorf("duplicate-address probe of new address %d: %w", i+1, err)
		}
	}
	return nil
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

// listenUntil is the end of the wait of one probe.
func listenUntil(ctx context.Context) time.Time {
	end := time.Now().Add(probeListen)
	if d, ok := ctx.Deadline(); ok && d.Before(end) {
		end = d
	}
	return end
}

func setReadTimeout(fd int) error {
	tv := syscall.NsecToTimeval(probeRead.Nanoseconds())
	return syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
}

func probeARP(ctx context.Context, ifi *net.Interface, addr netip.Addr) error {
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, int(htons(ethPARP)))
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	ll := &syscall.SockaddrLinklayer{Protocol: htons(ethPARP), Ifindex: ifi.Index}
	if err := syscall.Bind(fd, ll); err != nil {
		return err
	}
	if err := setReadTimeout(fd); err != nil {
		return err
	}
	to := &syscall.SockaddrLinklayer{Protocol: htons(ethPARP), Ifindex: ifi.Index, Halen: 6,
		Addr: [8]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}
	pkt := arpProbe(addr, ifi.HardwareAddr)
	end := listenUntil(ctx)
	next, sent := time.Now(), 0
	buf := make([]byte, 1500)
	for time.Now().Before(end) && ctx.Err() == nil {
		if sent < probeCount && !time.Now().Before(next) {
			if err := syscall.Sendto(fd, pkt, 0, to); err != nil {
				return err
			}
			sent++
			next = time.Now().Add(probeSpacing)
		}
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
				continue
			}
			return err
		}
		if p, ok := parseARP(buf[:n]); ok && arpConflict(p, addr, ifi.HardwareAddr) {
			return errAddrInUse
		}
	}
	return ctx.Err()
}

func probeNS(ctx context.Context, ifi *net.Interface, addr netip.Addr) error {
	fd, err := syscall.Socket(syscall.AF_INET6, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.IPPROTO_ICMPV6)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IPV6, ipv6MCastHops, 255); err != nil {
		return err
	}
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IPV6, ipv6MCastIf, ifi.Index); err != nil {
		return err
	}
	if err := syscall.BindToDevice(fd, ifi.Name); err != nil {
		return err
	}
	if err := setReadTimeout(fd); err != nil {
		return err
	}
	to := &syscall.SockaddrInet6{Addr: solicitedNode(addr).As16(), ZoneId: uint32(ifi.Index)}
	pkt := nsProbe(addr, ifi.HardwareAddr)
	end := listenUntil(ctx)
	next, sent := time.Now(), 0
	buf := make([]byte, 1500)
	for time.Now().Before(end) && ctx.Err() == nil {
		if sent < probeCount && !time.Now().Before(next) {
			if err := syscall.Sendto(fd, pkt, 0, to); err != nil {
				return err
			}
			sent++
			next = time.Now().Add(probeSpacing)
		}
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
				continue
			}
			return err
		}
		if t, ok := parseNA(buf[:n]); ok && t == addr {
			return errAddrInUse
		}
	}
	return ctx.Err()
}
