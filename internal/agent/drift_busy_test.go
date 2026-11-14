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

// busyFixture is a real agent against the real link with a small drift
// limit (T-180, T-202), and one applied file resource.
type busyFixture struct {
	t          *testing.T
	ctl        *link.Link
	ta         *testAgent
	offset     atomic.Int64 // link clock offset
	limited    atomic.Int64 // 429 answers to reports
	deliveries chan link.Delivery
	res        wire.Resource
}

func newBusyFixture(t *testing.T, every time.Duration, burst int, fullCheck time.Duration) *busyFixture {
	t.Helper()
	ctx := context.Background()
	f := &busyFixture{t: t, deliveries: make(chan link.Delivery, 64)}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "link.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := func() time.Time { return time.Now().Add(time.Duration(f.offset.Load())) }
	st.Now = now
	f.ctl, err = link.New(link.Config{Dir: filepath.Join(t.TempDir(), "link"), Store: st, Now: now,
		DriftEvery: every, DriftBurst: burst,
		OnReport: func(_ context.Context, d link.Delivery) error { f.deliveries <- d; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := f.ctl.Handler()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		if r.URL.Path == wire.PathReport && rec.Code == http.StatusTooManyRequests {
			f.limited.Add(1)
		}
	}))
	srv.TLS = f.ctl.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	tok, _, err := f.ctl.NewEnrollToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	f.ta = startAgentWith(t, &controller{srv: srv}, testCA{pem: string(f.ctl.CA().CertPEM)}, tok, func(o *Options) {
		o.Config.FullCheck = Duration(fullCheck)
	})
	if _, ok := f.next(wire.ReportHello, 5*time.Second); !ok {
		t.Fatal("no hello")
	}
	f.res = fileRes("busy", "/srv/busy", map[string]any{"content": "v0\n"})
	id, err := f.ctl.Send("web1", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{f.res}, Planned: unverified(f.res), MaxRisk: wire.RiskReversible})
	if err != nil {
		t.Fatal(err)
	}
	if rep, ok := f.next(wire.ReportResult, 5*time.Second); !ok || rep.CommandID != id {
		t.Fatalf("apply result: %+v", rep)
	}
	return f
}

// next returns the next delivery of a type within the wait, or false.
func (f *busyFixture) next(typ wire.ReportType, wait time.Duration) (wire.Report, bool) {
	f.t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case d := <-f.deliveries:
			if d.Report.Type == typ {
				return d.Report, true
			}
		case <-deadline:
			return wire.Report{}, false
		}
	}
}

func reportText(rep wire.Report) string { b, _ := json.Marshal(rep); return string(b) }

// edit replaces the file atomically (temp file and rename). A plain
// os.WriteFile truncates first: a writer slowed down (race detector) lets
// the debounced check observe the empty file and report it (T-216).
func (f *busyFixture) edit(content string) {
	f.t.Helper()
	path := filepath.Join(f.ta.root, "/srv/busy")
	tmp := filepath.Join(filepath.Dir(path), ".busy.tmp")
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		f.t.Fatal(err)
	}
}

// burstThenBlock sends v1 and v2 (the burst of 2), then v3 is refused and
// v4 is kept as unsent drift with at least one 429 after it.
func (f *busyFixture) burstThenBlock() {
	f.t.Helper()
	for _, v := range []string{"v1", "v2"} {
		f.edit(v + "\n")
		rep, ok := f.next(wire.ReportDrift, 5*time.Second)
		if !ok || !strings.Contains(reportText(rep), v) {
			f.t.Fatalf("drift %s: %v %s", v, ok, reportText(rep))
		}
	}
	f.edit("v3\n")
	waitUntil(f.t, func() bool { return f.limited.Load() > 0 }, "a 429 for v3")
	f.edit("v4\n")
	waitUntil(f.t, func() bool {
		f.ta.a.mu.Lock()
		defer f.ta.a.mu.Unlock()
		it, open := f.ta.a.open[f.res.ID]
		b, _ := json.Marshal(it)
		return open && f.ta.a.dirty[f.res.ID] && strings.Contains(string(b), "v4")
	}, "v4 kept as unsent drift")
}

// D-366, T-180: drift over the limit gets 429; the agent keeps its
// credential and the unsent drift per resource (the latest state wins);
// after a 2 minute block the latest state arrives and the states between
// are never sent. A Run result during the block is not limited. Every
// agent since D-055 maps 429 to busy and keeps a refused item dirty.
func TestDriftBusyResend(t *testing.T) {
	f := newBusyFixture(t, time.Minute, 2, 200*time.Millisecond)
	f.burstThenBlock()
	cred := f.ta.a.credential()
	before := f.limited.Load()
	waitUntil(t, func() bool { return f.limited.Load() > before }, "a 429 for v4")
	if rep, ok := f.next(wire.ReportDrift, 500*time.Millisecond); ok {
		t.Fatalf("drift passed the limit: %s", reportText(rep))
	}
	// A Run result is not limited.
	id, err := f.ctl.Send("web1", wire.CommandObserve, wire.ObserveBody{Resources: []wire.Resource{f.res}})
	if err != nil {
		t.Fatal(err)
	}
	if rep, ok := f.next(wire.ReportResult, 5*time.Second); !ok || rep.CommandID != id {
		t.Fatalf("observe result during the block: %+v", rep)
	}
	if f.ta.a.credential() != cred {
		t.Fatal("429 cost the credential")
	}
	f.offset.Store(int64(2 * time.Minute))
	rep, ok := f.next(wire.ReportDrift, 5*time.Second)
	if !ok || !strings.Contains(reportText(rep), "v4") || strings.Contains(reportText(rep), "v3") {
		t.Fatalf("after the block: %v %s", ok, reportText(rep))
	}
	if f.ta.a.credential() != cred {
		t.Fatal("credential changed")
	}
}

// T-202: with no check due (full check in an hour) and no new edit, the
// kept drift is sent again after Retry-After by the resend timer: the
// latest state arrives within Retry-After + a few seconds, once.
func TestDriftResendAfterRetryAfter(t *testing.T) {
	f := newBusyFixture(t, 2*time.Second, 2, time.Hour)
	f.burstThenBlock()
	start := time.Now()
	rep, ok := f.next(wire.ReportDrift, 2*time.Second+5*time.Second)
	if !ok || !strings.Contains(reportText(rep), "v4") || strings.Contains(reportText(rep), "v3") {
		t.Fatalf("no resend after Retry-After: %v %s", ok, reportText(rep))
	}
	t.Logf("latest drift arrived %s after v4 was kept", time.Since(start).Round(10*time.Millisecond))
	// One resend per wait: nothing more is sent while nothing changes.
	if rep, ok := f.next(wire.ReportDrift, 3*time.Second); ok {
		t.Fatalf("a second resend: %s", reportText(rep))
	}
}

// T-202: a stop during the wait ends the agent at once; the timer is the
// drift loop's and ends with it.
func TestDriftResendStopDuringWait(t *testing.T) {
	f := newBusyFixture(t, time.Hour, 2, time.Hour) // Retry-After 2 min (cap)
	f.burstThenBlock()
	f.ta.cancel()
	select {
	case <-f.ta.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent did not stop during the resend wait")
	}
}

func TestResendWait(t *testing.T) {
	for in, lo := range map[time.Duration]time.Duration{0: time.Second, 30 * time.Second: 30 * time.Second, time.Hour: maxResendWait} {
		for range 50 {
			if got := resendWait(in); got < lo || got > lo+lo/10+time.Millisecond {
				t.Fatalf("resendWait(%v) = %v, want %v … +10%%", in, got, lo)
			}
		}
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
