package link

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// Review T-009 must 1: a poll that started before a re-enrollment never
// takes a command after it, and it ends at once.
func TestRevokedPollGetsNoCommand(t *testing.T) {
	f := newFixture(t)
	old := f.agent("web1")
	type res struct {
		code int
		body []byte
	}
	done := make(chan res, 1)
	go func() {
		code, body := f.do("GET", wire.PathPoll+"?wait=10", old, nil)
		done <- res{code, body}
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	tok := f.token("web1")
	var r res
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the revoked poll still waits")
	}
	if r.code != 401 || time.Since(start) > time.Second {
		t.Fatalf("revoked poll: %d %s after %v; want 401 at once", r.code, r.body, time.Since(start))
	}
	code, resp, _ := f.enroll(tok)
	if code != 200 {
		t.Fatal(code)
	}
	id, _ := f.link.Send("web1", wire.CommandApply, wire.ApplyBody{})
	if code, _ := f.do("GET", wire.PathPoll, old, nil); code != 401 {
		t.Errorf("old agent: %d", code)
	}
	if code, _ := f.do("POST", wire.PathReport, old, wire.Report{Type: wire.ReportResult, CommandID: id}); code != 401 {
		t.Errorf("old agent report: %d", code)
	}
	code, body := f.do("GET", wire.PathPoll, resp.Credential, nil)
	var cmd wire.Command
	json.Unmarshal(body, &cmd)
	if code != 200 || cmd.ID != id {
		t.Errorf("the new agent did not get the command: %d %s", code, body)
	}
}

// drift returns a drift report with one changed attribute.
func drift(resource, attr, from, to string) wire.Report {
	return wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: resource, Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: attr, From: from, To: to, Risk: wire.RiskReversible},
			{Attr: "mode", From: "0600", To: "0644", Risk: wire.RiskReversible}}}}}
}

func (f *fixture) last() wire.Report {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) == 0 {
		f.t.Fatal("nothing delivered")
	}
	return f.got[len(f.got)-1].Report
}

// Review T-009 must 2 and the D-062 rule of pl-architect.
func TestRedactionSources(t *testing.T) {
	const file = "file:/etc/app.conf"
	t.Run("head revision after a restart", func(t *testing.T) {
		f := newFixture(t)
		f.link.cfg.Sensitive = func(_ context.Context, target, id string) ([]string, bool, error) {
			if target == "web1" && id == file {
				return []string{"content"}, true, nil
			}
			return nil, false, nil
		}
		cred := f.agent("web1") // a fresh Link: nothing was sent
		if code, _ := f.do("POST", wire.PathReport, cred, drift(file, "content", "password=hunter2", "password=new")); code != 204 {
			t.Fatal(code)
		}
		c := f.last().Items[0].Changes
		if !c[0].Sensitive || c[0].From != nil || c[0].To != nil {
			t.Errorf("content not redacted: %+v", c[0])
		}
		if c[1].To != "0644" {
			t.Errorf("a known, non-sensitive attribute was redacted: %+v", c[1])
		}
	})
	t.Run("unknown everywhere fails closed", func(t *testing.T) {
		f := newFixture(t)
		cred := f.agent("web1")
		if code, _ := f.do("POST", wire.PathReport, cred, drift(file, "content", "a-secret", "b-secret")); code != 204 {
			t.Fatalf("report of an unknown resource must still be accepted: %d", code)
		}
		for _, c := range f.last().Items[0].Changes {
			if !c.Sensitive || c.From != nil || c.To != nil {
				t.Errorf("unknown resource, change kept values: %+v", c)
			}
		}
	})
	t.Run("union of head and sent list", func(t *testing.T) {
		f := newFixture(t)
		f.link.cfg.Sensitive = func(context.Context, string, string) ([]string, bool, error) { return nil, true, nil }
		cred := f.agent("web1")
		f.link.Send("web1", wire.CommandWatch, wire.WatchBody{Resources: []wire.Resource{
			{ID: file, Type: "file", Attrs: map[string]any{"path": "/etc/app.conf"}, Sensitive: []string{"content"}}}})
		f.do("POST", wire.PathReport, cred, drift(file, "content", "x-secret", "y-secret"))
		c := f.last().Items[0].Changes
		if !c[0].Sensitive || c[0].From != nil || c[1].To != "0644" {
			t.Errorf("union: %+v", c)
		}
	})
	t.Run("sent list with no sensitive attributes is known", func(t *testing.T) {
		f := newFixture(t)
		cred := f.agent("web1")
		f.link.Send("web1", wire.CommandWatch, wire.WatchBody{Resources: []wire.Resource{{ID: file, Type: "file"}}})
		f.do("POST", wire.PathReport, cred, drift(file, "content", "plain", "text"))
		if c := f.last().Items[0].Changes; c[0].Sensitive || c[0].To != "text" {
			t.Errorf("a known, non-sensitive change was redacted: %+v", c)
		}
	})
	t.Run("lookup error", func(t *testing.T) {
		f := newFixture(t)
		calls := 0
		f.link.cfg.Sensitive = func(context.Context, string, string) ([]string, bool, error) {
			calls++
			if calls == 1 {
				return nil, false, errors.New("store busy")
			}
			return []string{"content"}, true, nil
		}
		cred := f.agent("web1")
		id, _ := f.link.Send("web1", wire.CommandObserve, wire.ObserveBody{})
		f.do("GET", wire.PathPoll, cred, nil)
		result := drift(file, "content", "a-secret", "b-secret")
		result.Type, result.CommandID = wire.ReportResult, id
		if code, _ := f.do("POST", wire.PathReport, cred, result); code != 503 {
			t.Fatalf("lookup error: %d, want 503", code)
		}
		if len(f.got) != 0 {
			t.Fatal("OnReport was called after a lookup error")
		}
		if code, _ := f.do("POST", wire.PathReport, cred, result); code != 204 {
			t.Fatalf("the result must stay open for the retry: %d", code)
		}
	})
}

