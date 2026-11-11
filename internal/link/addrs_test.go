package link

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *logBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// A bad address list in a Hello: the Hello is taken (the link stays), the
// list goes on as unknown (nil), and the log holds the reason, never an
// address (D-212).
func TestHelloBadAddrsKeepsLink(t *testing.T) {
	f := newFixture(t)
	var logs logBuffer
	f.link.log = slog.New(slog.NewTextHandler(&logs, nil))
	cred := f.agent("web1")
	const secretish = "127.0.0.9"
	code, body := f.do("POST", wire.PathReport, cred, wire.Report{Type: wire.ReportHello,
		Hello: &wire.Hello{Version: "1", Addrs: []string{"192.0.2.1", secretish}, AddrsTruncated: true}})
	if code != 204 {
		t.Fatalf("hello with a bad list: %d %s", code, body)
	}
	f.mu.Lock()
	got := f.got[len(f.got)-1].Report.Hello
	f.mu.Unlock()
	if got.Addrs != nil || got.AddrsTruncated {
		t.Fatalf("delivered list %#v %v, want unknown", got.Addrs, got.AddrsTruncated)
	}
	out := logs.String()
	if !strings.Contains(out, "bad address list") || strings.Contains(out, secretish) || strings.Contains(out, "192.0.2.1") {
		t.Fatalf("log: %s", out)
	}
	// A good list passes as it is; an empty list stays empty, not nil.
	f.do("POST", wire.PathReport, cred, wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "1", Addrs: []string{}}})
	f.mu.Lock()
	got = f.got[len(f.got)-1].Report.Hello
	f.mu.Unlock()
	if got.Addrs == nil {
		t.Fatal("an empty list became unknown")
	}
	// An addrs report with a bad list: 400 with the reason only.
	code, body = f.do("POST", wire.PathReport, cred, wire.Report{Type: wire.ReportAddrs, Addrs: []string{secretish}})
	if code != 400 || strings.Contains(string(body), secretish) {
		t.Fatalf("bad addrs report: %d %s", code, body)
	}
}

// OnReport's ErrTooOften is a 429 with Retry-After.
func TestAddrsTooOftenIs429(t *testing.T) {
	f := newFixture(t)
	f.link.cfg.OnReport = func(context.Context, Delivery) error { return ErrTooOften }
	cred := f.agent("web1")
	req := wire.Report{Type: wire.ReportAddrs, Addrs: []string{"192.0.2.1"}}
	code, _ := f.do("POST", wire.PathReport, cred, req)
	if code != 429 {
		t.Fatalf("code %d, want 429", code)
	}
}
