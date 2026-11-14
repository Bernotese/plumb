package mcp

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// validArgs are arguments that pass the input checks of each tool, so the
// call reaches the API (T-186 QA).
var validArgs = map[string]map[string]any{
	"get_target": {"name": "web1"}, "get_issue": {"id": 1}, "get_plan": {"id": 1}, "get_run": {"id": 1},
	"diff_revision": {"n": 2}, "get_block": {"name": "motd"}, "get_image": {"name": "img"},
	"events_since":  {"since": 0},
	"propose":       {"reason": "qa", "delete": []map[string]any{{"kind": "Block", "name": "motd"}}},
	"resolve_issue": {"id": 1, "action": "revert"}, "reenroll": {"name": "web1"},
	"drop_plan": {"id": 1}, "comment_issue": {"id": 1, "body": "qa"},
}

// The token is never in a tool result or tool error, whatever the API
// answers: success, every problem status, a non-JSON body, a cut answer, a
// broken connection (D-364, T-186).
func TestQATokenNeverInToolOutput(t *testing.T) {
	answers := map[string]http.HandlerFunc{
		"ok": okJSON(`{"items":[],"next":null}`),
		"problem": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"type":"urn:plumb:problem:forbidden","title":"Forbidden","status":403}`)
		},
		"429": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
		},
		"500 text": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "<html>oops</html>")
		},
		"big": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"x":"`+strings.Repeat("a", MaxResult+10)+`"}`)
		},
		"hangup": func(w http.ResponseWriter, r *http.Request) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				return
			}
			c, _, _ := hj.Hijack()
			c.Close()
		},
	}
	for name, h := range answers {
		t.Run(name, func(t *testing.T) {
			f := newFakeAPI(t, h)
			cs := session(t, f)
			for _, tool := range ToolNames {
				res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: validArgs[tool]})
				text := ""
				if err != nil {
					text = err.Error()
				} else {
					for _, c := range res.Content {
						if tc, ok := c.(*mcp.TextContent); ok {
							text += tc.Text
						}
					}
				}
				if strings.Contains(text, testToken) || strings.Contains(text, "s3cret") {
					t.Errorf("%s: the token is in the output: %.200s", tool, text)
				}
			}
			// Every tool reached the API (its inputs passed), so the sweep
			// covers every tool's answer path.
			f.mu.Lock()
			n := len(f.calls)
			f.mu.Unlock()
			if n != len(ToolNames) {
				t.Fatalf("%d API calls for %d tools: %v", n, len(ToolNames), f.calls)
			}
		})
	}
}

// A listener that accepts and closes at once: a transport error with the
// URL in it; the token is not in the URL, so not in the error.
func TestQATokenNotInTransportError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	c, err := NewClient(ClientConfig{URL: "http://" + l.Addr().String(), Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(context.Background(), http.MethodGet, "/api/v1/targets", nil, nil)
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("transport error: %v", err)
	}
}
