package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Bernotese/plumb/internal/wire"
)

// nlHdr builds one netlink header with the given length field and type,
// followed by pad bytes of payload.
func nlHdr(length uint32, typ uint16, pad int) []byte {
	b := make([]byte, unix.SizeofNlMsghdr+pad)
	binary.NativeEndian.PutUint32(b, length)
	binary.NativeEndian.PutUint16(b[4:], typ)
	return b
}

func TestNetTrigger(t *testing.T) {
	ev := nlHdr(20, unix.RTM_NEWADDR, 4)
	odd := nlHdr(17, unix.RTM_NEWADDR, 1) // unaligned last message
	cases := []struct {
		name string
		b    []byte
		want bool
	}{
		{"empty", nil, false},
		{"short header", ev[:15], false},
		{"one event", ev, true},
		{"two events", append(append([]byte{}, ev...), ev...), true},
		{"unaligned last", append(append([]byte{}, ev...), odd...), true},
		{"unaligned not last", append(append([]byte{}, odd...), ev...), false},
		{"length below header", nlHdr(8, unix.RTM_NEWADDR, 4), false},
		{"length past buffer", nlHdr(64, unix.RTM_NEWADDR, 4), false},
		{"huge length", nlHdr(0xffffffff, unix.RTM_NEWADDR, 4), false},
		{"control only", append(nlHdr(16, unix.NLMSG_DONE, 0), nlHdr(16, unix.NLMSG_NOOP, 0)...), false},
		{"event after control", append(nlHdr(16, unix.NLMSG_DONE, 0), ev...), true},
		{"trailing garbage", append(append([]byte{}, ev...), 1, 2, 3), false},
		{"nftables event", nlHdr(20, 0x0a<<8|6, 4), true},
	}
	for _, c := range cases {
		if got := netTrigger(c.b, false); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if !netTrigger(nil, true) {
		t.Error("a truncated kernel message must be a trigger")
	}
}

// FuzzNetTrigger: no input panics the header check (memory note on
// syscall.ParseNetlinkRouteAttr), and a trigger always has a whole first
// header inside the buffer.
func FuzzNetTrigger(f *testing.F) {
	f.Add(nlHdr(20, unix.RTM_NEWLINK, 4))
	f.Add(nlHdr(17, unix.RTM_NEWADDR, 1))
	f.Add(nlHdr(0xffffffff, 0xffff, 0))
	f.Add([]byte{1, 2, 3})
	f.Add(append(nlHdr(16, unix.NLMSG_DONE, 0), nlHdr(20, unix.RTM_NEWROUTE, 4)...))
	f.Fuzz(func(t *testing.T, b []byte) {
		if !netTrigger(b, false) {
			return
		}
		if len(b) < unix.SizeofNlMsghdr {
			t.Fatalf("trigger on %d bytes", len(b))
		}
		l := int(binary.NativeEndian.Uint32(b))
		if l < unix.SizeofNlMsghdr || l > len(b) {
			t.Fatalf("trigger with first length %d of %d", l, len(b))
		}
	})
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(nopWriter{}, nil)) }

type nopWriter struct{}

func (nopWriter) Write(b []byte) (int, error) { return len(b), nil }

