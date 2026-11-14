package link

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

func jsonBody(v any) *bytes.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func driftRep(resource, to string) wire.Report {
	return wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: resource, Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "owner", From: "root", To: to, Risk: wire.RiskReversible}}}}}
}

// postReport sends a report and returns the code and Retry-After.
func (f *fixture) postReport(cred string, rep wire.Report) (int, string) {
	f.t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", wire.PathReport, jsonBody(rep))
	req.RemoteAddr = "192.0.2.7:5000"
	req.Header.Set("Authorization", "Bearer "+cred)
	f.srv.ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("Retry-After")
}

func (f *fixture) rateEvents() int {
	f.t.Helper()
	evs, err := f.st.EventsAfter(context.Background(), 0, 1000)
	if err != nil {
		f.t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if e.Type == "agent.rate_limited" {
			if e.Subject != "target:web1" || e.Actor != "agent:web1" {
				f.t.Errorf("event %+v", e)
			}
			n++
		}
	}
	return n
}

// D-366: over the limit a drift report gets 429 with Retry-After, nothing
// reaches the handler, the credential stays valid; results, confirms,
// hellos and other agents are not limited; one event per 10 minutes.
func TestDriftRateLimit(t *testing.T) {
	f := newFixture(t)
	f.link.drift = newDriftLimiter(f.clock.now, time.Minute, 2)
	cred := f.agent("web1")
	other := f.agent("web2")
	fw := wire.Resource{ID: "firewall:main", Type: "firewall", Block: "fw", Attrs: map[string]any{"name": "main"}}
	apply, _ := f.link.Send("web1", wire.CommandApply, wire.ApplyBody{PlanID: 1, MaxRisk: wire.RiskDisruptive, Resources: []wire.Resource{fw}})
	f.do("GET", wire.PathPoll, cred, nil)

	for i, to := range []string{"a", "b"} {
		if code, _ := f.postReport(cred, driftRep("file:/x", to)); code != 204 {
			t.Fatalf("drift %d in the burst: %d", i, code)
		}
	}
	for i := 0; i < 5; i++ {
		code, retry := f.postReport(cred, driftRep("file:/x", "c"))
		if code != http.StatusTooManyRequests || retry != "60" {
			t.Fatalf("drift over the limit: %d Retry-After %q, want 429 60", code, retry)
		}
	}
	if n := len(f.got); n != 2 {
		t.Fatalf("%d reports reached the handler, want 2", n)
	}
	if f.rateEvents() != 1 {
		t.Errorf("events = %d, want 1 for the whole block", f.rateEvents())
	}
	// Never limited: the confirm and the result of a Run, a hello, another agent.
	if code, _ := f.postReport(cred, wire.Report{Type: wire.ReportConfirm, CommandID: apply, Items: []wire.Item{{Resource: "firewall:main", Status: wire.StatusRunning}}}); code != 204 {
		t.Errorf("confirm during the block: %d", code)
	}
	if code, _ := f.postReport(cred, wire.Report{Type: wire.ReportResult, CommandID: apply, Items: []wire.Item{{Resource: "firewall:main", Status: wire.StatusApplied}}}); code != 204 {
		t.Errorf("result during the block: %d", code)
	}
	if code, _ := f.postReport(cred, wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "0.2", Hostname: "web1"}}); code != 204 {
		t.Errorf("hello during the block: %d", code)
	}
	if code, _ := f.postReport(other, driftRep("file:/x", "z")); code != 204 {
		t.Errorf("another agent: %d", code)
	}
	// The credential stays: a poll works.
	if code, _ := f.do("GET", wire.PathPoll, cred, nil); code == http.StatusUnauthorized {
		t.Error("429 cost the credential")
	}
	// One token per minute: one report after a minute, then 429 again.
	f.clock.add(time.Minute)
	if code, _ := f.postReport(cred, driftRep("file:/x", "d")); code != 204 {
		t.Errorf("after a minute: %d", code)
	}
	if code, _ := f.postReport(cred, driftRep("file:/x", "e")); code != 429 {
		t.Errorf("second after a minute: %d", code)
	}
	if f.rateEvents() != 1 {
		t.Errorf("events within 10 minutes = %d, want 1", f.rateEvents())
	}
	f.clock.add(10 * time.Minute)
	for i := 0; i < 3; i++ {
		f.postReport(cred, driftRep("file:/x", "f"))
	}
	if f.rateEvents() != 2 {
		t.Errorf("events after 10 minutes = %d, want 2", f.rateEvents())
	}
}

// The defaults of D-366: a burst of 240, then 2 per second.
func TestDriftLimiterDefaults(t *testing.T) {
	c := &clock{t: time.Now()}
	d := newDriftLimiter(c.now, 0, 0)
	if d.every != 500*time.Millisecond || d.burst != 240 {
		t.Fatalf("defaults: every %v burst %d", d.every, d.burst)
	}
	for i := 0; i < 240; i++ {
		if ok, _, _ := d.take("a"); !ok {
			t.Fatalf("report %d of the burst refused", i)
		}
	}
	ok, retry, event := d.take("a")
	if ok || retry != time.Second || !event {
		t.Fatalf("241st: ok %v retry %v event %v", ok, retry, event)
	}
	c.add(time.Minute)
	n := 0
	for {
		if ok, _, _ := d.take("a"); !ok {
			break
		}
		n++
	}
	if n != 120 {
		t.Errorf("after a minute %d reports, want 120", n)
	}
}
