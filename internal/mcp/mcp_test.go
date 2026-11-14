package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testToken = "tok_test.s3cret"

// fakeAPI records the calls and answers with handler.
type fakeAPI struct {
	mu    sync.Mutex
	calls []string // "METHOD path?query body"
	srv   *httptest.Server
}

func newFakeAPI(t *testing.T, h http.HandlerFunc) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		call := r.Method + " " + r.URL.Path
		if r.URL.RawQuery != "" {
			call += "?" + r.URL.RawQuery
		}
		if len(b) > 0 {
			call += " " + string(b)
		}
		f.calls = append(f.calls, call)
		f.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

// session connects an MCP client to a server over the fake API.
func session(t *testing.T, f *fakeAPI) *mcp.ClientSession {
	t.Helper()
	c, err := NewClient(ClientConfig{URL: f.srv.URL, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := NewServer(c, "test").Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return res, text
}

func okJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}
}

// The tool set is fixed (D-364, ARCHITECTURE §18): no approve, no ignore,
// no secret, token, user, settings, export, import, backup or restore.
func TestToolListIsFixed(t *testing.T) {
	cs := session(t, newFakeAPI(t, okJSON(`{}`)))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		for _, bad := range []string{"approve", "ignore", "secret", "token", "user", "setting", "export", "import", "backup", "restore", "build", "refresh", "agent_update"} {
			if strings.Contains(tool.Name, bad) {
				t.Errorf("tool %s must not exist", tool.Name)
			}
		}
		if tool.Name != "drop_plan" && !strings.Contains(tool.Description, "untrusted data") {
			t.Errorf("tool %s does not mark guest data as untrusted", tool.Name)
		}
	}
	want := slices.Clone(ToolNames)
	slices.Sort(want)
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
	// resolve_issue offers only accept and revert.
	_, text := callTool(t, cs, "resolve_issue", map[string]any{"id": 3, "action": "ignore_once"})
	if !strings.Contains(text, "accept") {
		t.Fatalf("ignore_once through resolve_issue: %s", text)
	}
}

func TestToolsCallTheAPI(t *testing.T) {
	f := newFakeAPI(t, okJSON(`{"items":[],"next":null}`))
	cs := session(t, f)
	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"list_targets", nil, "GET /api/v1/targets"},
		{"get_target", map[string]any{"name": "web1"}, "GET /api/v1/targets/web1"},
		{"get_target", map[string]any{"name": "web.lab-1"}, "GET /api/v1/targets/web.lab-1"},
		{"list_issues", map[string]any{"target": "web1", "status": "open"}, "GET /api/v1/issues?status=open&target=web1"},
		{"get_issue", map[string]any{"id": 7}, "GET /api/v1/issues/7"},
		{"list_plans", map[string]any{"status": "pending", "limit": 5}, "GET /api/v1/plans?limit=5&status=pending"},
		{"get_plan", map[string]any{"id": 12}, "GET /api/v1/plans/12"},
		{"list_runs", nil, "GET /api/v1/runs"},
		{"get_run", map[string]any{"id": 2}, "GET /api/v1/runs/2"},
		{"list_revisions", map[string]any{"cursor": "abc"}, "GET /api/v1/revisions?cursor=abc"},
		{"diff_revision", map[string]any{"n": 9, "from": 4}, "GET /api/v1/revisions/9/diff?from=4"},
		{"list_blocks", nil, "GET /api/v1/blocks"},
		{"get_block", map[string]any{"name": "motd"}, "GET /api/v1/blocks/motd"},
		{"list_images", nil, "GET /api/v1/images"},
		{"get_image", map[string]any{"name": "ubuntu"}, "GET /api/v1/images/ubuntu"},
		{"list_catalog", nil, "GET /api/v1/catalog/blocks"},
		{"propose", map[string]any{"reason": "r", "delete": []map[string]string{{"kind": "Target", "name": "web1"}}},
			`POST /api/v1/plans {"delete":[{"kind":"Target","name":"web1"}],"put":[],"reason":"r"}`},
		{"propose", map[string]any{"reason": "r", "put": []map[string]any{{"kind": "Block", "name": "b", "spec": map[string]any{"resources": []any{}}}}},
			`POST /api/v1/plans {"delete":[],"put":[{"kind":"Block","name":"b","spec":{"resources":[]}}],"reason":"r"}`},
		{"resolve_issue", map[string]any{"id": 7, "action": "revert"}, `POST /api/v1/issues/7/resolve {"action":"revert","reason":""}`},
		{"reenroll", map[string]any{"name": "web1"}, `POST /api/v1/targets/web1/reenroll {"reason":""}`},
		{"drop_plan", map[string]any{"id": 12}, "DELETE /api/v1/plans/12"},
		{"comment_issue", map[string]any{"id": 7, "body": "checked"}, `POST /api/v1/issues/7/comments {"body":"checked"}`},
	} {
		res, text := callTool(t, cs, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s: error %s", c.tool, text)
			continue
		}
		if got := f.last(); got != c.want {
			t.Errorf("%s: call %q, want %q", c.tool, got, c.want)
		}
		var r result
		if err := json.Unmarshal([]byte(text), &r); err != nil || r.Status != 200 || r.Truncated || string(r.Data) != `{"items":[],"next":null}` {
			t.Errorf("%s: result %s (%v)", c.tool, text, err)
		}
	}
}

