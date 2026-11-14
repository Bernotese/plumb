package agent

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Bernotese/plumb/internal/wire"
)

// Live drift of ip and nft changes (D-367). The agent subscribes to the
// rtnetlink groups of links, IPv4/IPv6 addresses and routes (for network
// resources) and to the nftables group of NETLINK_NETFILTER (for firewall
// resources), only while it holds a resource of that type. A message is a
// trigger only: the agent checks the netlink headers and nothing of the
// payload. After the debounce (2 s), and at most once per netGap per
// type, the drift loop runs the normal check of every resource of the
// type. Events during an apply are skipped, as for inotify. The full
// check every Config.FullCheck stays.

// netKind is one watched resource type.
type netKind int

const (
	netNetwork netKind = iota
	netFirewall
	netKinds // number of kinds
)

// resourceType is the resource type that a kind triggers.
func (k netKind) resourceType() string {
	if k == netFirewall {
		return "firewall"
	}
	return "network"
}

// netGap is the shortest time between two checks of one kind (D-367).
const netGap = 5 * time.Second

// netSocket opens a netlink socket of proto that is a member of groups.
// The socket is non-blocking and close-on-exec.
func netSocket(kind netKind) (*os.File, error) {
	proto, groups := unix.NETLINK_ROUTE, []int{unix.RTNLGRP_LINK,
		unix.RTNLGRP_IPV4_IFADDR, unix.RTNLGRP_IPV4_ROUTE, unix.RTNLGRP_IPV6_IFADDR, unix.RTNLGRP_IPV6_ROUTE}
	if kind == netFirewall {
		proto, groups = unix.NETLINK_NETFILTER, []int{unix.NFNLGRP_NFTABLES}
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, proto)
	if err != nil {
		return nil, fmt.Errorf("netlink socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("netlink bind: %w", err)
	}
	for _, g := range groups {
		if err := unix.SetsockoptInt(fd, unix.SOL_NETLINK, unix.NETLINK_ADD_MEMBERSHIP, g); err != nil {
			unix.Close(fd)
			return nil, fmt.Errorf("netlink group %d: %w", g, err)
		}
	}
	// A non-blocking fd in os.File uses the runtime poller, so Close ends
	// a blocked read.
	return os.NewFile(uintptr(fd), "netlink"), nil
}

// netTrigger reports whether a received netlink buffer is a trigger. It
// reads only the headers: every header must be whole (length at least the
// header, not past the buffer), and at least one message must be an event
// (type at least NLMSG_MIN_TYPE, not a control message). It never reads
// past b and never panics. truncated: the kernel had more than b holds;
// that is an event of the kernel too.
func netTrigger(b []byte, truncated bool) bool {
	if truncated {
		return true
	}
	event := false
	for off := 0; off < len(b); {
		if len(b)-off < unix.SizeofNlMsghdr {
			return false
		}
		l := int(binary.NativeEndian.Uint32(b[off:]))
		typ := binary.NativeEndian.Uint16(b[off+4:])
		if l < unix.SizeofNlMsghdr || l > len(b)-off {
			return false
		}
		if typ >= unix.NLMSG_MIN_TYPE {
			event = true
		}
		next := off + (l+unix.NLMSG_ALIGNTO-1)&^(unix.NLMSG_ALIGNTO-1)
		if next < off { // overflow on a huge length; cannot happen after the check above
			return false
		}
		off = next
	}
	return event
}

// netSub is one subscribed socket and its pacing goroutine.
type netSub struct {
	f      *os.File
	hit    chan struct{} // a trigger came; capacity 1
	cancel context.CancelFunc
	done   sync.WaitGroup
}

// netWatch holds the subscriptions of the drift loop. Only the drift loop
// calls sync and close; fire carries the kinds whose check is due.
type netWatch struct {
	log      *slog.Logger
	debounce time.Duration
	gap      time.Duration
	skip     func() bool // an apply runs: drop the event
	open     func(netKind) (*os.File, error)
	fire     chan netKind
	subs     [netKinds]*netSub
	failed   [netKinds]bool // subscribe failed (logged once; full check only)
	// live is what the agent reports as live_watch; set by probe and sync.
	mu   sync.Mutex
	live [netKinds]bool
}

func newNetWatch(log *slog.Logger, debounce time.Duration, skip func() bool) *netWatch {
	return &netWatch{log: log, debounce: debounce, gap: netGap, skip: skip, open: netSocket,
		fire: make(chan netKind, netKinds)}
}

// probe opens and closes a socket of every kind, to tell the controller in
// the hello which kinds the agent can watch live, before it holds any
// resource of the kind.
func (n *netWatch) probe() {
	for k := range netKinds {
		f, err := n.open(k)
		ok := err == nil
		if ok {
			_ = f.Close()
		} else {
			n.failed[k] = true
			n.log.Warn("cannot watch netlink; drift of this type is found only by the full check",
				"type", k.resourceType(), "err", err)
		}
		n.mu.Lock()
		n.live[k] = ok
		n.mu.Unlock()
	}
}

// liveWatch is the hello field (D-367).
func (n *netWatch) liveWatch() *wire.LiveWatch {
	n.mu.Lock()
	defer n.mu.Unlock()
	return &wire.LiveWatch{Network: n.live[netNetwork], Firewall: n.live[netFirewall]}
}

// sync subscribes the kinds in want and closes the others. A kind whose
// subscribe failed once stays on the full check until the agent restarts.
func (n *netWatch) sync(ctx context.Context, want [netKinds]bool) {
	for k := range netKinds {
		switch {
		case want[k] && n.subs[k] == nil && !n.failed[k]:
			f, err := n.open(k)
			if err != nil {
				n.failed[k] = true
				n.mu.Lock()
				n.live[k] = false
				n.mu.Unlock()
				n.log.Warn("cannot watch netlink; drift of this type is found only by the full check",
					"type", k.resourceType(), "err", err)
				continue
			}
			n.subs[k] = n.start(ctx, k, f)
		case !want[k] && n.subs[k] != nil:
			n.subs[k].stop()
			n.subs[k] = nil
		}
	}
}

func (n *netWatch) close() {
	for k := range netKinds {
		if n.subs[k] != nil {
			n.subs[k].stop()
			n.subs[k] = nil
		}
	}
}

func (n *netWatch) start(ctx context.Context, k netKind, f *os.File) *netSub {
	ctx, cancel := context.WithCancel(ctx)
	s := &netSub{f: f, hit: make(chan struct{}, 1), cancel: cancel}
	s.done.Add(2)
	go func() { defer s.done.Done(); n.read(k, s) }()
	go func() { defer s.done.Done(); n.pace(ctx, k, s) }()
	return s
}

func (s *netSub) stop() {
	s.cancel()
	_ = s.f.Close()
	s.done.Wait()
}

// read receives messages until the socket is closed and marks a hit for
// every trigger from the kernel. A lost message (ENOBUFS: the socket
// buffer ran over) is a hit too.
func (n *netWatch) read(k netKind, s *netSub) {
	rc, err := s.f.SyscallConn()
	if err != nil {
		return
	}
	buf := make([]byte, 64<<10)
	for {
		var (
			nr   int
			from unix.Sockaddr
			rerr error
		)
		err := rc.Read(func(fd uintptr) bool {
			nr, from, rerr = unix.Recvfrom(int(fd), buf, unix.MSG_TRUNC)
			return !errors.Is(rerr, unix.EAGAIN)
		})
		if err != nil {
			return // closed
		}
		hit, pause := netHit(buf, nr, from, rerr)
		if pause {
			// Not expected on a netlink socket; do not spin on it.
			n.log.Debug("netlink receive", "type", k.resourceType(), "err", rerr)
			time.Sleep(100 * time.Millisecond)
		}
		if !hit || n.skip() {
			continue
		}
		select {
		case s.hit <- struct{}{}:
		default: // a hit is pending already
		}
	}
}

// netHit decides about one receive of read: hit is a trigger, pause asks
// read to wait before the next receive (an unexpected error). A lost
// message (ENOBUFS: the socket buffer ran over) is a hit. Only the kernel
// (port id 0) sends events: a message of another process is never a hit.
// buf holds the message; nr is its full length (MSG_TRUNC).
func netHit(buf []byte, nr int, from unix.Sockaddr, rerr error) (hit, pause bool) {
	switch {
	case errors.Is(rerr, unix.ENOBUFS):
		return true, false
	case errors.Is(rerr, unix.EINTR):
		return false, false
	case rerr != nil:
		return false, true
	}
	sa, ok := from.(*unix.SockaddrNetlink)
	if !ok || sa.Pid != 0 || nr < 0 {
		return false, false
	}
	return netTrigger(buf[:min(nr, len(buf))], nr > len(buf)), false
}

// pace turns hits into fires: the first hit waits the debounce, and two
// fires of one kind are at least gap apart. Hits during the wait are
// covered by the check that follows.
func (n *netWatch) pace(ctx context.Context, k netKind, s *netSub) {
	var last time.Time
	t := time.NewTimer(time.Hour)
	t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.hit:
		}
		due := time.Now().Add(n.debounce)
		if g := last.Add(n.gap); g.After(due) {
			due = g
		}
		t.Reset(time.Until(due))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		select {
		case <-s.hit:
		default:
		}
		last = time.Now()
		select {
		case n.fire <- k:
		case <-ctx.Done():
			return
		}
	}
}

// netWant returns the kinds that the list holds a resource of.
func netWant(list []wire.Resource) [netKinds]bool {
	var want [netKinds]bool
	for _, r := range list {
		for k := range netKinds {
			if r.Type == k.resourceType() {
				want[k] = true
			}
		}
	}
	return want
}

// netIDs returns the ids of the resources of kind k.
func netIDs(list []wire.Resource, k netKind) []string {
	ids := []string{}
	for _, r := range list {
		if r.Type == k.resourceType() {
			ids = append(ids, r.ID)
		}
	}
	return ids
}
