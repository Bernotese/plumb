package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/store"
)

// sse reads server-sent events from a live server.
type sse struct {
	resp *http.Response
	sc   *bufio.Scanner
}

func (e *env) stream(c *client, query, lastID string) *sse {
	e.t.Helper()
	ts := httptest.NewServer(e.h)
	e.t.Cleanup(ts.Close)
	req, _ := http.NewRequestWithContext(e.ctx, "GET", ts.URL+"/api/v1/events"+query, nil)
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		e.t.Fatalf("stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return &sse{resp: resp, sc: bufio.NewScanner(resp.Body)}
}

type sseEvent struct {
	id, typ string
	data    eventView
	ping    bool
}

// next returns the next event or keep-alive comment.
func (s *sse) next(t *testing.T) sseEvent {
	t.Helper()
	done := make(chan sseEvent, 1)
	go func() {
		var ev sseEvent
		for s.sc.Scan() {
			line := s.sc.Text()
			switch {
			case line == "":
				if ev.id != "" || ev.ping {
					done <- ev
					return
				}
			case strings.HasPrefix(line, ": keep-alive"):
				ev.ping = true
			case strings.HasPrefix(line, "id: "):
				ev.id = line[4:]
			case strings.HasPrefix(line, "event: "):
				ev.typ = line[7:]
			case strings.HasPrefix(line, "data: "):
				json.Unmarshal([]byte(line[6:]), &ev.data)
			}
		}
		close(done)
	}()
	select {
	case ev, ok := <-done:
		if !ok {
			t.Fatal("stream ended")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	return sseEvent{}
}

func (s *sse) nextEvent(t *testing.T) sseEvent {
	t.Helper()
	for {
		if ev := s.next(t); !ev.ping {
			return ev
		}
	}
}

func TestEventStream(t *testing.T) {
	e := newEnv(t, true)
	reader := e.token(TierRead, "")
	for i := 0; i < 3; i++ {
		e.events.Emit(e.ctx, "plan.created", "plan:"+strconv.Itoa(i+1), "system", map[string]int{"i": i})
	}
	// since: resume after seq 1.
	s := e.stream(reader, "?since=1", "")
	for _, want := range []string{"2", "3"} {
		if ev := s.nextEvent(t); ev.id != want || ev.typ != "plan.created" || ev.data.Seq != mustInt(want) || ev.data.Subject != "plan:"+want {
			t.Fatalf("since: got %+v, want id %s", ev, want)
		}
	}
	// Live events follow.
	e.events.Emit(e.ctx, "issue.opened", "issue:9", "agent:web1", nil)
	if ev := s.nextEvent(t); ev.id != "4" || ev.typ != "issue.opened" || string(ev.data.Data) != "{}" {
		t.Fatalf("live: %+v", ev)
	}
	// Keep-alive comments come while nothing happens.
	if ev := s.next(t); !ev.ping {
		t.Errorf("want a keep-alive, got %+v", ev)
	}
	// Last-Event-ID: a browser reconnect.
	s2 := e.stream(reader, "", "3")
	if ev := s2.nextEvent(t); ev.id != "4" {
		t.Errorf("Last-Event-ID 3: got %+v", ev)
	}
	// Without both: only new events.
	s3 := e.stream(reader, "", "")
	e.events.Emit(e.ctx, "plan.stale", "plan:1", "system", nil)
	if ev := s3.nextEvent(t); ev.id != "5" {
		t.Errorf("new only: got %+v", ev)
	}
	if code, _ := reader.do("GET", "/api/v1/events?since=x", nil); code != 400 {
		t.Errorf("bad since: %d", code)
	}
}

func mustInt(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

func TestUI(t *testing.T) {
	e := newEnv(t, true)
	e.srv.cfg.UI = fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><div id=app></div>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	h := e.srv.Handler()
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	for _, path := range []string{"/", "/index.html", "/targets/web1", "/issues/7?x=1"} {
		rec := get(path)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<div id=app>") || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body)
		}
	}
	if rec := get("/assets/app.js"); rec.Code != 200 || rec.Body.String() != "console.log(1)" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %d %q", rec.Code, rec.Body)
	}
	if rec := get("/api/v1/nothing"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "urn:plumb:problem:not-found") {
		t.Errorf("unknown API path falls back to the UI: %d", rec.Code)
	}
	// An unclean path is redirected to its clean form; nothing outside the UI is served.
	if rec := get("/../../etc/passwd"); strings.Contains(rec.Body.String(), "root:") || (rec.Code != 200 && rec.Code != 307) {
		t.Errorf("path escape: %d %q", rec.Code, rec.Body)
	}
	rec := get("/")
	for _, hd := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options"} {
		if rec.Header().Get(hd) == "" {
			t.Errorf("no %s header", hd)
		}
	}
}

// Review T-010 m5: a pending Plan can be dropped; its Issue opens again.
func TestDiscardPlan(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	a := e.agents["web1"]
	a.Change(context.Background(), "file:/etc/motd", map[string]any{"path": "/etc/motd", "content": "x"})
	var iss store.Issue
	deadline := time.Now().Add(5 * time.Second)
	for {
		var err error
		if iss, err = e.st.OpenIssue(e.ctx, "web1", "file:/etc/motd"); err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	code, body := adm.do("POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", map[string]string{"action": issue.ActionRevert})
	if code != 201 {
		t.Fatalf("resolve: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code, _ := adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil); code != 204 {
		t.Fatalf("discard: %d", code)
	}
	if got, _ := e.st.GetIssue(e.ctx, iss.ID); got.Status != store.IssueOpen {
		t.Errorf("issue after discard: %s", got.Status)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 409 {
		t.Errorf("approve a discarded plan: %d %s", code, body)
	}
	if code, body := adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil); code != 409 || problemType(body) != "plan-not-pending" {
		t.Errorf("discard twice: %d %s", code, body)
	}
}