// Bad inputs never reach the API: no path tricks, no free actions.
func TestInputsChecked(t *testing.T) {
	f := newFakeAPI(t, okJSON(`{}`))
	cs := session(t, f)
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"get_target", map[string]any{"name": "../tokens"}},
		{"get_target", map[string]any{"name": "web1?x=1"}},
		{"get_target", map[string]any{"name": "a/b"}},
		{"get_target", map[string]any{"name": ".hidden"}},
		{"get_issue", map[string]any{"id": 0}},
		{"get_issue", map[string]any{"id": -3}},
		{"list_issues", map[string]any{"status": "all"}},
		{"list_plans", map[string]any{"limit": 500}},
		{"resolve_issue", map[string]any{"id": 1, "action": "ignore_rule"}},
		{"propose", map[string]any{"reason": "r"}},
		{"propose", map[string]any{"reason": "", "delete": []map[string]string{{"kind": "Target", "name": "x"}}}},
		{"comment_issue", map[string]any{"id": 1, "body": ""}},
		{"events_since", map[string]any{"since": -1}},
	} {
		res, text := callTool(t, cs, c.tool, c.args)
		if !res.IsError {
			t.Errorf("%s %v: accepted: %s", c.tool, c.args, text)
		}
	}
	if got := f.last(); got != "" {
		t.Fatalf("a refused input reached the API: %s", got)
	}
}

// A long answer is cut at MaxResult and marked (D-364).
func TestResultTruncated(t *testing.T) {
	big := `{"items":["` + strings.Repeat("x", MaxResult) + `"]}`
	cs := session(t, newFakeAPI(t, okJSON(big)))
	res, text := callTool(t, cs, "list_targets", nil)
	var r result
	if err := json.Unmarshal([]byte(text), &r); err != nil || res.IsError {
		t.Fatalf("%v %v", err, res.IsError)
	}
	if !r.Truncated || len(r.DataText) != MaxResult || r.Data != nil {
		t.Fatalf("truncated=%v len=%d data=%d", r.Truncated, len(r.DataText), len(r.Data))
	}
}

