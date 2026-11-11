package issue_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// call sends one agent request with a copied credential, from its own
// client address (the bad-bearer limit of D-157 counts per client).
func (e *env) call(method, path, cred string, body any) (int, []byte) {
	e.t.Helper()
	var r *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	} else {
		r = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(e.ctx, method, path, r)
	req.RemoteAddr = "10.99.0.66:4000"
	if cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	rec := httptest.NewRecorder()
	e.link.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

var (
	helloReport = wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "test", Hostname: "old", OS: "fake 1"}}
	driftReport = wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: "file:/etc/motd", Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "content", From: "hello a", To: "evil", Risk: wire.RiskReversible}}}}}
)

// oldCredentialDead checks that a credential gets 401 on poll, hello and
// drift report, and never a command (D-211).
func (e *env) oldCredentialDead(when, cred string) {
	e.t.Helper()
	if code, body := e.call("GET", wire.PathPoll, cred, nil); code != http.StatusUnauthorized {
		e.t.Errorf("%s: poll with the old credential: %d %s, want 401", when, code, body)
	}
	if code, _ := e.call("POST", wire.PathReport, cred, helloReport); code != http.StatusUnauthorized {
		e.t.Errorf("%s: hello with the old credential: %d, want 401", when, code)
	}
	if code, _ := e.call("POST", wire.PathReport, cred, driftReport); code != http.StatusUnauthorized {
		e.t.Errorf("%s: drift with the old credential: %d, want 401", when, code)
	}
}

// deleteTarget plans and approves the delete of a target.
func (e *env) deleteTarget(name string) store.Run {
	e.t.Helper()
	return e.approve(e.propose(plan.Proposal{Delete: []plan.DocKey{{Kind: state.KindTarget, Name: name}}}).ID)
}

// stealCredential stops the agent of a target and returns its
// credential, like a copy in a backup or a template of the guest.
func (e *env) stealCredential(target string) string {
	e.t.Helper()
	cred := e.agents[target].Credential()
	if cred == "" {
		e.t.Fatalf("%s has no credential", target)
	}
	e.stop(target)
	return cred
}

func TestTargetDeleteRevokesAgent(t *testing.T) {
	e := newEnv(t)
	e.seed("a")
	cred := e.stealCredential("a")
	if code, _ := e.call("GET", wire.PathPoll, cred, nil); code != http.StatusNoContent {
		t.Fatalf("poll before the delete: %d", code)
	}
	if run := e.deleteTarget("a"); run.Status != "succeeded" {
		t.Fatalf("delete run: %s", run.Status)
	}
	if _, err := e.st.AgentByTarget(e.ctx, "a"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("agent row after the delete: %v", err)
	}
	e.oldCredentialDead("after the delete", cred)

	// Also after a controller restart (no memory of the revoke).
	e.eng.Close()
	e.start()
	e.oldCredentialDead("after a restart", cred)
}

func TestTargetReaddOldCredential(t *testing.T) {
	e := newEnv(t)
	e.seed("a")
	cred := e.stealCredential("a")
	if run := e.deleteTarget("a"); run.Status != "succeeded" {
		t.Fatalf("delete run: %s", run.Status)
	}

	// The Target comes back with the same name.
	p := e.propose(plan.Proposal{Put: []state.Doc{target("a", "secret text", 10)}})
	e.oldCredentialDead("before the approval", cred)

	// During the create: when the guest is made, and when the new agent
	// has enrolled but the Run is not done.
	var codes []int
	e.drv.Hook(fake.OpCreate, func() {
		for _, req := range []struct {
			method, path string
			body         any
		}{{"GET", wire.PathPoll, nil}, {"POST", wire.PathReport, helloReport}, {"POST", wire.PathReport, driftReport}} {
			c, _ := e.call(req.method, req.path, cred, req.body)
			codes = append(codes, c)
		}
	})
	e.agent("a")
	if run := e.approve(p.ID); run.Status != "succeeded" {
		t.Fatalf("create run: %s", run.Status)
	}
	if len(codes) != 3 {
		t.Fatalf("hook ran %d calls, want 3", len(codes))
	}
	for i, c := range codes {
		if c != http.StatusUnauthorized {
			t.Errorf("call %d during the create: %d, want 401", i, c)
		}
	}
	e.online("a")
	e.oldCredentialDead("after the enrollment", cred)
	if _, err := e.st.OpenIssue(e.ctx, "a", "file:/etc/motd"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the old agent made an Issue: %v", err)
	}
}

// An agent of a target without a guest binding: its drift makes no Issue
// and no event, and its hello gets no watch list (D-211).
func TestReportWithoutBindingDropped(t *testing.T) {
	e := newEnv(t)
	e.seed("a")
	tok, _, err := e.link.NewEnrollToken(e.ctx, "ghost")
	if err != nil {
		t.Fatal(err)
	}
	code, body := e.call("POST", wire.PathEnroll, "", wire.EnrollRequest{Token: tok, Hostname: "ghost", Version: "test"})
	var resp wire.EnrollResponse
	json.Unmarshal(body, &resp)
	if code != http.StatusOK {
		t.Fatalf("enroll: %d %s", code, body)
	}
	before, err := e.st.LastEventSeq(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := e.call("POST", wire.PathReport, resp.Credential, driftReport); code != http.StatusNoContent {
		t.Errorf("drift: %d", code)
	}
	if code, _ := e.call("POST", wire.PathReport, resp.Credential, helloReport); code != http.StatusNoContent {
		t.Errorf("hello: %d", code)
	}
	if code, body := e.call("GET", wire.PathPoll, resp.Credential, nil); code != http.StatusNoContent {
		t.Errorf("poll after hello: %d %s, want 204 (no watch list)", code, body)
	}
	if _, err := e.st.OpenIssue(e.ctx, "ghost", "file:/etc/motd"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Issue for a target without a binding: %v", err)
	}
	after, _ := e.st.LastEventSeq(e.ctx)
	if after != before {
		evs, _ := e.st.EventsAfter(context.Background(), before, 10)
		t.Errorf("events for a target without a binding: %+v", evs)
	}
}

// A delete Run that fails (the guest is not gone) keeps the agent.
func TestFailedDeleteKeepsAgent(t *testing.T) {
	e := newEnv(t)
	e.seed("a")
	cred := e.stealCredential("a")
	e.drv.Fail(fake.OpDelete, errors.New("host busy"))
	if run := e.deleteTarget("a"); run.Status == "succeeded" {
		t.Fatal("delete run succeeded with a failing driver")
	}
	if _, err := e.st.AgentByTarget(e.ctx, "a"); err != nil {
		t.Errorf("agent row after a failed delete: %v", err)
	}
	if _, err := e.st.GetBinding(e.ctx, state.KindTarget, "a"); err != nil {
		t.Errorf("binding after a failed delete: %v", err)
	}
	if code, _ := e.call("GET", wire.PathPoll, cred, nil); code != http.StatusNoContent && code != http.StatusOK {
		t.Errorf("poll after a failed delete: %d, want 204 or 200", code)
	}
}
