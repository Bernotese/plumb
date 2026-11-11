package link

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Revoke (D-211): the agent row and the token go, a queued command is
// dropped, an open poll ends with 401, and every route answers 401.
func TestRevoke(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cred := f.agent("web1")
	if code, _ := f.do("GET", wire.PathPoll, cred, nil); code != 204 {
		t.Fatalf("poll: %d", code)
	}

	// An open poll ends with 401 when the agent is revoked.
	done := make(chan int, 1)
	go func() {
		code, _ := f.do("GET", wire.PathPoll+"?wait=30", cred, nil)
		done <- code
	}()
	time.Sleep(100 * time.Millisecond)
	if err := f.link.Revoke(ctx, "web1"); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 401 {
			t.Errorf("open poll ended with %d, want 401", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("open poll did not end after Revoke")
	}

	if _, err := f.st.AgentByTarget(ctx, "web1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("agent row after Revoke: %v", err)
	}
	if f.link.Online("web1") {
		t.Error("online after Revoke")
	}
	// A command queued after the revoke is not given to the old agent.
	f.link.Send("web1", wire.CommandWatch, wire.WatchBody{})
	for name, req := range map[string]struct {
		method, path string
		body         any
	}{
		"poll":   {"GET", wire.PathPoll, nil},
		"hello":  {"POST", wire.PathReport, wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "1"}}},
		"drift":  {"POST", wire.PathReport, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: "file:/x", Status: wire.StatusDiffers}}}},
		"result": {"POST", wire.PathReport, wire.Report{Type: wire.ReportResult, CommandID: "c1"}},
	} {
		if code, _ := f.do(req.method, req.path, cred, req.body); code != 401 {
			t.Errorf("%s with a revoked credential: %d, want 401", name, code)
		}
	}
	if len(f.got) != 0 {
		t.Errorf("deliveries after Revoke: %+v", f.got)
	}

	// An unused token of the target is removed too.
	tok := f.token("web2")
	if err := f.link.Revoke(ctx, "web2"); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.enroll(tok); code != 401 {
		t.Errorf("enroll with the token of a revoked target: %d, want 401", code)
	}
	// A target without agent or token is no error.
	if err := f.link.Revoke(ctx, "nobody"); err != nil {
		t.Errorf("revoke of an unknown target: %v", err)
	}
	if err := f.link.Revoke(ctx, ""); err == nil {
		t.Error("revoke without a target: want an error")
	}
}