// A refused call is a tool error with the problem and, for 429, the wait.
func TestAPIErrors(t *testing.T) {
	cs := session(t, newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		if r.Method == http.MethodPost {
			w.Header().Set("Retry-After", "17")
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"type":"urn:plumb:problem:rate-limited","title":"Too many requests","status":429}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"type":"urn:plumb:problem:forbidden","title":"Forbidden","status":403,"detail":"needs tier plan"}`)
	}))
	res, text := callTool(t, cs, "comment_issue", map[string]any{"id": 1, "body": "x"})
	if !res.IsError || !strings.Contains(text, "429") || !strings.Contains(text, "rate-limited") || !strings.Contains(text, "Wait 17 seconds") {
		t.Fatalf("429: %v %s", res.IsError, text)
	}
	res, text = callTool(t, cs, "get_plan", map[string]any{"id": 1})
	if !res.IsError || !strings.Contains(text, "403") || !strings.Contains(text, "needs tier plan") {
		t.Fatalf("403: %v %s", res.IsError, text)
	}
	if strings.Contains(text, testToken) {
		t.Fatal("the token is in a tool result")
	}
}

func TestEventsSince(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ": plumb events\n\n")
		io.WriteString(w, "id: 5\nevent: issue.opened\ndata: {\"subject\":\"issue:3\"}\n\n")
		io.WriteString(w, ": keep-alive\n\n")
		io.WriteString(w, "id: 6\nevent: plan.created\ndata: {\"subject\":\"plan:9\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // the stream stays open; the tool closes it
	})
	cs := session(t, f)
	start := time.Now()
	_, text := callTool(t, cs, "events_since", map[string]any{"since": 4})
	if d := time.Since(start); d > eventWait+2*time.Second {
		t.Fatalf("events_since took %s", d)
	}
	if !strings.HasPrefix(f.last(), "GET /api/v1/events?since=4") {
		t.Fatalf("call %s", f.last())
	}
	var r struct {
		Data eventsPage
	}
	if err := json.Unmarshal([]byte(text), &r); err != nil || len(r.Data.Events) != 2 || r.Data.Events[1].Seq != 6 ||
		r.Data.Events[0].Type != "issue.opened" || r.Data.Cut || r.Data.Next != 6 {
		t.Fatalf("events %s (%v)", text, err)
	}
}

// events_since is cut like every other result (review n1 of #280): one
// event too big keeps seq and type without data; many events stop at
// MaxResult with cut and the next cursor.
func TestCutEvents(t *testing.T) {
	big := json.RawMessage(`"` + strings.Repeat("x", maxEventData+10) + `"`)
	mid := json.RawMessage(`"` + strings.Repeat("y", maxEventData-10) + `"`)
	var evs []Event
	evs = append(evs, Event{Seq: 10, Type: "a", Data: big})
	for i := int64(11); i < 30; i++ {
		evs = append(evs, Event{Seq: i, Type: "b", Data: mid})
	}
	p := cutEvents(evs, 9)
	b, _ := json.Marshal(p)
	if len(b) > MaxResult {
		t.Fatalf("result %d bytes > %d", len(b), MaxResult)
	}
	if !p.Cut || p.Next == 9 || p.Next != p.Events[len(p.Events)-1].Seq {
		t.Fatalf("cut=%v next=%d last=%d", p.Cut, p.Next, p.Events[len(p.Events)-1].Seq)
	}
	if !p.Events[0].DataCut || string(p.Events[0].Data) != "null" || p.Events[0].Seq != 10 {
		t.Fatalf("big event %+v", p.Events[0].Seq)
	}
	// Data that JSON escapes (<, >, &) grows when marshalled; the result
	// still fits.
	esc := json.RawMessage(`"` + strings.Repeat("<", maxEventData-10) + `"`)
	var many []Event
	for i := int64(1); i <= 20; i++ {
		many = append(many, Event{Seq: i, Type: "c", Data: esc})
	}
	page := cutEvents(many, 0)
	res, _ := json.Marshal(result{Status: 200, Data: mustJSON(page)})
	if len(res) > MaxResult || !page.Cut || page.Next != page.Events[len(page.Events)-1].Seq {
		t.Fatalf("escaped data: %d bytes, cut=%v", len(res), page.Cut)
	}
	if q := cutEvents(nil, 42); q.Cut || q.Next != 42 || len(q.Events) != 0 {
		t.Fatalf("empty %+v", q)
	}
}

func TestReadEventsBounds(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 150; i++ {
		b.WriteString("id: 1\nevent: x\ndata: {}\n\n")
	}
	if got := readEvents(strings.NewReader(b.String()), maxEvents); len(got) != maxEvents {
		t.Fatalf("%d events", len(got))
	}
	// A data line that is not JSON, or an endless line, ends the read.
	if got := readEvents(strings.NewReader("id: 1\nevent: x\ndata: {not json\n\n"), 10); len(got) != 0 {
		t.Fatal(got)
	}
	if got := readEvents(strings.NewReader("data: "+strings.Repeat("a", 2<<20)), 10); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestNewClientRules(t *testing.T) {
	for _, c := range []struct {
		cfg  ClientConfig
		want string
	}{
		{ClientConfig{URL: "http://plumb.lan:8443", Token: "t"}, "loopback"},
		{ClientConfig{URL: "https://plumb.lan:8443", Token: "t"}, "CA certificate"},
		{ClientConfig{URL: "https://plumb.lan:8443", Token: "t", CAPEM: []byte("no pem")}, "no PEM"},
		{ClientConfig{URL: "http://127.0.0.1:8443", Token: ""}, "token"},
		{ClientConfig{URL: "ftp://127.0.0.1", Token: "t"}, "http(s)"},
		{ClientConfig{URL: "http://user:pw@127.0.0.1:8443", Token: "t"}, "http(s)"},
		{ClientConfig{URL: "http://127.0.0.1:8443/api", Token: "t"}, "http(s)"},
	} {
		if _, err := NewClient(c.cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.cfg.URL, err, c.want)
		}
	}
	for _, u := range []string{"http://127.0.0.1:8443", "http://localhost:8443", "http://[::1]:8443/"} {
		if _, err := NewClient(ClientConfig{URL: u, Token: "t"}); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

// A redirect is never followed: the token must not go to another host.
func TestNoRedirect(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the redirect was followed (Authorization %q)", r.Header.Get("Authorization"))
	}))
	defer other.Close()
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	})
	cs := session(t, f)
	if res, _ := callTool(t, cs, "list_targets", nil); !res.IsError {
		t.Fatal("a redirect answer is no error")
	}
}

// An inbound line longer than MaxLineLength ends the session instead of
// growing the buffer (review n1 of #276).
func TestMaxLineLength(t *testing.T) {
	c, err := NewClient(ClientConfig{URL: "http://127.0.0.1:1", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), c, "test", pr, nopWriteCloser{io.Discard}) }()
	go func() {
		// A JSON-RPC frame whose string never ends: only the limit stops it.
		pw.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"x":"` + strings.Repeat("a", MaxLineLength+1024)))
		pw.Close()
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "maximum line length") {
			t.Fatalf("Run ended without the line-length error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not end on an endless line")
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
