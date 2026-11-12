package issue_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func ctRequest(e *env, path, credential string, body any) int {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	res := httptest.NewRecorder()
	e.link.Handler().ServeHTTP(res, req)
	return res.Code
}

// The clear must follow a hello even if the driver rejects the clear.
// Its failure must not make the already consumed token usable again.
func TestLXCClearAfterHelloQA(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			e, _ := newCTEnv(t)
			e.agent("c1")
			checked := make(chan bool, 1)
			e.drv.Hook(fake.OpClearBootstrap, func() {
				a, err := e.st.AgentByTarget(e.ctx, "c1")
				boot, ok := e.drv.Bootstrap(e.guestID("c1"))
				hello := err == nil && !a.LastSeenAt.IsZero() && e.link.Online("c1")
				rejected := ok && ctRequest(e, wire.PathEnroll, "", wire.EnrollRequest{Token: boot.Token}) == http.StatusUnauthorized
				checked <- hello && rejected
			})
			if fail {
				e.drv.Fail(fake.OpClearBootstrap, errors.New("config locked"))
			}
			run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, false))}).ID)
			select {
			case ok := <-checked:
				if !ok {
					t.Error("clear ran before hello or accepted the used token")
				}
			default:
				t.Fatal("clear was not attempted")
			}
			want := "succeeded"
			if fail {
				want = "failed"
			}
			if run.Status != want {
				t.Fatalf("run = %s, want %s", run.Status, want)
			}
			if e.observe("c1").TokenInConfig != fail {
				t.Error("unexpected token config after clear")
			}
		})
	}
}

// Cancel a create while it waits for its first hello. Keep the bound
// container for recovery, but do not clear its unused enrollment token.
func TestLXCCancelBeforeHelloQA(t *testing.T) {
	e := newEnv(t, func(c *plan.Config) { c.EnrollTimeout = 10 * time.Second })
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for e.ctx.Err() == nil {
			if _, err := e.st.GetBinding(e.ctx, state.KindTarget, "c1"); err == nil {
				e.eng.Stop()
				return
			}
			sleep(e.ctx, time.Millisecond)
		}
	}()
	run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, true))}).ID)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("create did not reach a bound guest")
	}
	if run.Status != "failed" {
		t.Fatalf("cancelled run = %s", run.Status)
	}
	if e.drv.Calls(fake.OpCreate) != 1 || e.drv.Calls(fake.OpClearBootstrap) != 0 {
		t.Fatal("cancelled create retried creation or cleared an unused token")
	}
	if !e.observe("c1").TokenInConfig {
		t.Error("unused token lost before hello")
	}
	for _, s := range e.steps(run) {
		if s.Status == "running" || s.Status == "pending" {
			t.Errorf("unfinished step: %s=%s", s.Resource, s.Status)
		}
	}
	if _, err := e.st.AgentByTarget(e.ctx, "c1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("agent before hello: %v", err)
	}
}