// Review T-009 should 4: sensitive desired values never pass in Output or Error.
func TestOutputMasking(t *testing.T) {
	f := newFixture(t)
	cred := f.agent("web1")
	id, _ := f.link.Send("web1", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{{
		ID: "file:/etc/db.conf", Type: "file", Attrs: map[string]any{"path": "/etc/db.conf", "content": "pw=hunter2"},
		Sensitive: []string{"content"}}}})
	f.do("GET", wire.PathPoll, cred, nil)
	f.do("POST", wire.PathReport, cred, wire.Report{Type: wire.ReportResult, CommandID: id, Error: "write failed: pw=hunter2",
		Items: []wire.Item{{Resource: "file:/etc/db.conf", Status: wire.StatusFailed, Output: "diff: +pw=hunter2"}}})
	raw, _ := json.Marshal(f.last())
	if strings.Contains(string(raw), "hunter2") {
		t.Errorf("secret value delivered: %s", raw)
	}
}

// Review T-009 should 3 and 5: CA and directory files.
func TestCAHalfMissingAndDirMode(t *testing.T) {
	f := newFixture(t)
	dir := f.link.cfg.Dir
	caBefore, _ := os.ReadFile(filepath.Join(dir, caCertFile))
	os.Remove(filepath.Join(dir, caKeyFile))
	if _, err := New(Config{Dir: dir, Store: f.st}); err == nil || !strings.Contains(err.Error(), "only one of") {
		t.Errorf("missing CA key: %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, caCertFile)); string(after) != string(caBefore) {
		t.Error("the CA certificate was replaced")
	}

	other := filepath.Join(t.TempDir(), "link")
	os.Mkdir(other, 0o755)
	if _, err := New(Config{Dir: other, Store: f.st}); err == nil || !strings.Contains(err.Error(), "0700") {
		t.Errorf("open directory: %v", err)
	}
}

// Review T-009 should 6: failed credential checks are rate limited per client.
func TestFailedAuthLimit(t *testing.T) {
	f := newFixture(t)
	good := f.agent("web1")
	for i := range 25 { // the burst is 20 failures
		want := http.StatusUnauthorized
		if i >= 20 {
			want = http.StatusTooManyRequests
		}
		if code, _ := f.do("GET", wire.PathPoll, "bad.cred", nil); code != want {
			t.Fatalf("failed auth %d: got %d, want %d", i+1, code, want)
		}
	}
	// A valid agent sharing the blocked IP must back off, not re-enroll.
	for method, path := range map[string]string{"GET": wire.PathPoll, "POST": wire.PathReport} {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "10.0.0.7:5000"
		req.Header.Set("Authorization", "Bearer "+good)
		rec := httptest.NewRecorder()
		f.srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "3" {
			t.Errorf("blocked %s: status %d, Retry-After %q", method, rec.Code, rec.Header().Get("Retry-After"))
		}
	}
	// Another client is not affected.
	req := httptest.NewRequest("GET", wire.PathPoll, nil)
	req.RemoteAddr = "10.0.0.8:1"
	req.Header.Set("Authorization", "Bearer "+good)
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("other client: %d", rec.Code)
	}
	f.clock.add(3 * time.Second)
	if code, _ := f.do("GET", wire.PathPoll, good, nil); code != 204 {
		t.Errorf("after refill: %d", code)
	}
}

// Review T-009 nit 7: the limiter map has a hard cap.
func TestLimiterCap(t *testing.T) {
	c := &clock{t: time.Now()}
	l := newLimiter(c.now, time.Hour, 1, 1<<30)
	for i := range maxClients {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = net4(i) + ":1"
		l.allow(r)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.0.1:1"
	if l.allow(r) {
		t.Error("a new client was admitted with a full, active map")
	}
	if len(l.clients) > maxClients {
		t.Errorf("map grew to %d", len(l.clients))
	}
}

func net4(i int) string {
	return "10." + strconv.Itoa(i>>16&255) + "." + strconv.Itoa(i>>8&255) + "." + strconv.Itoa(i&255)
}
