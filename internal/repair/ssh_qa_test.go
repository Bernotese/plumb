package repair

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-195 (T-191 part), mutant T7: after a host key refusal the Run stops.
// It dials no further address, and the error stays ErrHostKey, so the
// Run says "the host key changed" and not the error of a later address.
func TestRepairQAHostKeyErrorStopsDialling(t *testing.T) {
	e := newEnv(t)
	f := newFakeSSHD(t)
	e.allow(f)
	e.bind("web1", "vm/101")
	e.report("web1", []string{e.currentID()}, f.port(), f.fp())
	f.newHostKey() // the guest now shows a key that is not pinned
	guestMAC := "bc:24:11:00:00:01"
	e.neigh["192.0.2.50"] = guestMAC
	e.neigh["192.0.2.51"] = guestMAC // a second address of the same NIC
	var dialed []string
	e.s.cfg.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		if strings.HasPrefix(addr, "192.0.2.50:") {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(f.port()))
		}
		return nil, errors.New("connection refused")
	}
	_, sent, err := e.s.Send(e.ctx, plan.RepairSend{Target: "web1", Reported: []string{"192.0.2.50/24", "192.0.2.51/24"},
		GuestMAC: guestMAC, Bundle: func(io.Writer) error { return nil }})
	if !errors.Is(err, ErrHostKey) || sent {
		t.Fatalf("err %v sent %v", err, sent)
	}
	// The report has no key types (an agent before T-207), so the session
	// tries each host key type on the same address (T-207); it never
	// dials the next address.
	// At most one dial per host key type (review of #310).
	if len(dialed) == 0 || len(dialed) > len(wire.HostKeyTypeList) {
		t.Fatalf("dials: %v", dialed)
	}
	for _, a := range dialed {
		if !strings.HasPrefix(a, "192.0.2.50:") {
			t.Fatalf("dialled after the host key refusal: %v", dialed)
		}
	}
	if len(f.stdin) != 0 {
		t.Fatal("bytes reached the guest")
	}
}
