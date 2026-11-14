package agent

import (
	"context"
	"encoding/binary"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// T-186 (A12) QA of the live watch (D-367).

// The gap of D-367: at most one triggered check per 5 s per type.
func TestQANetGap(t *testing.T) {
	if netGap != 5*time.Second {
		t.Fatalf("netGap %v, want 5s (D-367)", netGap)
	}
	if n := newNetWatch(quietLog(), time.Second, func() bool { return false }); n.gap != netGap {
		t.Fatalf("a new watch uses gap %v, want netGap", n.gap)
	}
}

// A header whose length is shorter than the header is never a trigger,
// and never loops (length 0 would not move the offset).
func TestQANetTriggerShortLength(t *testing.T) {
	for _, l := range []uint32{0, 1, unix.SizeofNlMsghdr - 1} {
		b := make([]byte, 2*unix.SizeofNlMsghdr)
		binary.NativeEndian.PutUint32(b, l)
		binary.NativeEndian.PutUint16(b[4:], unix.RTM_NEWADDR)
		done := make(chan bool, 1)
		go func() { done <- netTrigger(b, false) }()
		select {
		case hit := <-done:
			if hit {
				t.Errorf("length %d: a trigger", l)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("length %d: netTrigger does not end", l)
		}
	}
}

// routeSocket is a NETLINK_ROUTE socket in no group: only the kernel's
// answers to its own requests reach it, so the test sees no foreign
// event. Any user may ask for the link list.
func routeSocket(t *testing.T) (*os.File, func()) {
	t.Helper()
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_ROUTE)
	if err != nil {
		t.Skipf("no netlink: %v", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		unix.Close(fd)
		t.Skipf("netlink bind: %v", err)
	}
	ask := func() {
		req := make([]byte, unix.SizeofNlMsghdr+unix.SizeofIfInfomsg)
		binary.NativeEndian.PutUint32(req, uint32(len(req)))
		binary.NativeEndian.PutUint16(req[4:], unix.RTM_GETLINK)
		binary.NativeEndian.PutUint16(req[6:], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
		binary.NativeEndian.PutUint32(req[8:], 1)
		if err := unix.Sendto(fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
			t.Fatalf("netlink request: %v", err)
		}
	}
	return os.NewFile(uintptr(fd), "netlink-qa"), ask
}

// Events while the agent applies are dropped (as for inotify): a kernel
// message that comes during an apply gives no check; the same message
// without an apply gives one.
func TestQANetSkipDuringApply(t *testing.T) {
	for _, applying := range []bool{true, false} {
		n := newNetWatch(quietLog(), 10*time.Millisecond, func() bool { return applying })
		n.gap = 10 * time.Millisecond
		f, ask := routeSocket(t)
		ctx, cancel := context.WithCancel(context.Background())
		s := n.start(ctx, netNetwork, f)
		ask()
		var fired bool
		select {
		case <-n.fire:
			fired = true
		case <-time.After(500 * time.Millisecond):
		}
		s.stop()
		cancel()
		if fired == applying {
			t.Fatalf("applying=%v: fired=%v", applying, fired)
		}
	}
}
