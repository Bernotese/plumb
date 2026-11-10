package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"
)

// devAgentRuns counts the goroutines that run a simulated agent. The
// count is global for the process: tests that use it must not run in
// parallel.
func devAgentRuns() int {
	var b bytes.Buffer
	pprof.Lookup("goroutine").WriteTo(&b, 2)
	return strings.Count(b.String(), "linktest.(*Agent).Run(")
}

func eventually(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// T-024: in --dev a target created through the API gets a simulated agent,
// its Run succeeds, and deleting the target stops the agent.
func TestDevAgentForNewTarget(t *testing.T) {
	if n := devAgentRuns(); n != 0 {
		t.Fatalf("%d agents run before the test", n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f, err := parseServe([]string{"--dev", "--dev-demo", "--data", t.TempDir(), "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := runServer(ctx, f, pw, slog.New(slog.NewTextHandler(io.Discard, nil)))
		pw.Close()
		done <- err
	}()
	var base, login string
	var printed lockedBuffer // everything the server prints, for the bearer check
	sc := bufio.NewScanner(pr)
	for login == "" && sc.Scan() {
		line := sc.Text()
		printed.Write([]byte(line + "\n"))
		if rest, ok := strings.CutPrefix(line, "Plumb listens on "); ok {
			base, _, _ = strings.Cut(rest, " ")
		}
		if rest, ok := strings.CutPrefix(line, "dev login: "); ok {
			login = rest
		}
	}
	go io.Copy(&printed, pr)
	if base == "" || login == "" {
		t.Fatalf("no listen address or login link (%v)", sc.Err())
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	if resp, err := c.Get(login); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	call := func(method, path string, body, out any) int {
		t.Helper()
		var r io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			r = bytes.NewReader(raw)
		}
		req, _ := http.NewRequestWithContext(ctx, method, base+path, r)
		req.Header.Set("Origin", base)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if out != nil && resp.StatusCode < 300 {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatalf("%s %s: %v: %s", method, path, err, raw)
			}
		}
		if resp.StatusCode >= 300 {
			t.Logf("%s %s: %d %s", method, path, resp.StatusCode, raw)
		}
		return resp.StatusCode
	}
	// The seed is done when the demo Plan is pending. The seed Plan is
	// pending too for a moment, so match the demo Plan by its reason.
	eventually(t, "demo plan", 30*time.Second, func() bool {
		var pl struct{ Items []struct{ Reason string } }
		if call("GET", "/api/v1/plans?status=pending", nil, &pl) != 200 {
			return false
		}
		for _, p := range pl.Items {
			if strings.HasPrefix(p.Reason, "demo:") {
				return true
			}
		}
		return false
	})
	eventually(t, "2 seed agents", 5*time.Second, func() bool { return devAgentRuns() == 2 })

	// --dev-demo seeds one plan token; the server never prints its bearer (T-030).
	var toks struct {
		Items []struct{ ID, Tier string }
	}
	eventually(t, "demo token", 10*time.Second, func() bool {
		return call("GET", "/api/v1/tokens", nil, &toks) == 200 && len(toks.Items) == 1
	})
	if toks.Items[0].Tier != "plan" {
		t.Errorf("demo token: %+v, want one plan token", toks.Items)
	}
	if out := printed.String(); strings.Contains(out, toks.Items[0].ID+".") {
		t.Errorf("the server printed the demo bearer: %s", out)
	}

	apply := func(reason string, body map[string]any) {
		t.Helper()
		body["reason"] = reason
		var p struct{ ID int64 }
		if code := call("POST", "/api/v1/plans", body, &p); code != 201 {
			t.Fatalf("%s: plan %d", reason, code)
		}
		var run struct{ ID int64 }
		if code := call("POST", fmt.Sprintf("/api/v1/plans/%d/approve", p.ID), map[string]any{}, &run); code >= 300 {
			t.Fatalf("%s: approve %d", reason, code)
		}
		var status string
		eventually(t, reason+": run succeeds", 30*time.Second, func() bool {
			var r struct{ Status string }
			call("GET", fmt.Sprintf("/api/v1/runs/%d", run.ID), nil, &r)
			status = r.Status
			return status != "running" && status != "pending" && status != ""
		})
		if status != "succeeded" {
			t.Fatalf("%s: run %s", reason, status)
		}
	}
	apply("add web3", map[string]any{"put": []any{map[string]any{"kind": "Target", "name": "web3", "spec": map[string]any{
		"type": "vm", "image": "ubuntu-26.04",
		"guest":  map[string]any{"cores": 1, "memory": 1024, "disk": 10, "bridge": "vmbr0"},
		"blocks": []any{map[string]any{"use": "motd", "params": map[string]any{"text": "Hello from web3"}}},
	}}}})
	eventually(t, "agent of web3", 5*time.Second, func() bool { return devAgentRuns() == 3 })
	apply("delete web3", map[string]any{"delete": []any{map[string]any{"kind": "Target", "name": "web3"}}})
	eventually(t, "agent of web3 stops", 5*time.Second, func() bool { return devAgentRuns() == 2 })

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// runServer waits for its agents (review T-024 m1).
	if n := devAgentRuns(); n != 0 {
		t.Fatalf("after shutdown: %d agents run", n)
	}
}

// lockedBuffer is a bytes.Buffer for one writer goroutine and one reader.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
