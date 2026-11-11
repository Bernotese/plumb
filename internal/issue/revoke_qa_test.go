package issue_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// A desired Target can exist before its guest is bound. Unlike a ghost
// name, it has a render that would leak through an unguarded hello.
func TestHelloWithoutBindingWithDesiredState(t *testing.T) {
	e := newEnv(t)
	e.seed("a")
	cred := e.stealCredential("a")
	// Drain any watch queued by the boot hello before removing the binding.
	for i := 0; ; i++ {
		code, _ := e.call("GET", wire.PathPoll, cred, nil)
		if code == http.StatusNoContent {
			break
		}
		if code != http.StatusOK || i == 10 {
			t.Fatalf("drain poll: %d", code)
		}
	}
	if err := e.st.DeleteBinding(e.ctx, state.KindTarget, "a"); err != nil {
		t.Fatal(err)
	}
	before, err := e.st.LastEventSeq(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := e.call("POST", wire.PathReport, cred, helloReport); code != http.StatusNoContent {
		t.Fatalf("hello: %d", code)
	}
	if code, body := e.call("GET", wire.PathPoll, cred, nil); code != http.StatusNoContent {
		t.Errorf("hello without binding leaked a watch: %d %s", code, body)
	}
	if after, err := e.st.LastEventSeq(e.ctx); err != nil || after != before {
		t.Errorf("hello emitted events: before=%d after=%d err=%v", before, after, err)
	}
}

type failingRevoker struct {
	plan.Agents
	revoke func(context.Context, string) error
}

func (a failingRevoker) Revoke(ctx context.Context, target string) error {
	return a.revoke(ctx, target)
}

// Revoke must run while the binding exists. If it fails, retain the
// binding so a retry can revoke the credential before losing the guest ref.
func TestDeleteRevokeFailureKeepsBinding(t *testing.T) {
	var e *env
	calls := 0
	e = newEnv(t, func(cfg *plan.Config) {
		cfg.Agents = failingRevoker{Agents: cfg.Agents, revoke: func(ctx context.Context, target string) error {
			calls++
			if _, err := e.st.GetBinding(ctx, state.KindTarget, target); err != nil {
				t.Errorf("binding at revoke: %v", err)
			}
			return errors.New("revoke unavailable")
		}}
	})
	e.seed("a")
	cred := e.stealCredential("a")
	if run := e.deleteTarget("a"); run.Status != "failed" {
		t.Errorf("run: %s, want failed", run.Status)
	}
	if calls != 1 {
		t.Errorf("revoke calls: %d, want 1", calls)
	}
	if _, err := e.st.GetBinding(e.ctx, state.KindTarget, "a"); err != nil {
		t.Errorf("binding after revoke failure: %v", err)
	}
	if _, err := e.st.AgentByTarget(e.ctx, "a"); err != nil {
		t.Errorf("agent after revoke failure: %v", err)
	}
	if code, _ := e.call("GET", wire.PathPoll, cred, nil); code != http.StatusOK && code != http.StatusNoContent {
		t.Errorf("poll after revoke failure: %d", code)
	}
	if _, err := e.st.OpenIssue(e.ctx, "a", "file:/etc/motd"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unexpected issue: %v", err)
	}
}
