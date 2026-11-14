package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// D-366, T-180: the real agent against the real link with a small drift
// limit. Drift over the limit gets 429; the agent keeps its credential and
// the unsent drift per resource (the latest state wins), and after a
// 2 minute block the latest state arrives; the states between are never
// sent (coalescing). A Run result during the block is not limited. No
// agent code changed for this: every agent since D-055 maps 429 to busy
// and keeps a refused drift item dirty (sendDrift, markSent).
func TestDriftBusyResend(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "link.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var offset atomic.Int64
	now := func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	st.Now = now
	deliveries := make(chan link.Delivery, 64)
	ctl, err := link.New(link.Config{Dir: filepath.Join(t.TempDir(), "link"), Store: st, Now: now,
		DriftEvery: time.Minute, DriftBurst: 2,
		OnReport: func(_ context.Context, d link.Delivery) error { deliveries <- d; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	var limited atomic.Int64
	handler := ctl.Handler()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		if r.URL.Path == wire.PathReport && rec.Code == http.StatusTooManyRequests {
			limited.Add(1)
		}
	}))
	srv.TLS = ctl.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	tok, _, err := ctl.NewEnrollToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	ta := startAgentWith(t, &controller{srv: srv}, testCA{pem: string(ctl.CA().CertPEM)}, tok, func(o *Options) {
		o.Config.FullCheck = Duration(200 * time.Millisecond)
	})

	// next returns the next delivery of a type within the wait, or false.
	next := func(typ wire.ReportType, wait time.Duration) (wire.Report, bool) {
		t.Helper()
		deadline := time.After(wait)
		for {
			select {
			case d := <-deliveries:
				if d.Report.Type == typ {
					return d.Report, true
				}
			case <-deadline:
				return wire.Report{}, false
			}
		}
	}
	text := func(rep wire.Report) string { b, _ := json.Marshal(rep); return string(b) }
	if _, ok := next(wire.ReportHello, 5*time.Second); !ok {
		t.Fatal("no hello")
	}
	path := "/srv/busy"
	r := fileRes("busy", path, map[string]any{"content": "v0\n"})
	id, err := ctl.Send("web1", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{r}, Planned: unverified(r), MaxRisk: wire.RiskReversible})
	if err != nil {
		t.Fatal(err)
	}
	if rep, ok := next(wire.ReportResult, 5*time.Second); !ok || rep.CommandID != id {
		t.Fatalf("apply result: %+v", rep)
	}
	edit := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(ta.root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The burst of 2: both edits arrive.
	for _, v := range []string{"v1", "v2"} {
		edit(v + "\n")
		rep, ok := next(wire.ReportDrift, 5*time.Second)
		if !ok || !strings.Contains(text(rep), v) {
			t.Fatalf("drift %s: %v %s", v, ok, text(rep))
		}
	}
	cred := ta.a.credential()

	// The block: v3 is refused, then v4 replaces it in the agent.
	edit("v3\n")
	waitUntil(t, func() bool { return limited.Load() > 0 }, "a 429 for v3")
	edit("v4\n")
	waitUntil(t, func() bool {
		ta.a.mu.Lock()
		defer ta.a.mu.Unlock()
		it, open := ta.a.open[r.ID]
		b, _ := json.Marshal(it)
		return open && ta.a.dirty[r.ID] && strings.Contains(string(b), "v4")
	}, "v4 kept as unsent drift")
	before := limited.Load()
	waitUntil(t, func() bool { return limited.Load() > before }, "a 429 for v4")
	if rep, ok := next(wire.ReportDrift, 500*time.Millisecond); ok {
		t.Fatalf("drift passed the limit: %s", text(rep))
	}
	// A Run result is not limited.
	id, err = ctl.Send("web1", wire.CommandObserve, wire.ObserveBody{Resources: []wire.Resource{r}})
	if err != nil {
		t.Fatal(err)
	}
	if rep, ok := next(wire.ReportResult, 5*time.Second); !ok || rep.CommandID != id {
		t.Fatalf("observe result during the block: %+v", rep)
	}
	if ta.a.credential() != cred {
		t.Fatal("429 cost the credential")
	}

	// 2 minutes later the bucket has room: the latest state (v4) arrives
	// without a new edit, and v3 never does.
	offset.Store(int64(2 * time.Minute))
	rep, ok := next(wire.ReportDrift, 5*time.Second)
	if !ok || !strings.Contains(text(rep), "v4") || strings.Contains(text(rep), "v3") {
		t.Fatalf("after the block: %v %s", ok, text(rep))
	}
	if ta.a.credential() != cred {
		t.Fatal("credential changed")
	}
}

func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