// TestNetPace: the first hit fires after the debounce; a storm of hits
// (a flapping link) fires at most once per gap, and the hits during a
// wait are covered by the fire that follows.
func TestNetPace(t *testing.T) {
	n := newNetWatch(quietLog(), 50*time.Millisecond, func() bool { return false })
	n.gap = 300 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &netSub{hit: make(chan struct{}, 1), cancel: cancel}
	go n.pace(ctx, netNetwork, s)

	start := time.Now()
	s.hit <- struct{}{}
	if k := <-n.fire; k != netNetwork {
		t.Fatalf("kind %v", k)
	}
	if d := time.Since(start); d < 50*time.Millisecond {
		t.Fatalf("fired after %v, before the debounce", d)
	}
	// Storm for 1 s: hits every 5 ms.
	var fires atomic.Int32
	stop := time.After(time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	var last time.Time
	prev := time.Now()
loop:
	for {
		select {
		case <-stop:
			break loop
		case <-tick.C:
			select {
			case s.hit <- struct{}{}:
			default:
			}
		case <-n.fire:
			now := time.Now()
			if gap := now.Sub(prev); gap < 290*time.Millisecond {
				t.Fatalf("two fires %v apart, want at least the gap", gap)
			}
			prev, last = now, now
			fires.Add(1)
		}
	}
	if f := fires.Load(); f < 2 || f > 4 {
		t.Fatalf("%d fires in 1 s of storm, want 2 … 4", f)
	}
	// After the storm: one more fire covers the last hits, then quiet.
	select {
	case <-n.fire:
	case <-time.After(time.Second):
		t.Fatalf("no fire after the storm (last at %v)", last)
	}
	select {
	case <-n.fire:
		t.Fatal("fire without a hit")
	case <-time.After(400 * time.Millisecond):
	}
}

// TestNetWatchUnavailable: a kind that cannot subscribe is reported as
// live_watch false, logged once, and never tried again; the other kind
// stays live.
func TestNetWatchUnavailable(t *testing.T) {
	var logs bytes.Buffer
	n := newNetWatch(slog.New(slog.NewTextHandler(&logs, nil)), time.Second, func() bool { return false })
	var opens [netKinds]int
	n.open = func(k netKind) (*os.File, error) {
		opens[k]++
		if k == netFirewall {
			return nil, unix.EPERM
		}
		r, w, err := os.Pipe()
		if err == nil {
			w.Close()
		}
		return r, err
	}
	if lw := n.liveWatch(); lw.Network || lw.Firewall {
		t.Fatalf("before the probe: %+v", lw)
	}
	n.probe()
	if lw := n.liveWatch(); !lw.Network || lw.Firewall {
		t.Fatalf("after the probe: %+v", lw)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	both := [netKinds]bool{true, true}
	n.sync(ctx, both)
	n.sync(ctx, both)
	if n.subs[netNetwork] == nil || n.subs[netFirewall] != nil {
		t.Fatalf("subs: %+v", n.subs)
	}
	if opens[netFirewall] != 1 || opens[netNetwork] != 2 {
		t.Fatalf("opens: %v (probe + one subscribe for network; firewall only once)", opens)
	}
	if c := strings.Count(logs.String(), "cannot watch netlink"); c != 1 {
		t.Fatalf("%d log lines, want 1:\n%s", c, logs.String())
	}
	n.sync(ctx, [netKinds]bool{})
	if n.subs[netNetwork] != nil {
		t.Fatal("network still subscribed without a network resource")
	}
	n.close()
}

func TestNetWantIDs(t *testing.T) {
	list := []wire.Resource{{ID: "network:a", Type: "network"}, {ID: "file:x", Type: "file"},
		{ID: "network:b", Type: "network"}}
	if w := netWant(list); !w[netNetwork] || w[netFirewall] {
		t.Fatalf("want %v", w)
	}
	if ids := netIDs(list, netNetwork); len(ids) != 2 {
		t.Fatalf("ids %v", ids)
	}
	if ids := netIDs(list, netFirewall); ids == nil || len(ids) != 0 {
		t.Fatalf("firewall ids %v, want empty non-nil (nil means every resource to check)", ids)
	}
}

// TestNetReadOnlyKernel: a message from another process (port id not 0)
// is no trigger. Needs
// rtnetlink, which every Linux test host has; skips when it is missing.
func TestNetReadOnlyKernel(t *testing.T) {
	f, err := netSocket(netNetwork)
	if err != nil {
		t.Skip("no rtnetlink here:", err)
	}
	n := newNetWatch(quietLog(), time.Millisecond, func() bool { return false })
	_, cancel := context.WithCancel(context.Background())
	s := &netSub{f: f, hit: make(chan struct{}, 1), cancel: cancel}
	s.done.Add(1)
	go func() { defer s.done.Done(); n.read(netNetwork, s) }()
	defer s.stop()

	rc, _ := f.SyscallConn()
	var port uint32
	_ = rc.Control(func(fd uintptr) {
		if sa, err := unix.Getsockname(int(fd)); err == nil {
			port = sa.(*unix.SockaddrNetlink).Pid
		}
	})
	sender, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		t.Skip(err)
	}
	defer unix.Close(sender)
	if err := unix.Sendto(sender, nlHdr(20, unix.RTM_NEWADDR, 4), 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Pid: port}); err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.ECONNREFUSED) {
			t.Skip("cannot send to the socket here:", err)
		}
		t.Fatal(err)
	}
	select {
	case <-s.hit:
		t.Fatal("a message of another process triggered")
	case <-time.After(300 * time.Millisecond):
	}
}
