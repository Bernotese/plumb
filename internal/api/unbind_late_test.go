package api

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-210 (host findings F12, F11 of T-192). A short unbind timeout so the
// delete Run gives up while the agent is stopped.
func withShortUnbind(c *plan.Config) { c.UnbindTimeout = 300 * time.Millisecond }

// unmanagedEntry is one guest of GET /guests/unmanaged.
type unmanagedEntry struct {
	VMID      int
	Adoptable bool
	Reasons   []string
	AgentLeft bool `json:"agent_left"`
}

func (e *env) unmanaged(adm *client, vmid int) unmanagedEntry {
	e.t.Helper()
	_, body := adm.do("GET", "/api/v1/guests/unmanaged", nil)
	var list struct{ Items []unmanagedEntry }
	json.Unmarshal(body, &list)
	for _, g := range list.Items {
		if g.VMID == vmid {
			return g
		}
	}
	e.t.Fatalf("guest %d not unmanaged: %s", vmid, body)
	return unmanagedEntry{}
}

func countType(cmds []wire.CommandType, typ wire.CommandType) int {
	n := 0
	for _, c := range cmds {
		if c == typ {
			n++
		}
	}
	return n
}

// The host sequence: the agent stops (it still counts as online), the
// delete Run sends unbind, times out and parks the agent. The retry sends
// no second unbind while the first waits in the queue. The agent comes
// back, takes the queued unbind of the Run, runs it and answers: that
// late result ends the park (credential revoked, row gone, agent.unbound).
func TestParkEndsOnLateUnbindResult(t *testing.T) {
	e := newEnv(t, true, withInstall, withShortUnbind)
	adm := e.admin()
	vmid, a := e.adoptWithAgent(adm, "wiki")
	a.Pause(true) // stopped: no poll, but polled less than 90 s ago
	seq, _ := e.st.LastEventSeq(e.ctx)

	run := e.deleteTarget(adm, "wiki")
	if run.Status != "succeeded" || run.Warnings != 1 {
		t.Fatalf("run %+v", run)
	}
	g := e.unmanaged(adm, vmid)
	if !g.AgentLeft || g.Adoptable || !slices.Contains(g.Reasons, "agent-left") {
		t.Fatalf("parked guest in the list (F11): %+v", g)
	}
	if !e.link.Queued("wiki", wire.CommandUnbind) {
		t.Fatal("the unbind of the Run is not queued")
	}

	// The guest poll's retry: no second unbind while the first is queued.
	if err := e.eng.RetryUnbinds(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.link.QueuedCount("wiki", wire.CommandUnbind); n != 1 {
		t.Fatalf("%d unbind commands queued after the retry, want 1", n)
	}

	a.Pause(false)
	for deadline := time.Now().Add(10 * time.Second); e.unmanaged(adm, vmid).AgentLeft; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the park did not end after the agent answered the queued unbind")
		}
	}
	if !a.Unbound() {
		t.Fatal("the agent did not run the unbind")
	}
	if n := countType(a.Commands(), wire.CommandUnbind); n != 1 {
		t.Fatalf("the agent got %d unbind commands, want 1", n)
	}
	if _, err := e.st.AgentByTarget(e.ctx, "wiki"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the credential stays: %v", err)
	}
	if g := e.unmanaged(adm, vmid); !g.Adoptable || slices.Contains(g.Reasons, "agent-left") {
		t.Fatalf("after the park: %+v", g)
	}
	evs, _ := e.st.EventsAfter(e.ctx, seq, 500)
	if !slices.ContainsFunc(evs, func(ev store.Event) bool { return ev.Type == "agent.unbound" }) {
		t.Fatal("no agent.unbound event")
	}
}

// Only a confirmed unbind result of the parked agent itself ends a park:
// not a result for a command id the controller never sent, not a failed
// unbind, not a delivery of another agent id, not a result of another
// command type.
func TestLateUnbindEndsNothingElse(t *testing.T) {
	e := newEnv(t, true, withInstall, withShortUnbind)
	adm := e.admin()
	vmid, a := e.adoptWithAgent(adm, "wiki")
	a.Pause(true)
	if run := e.deleteTarget(adm, "wiki"); run.Warnings != 1 {
		t.Fatalf("run %+v", run)
	}
	parked := func() bool {
		ag, err := e.st.AgentByTarget(e.ctx, "wiki")
		if err != nil {
			t.Fatalf("the parked agent row is gone: %v", err)
		}
		ok, err := e.st.IsParked(e.ctx, "wiki", ag.ID)
		if err != nil {
			t.Fatal(err)
		}
		return ok && e.unmanaged(adm, vmid).AgentLeft
	}
	ag, _ := e.st.AgentByTarget(e.ctx, "wiki")

	// A result for a command id that was never sent to this agent.
	a.Report(e.ctx, wire.Report{Type: wire.ReportResult, CommandID: "forged-unbind-id", Items: []wire.Item{}})
	if !parked() {
		t.Fatal("a forged result ended the park")
	}
	// Deliveries the link would never make, straight to the engine: another
	// agent id, another command type, a failed unbind.
	for name, d := range map[string]link.Delivery{
		"other agent":  {Target: "wiki", AgentID: "another-agent", CommandType: wire.CommandUnbind, Report: wire.Report{Type: wire.ReportResult, CommandID: "x"}},
		"other type":   {Target: "wiki", AgentID: ag.ID, CommandType: wire.CommandObserve, Report: wire.Report{Type: wire.ReportResult, CommandID: "y"}},
		"failed":       {Target: "wiki", AgentID: ag.ID, CommandType: wire.CommandUnbind, Report: wire.Report{Type: wire.ReportResult, CommandID: "z", Error: "disk full"}},
		"other target": {Target: "web9", AgentID: ag.ID, CommandType: wire.CommandUnbind, Report: wire.Report{Type: wire.ReportResult, CommandID: "w"}},
	} {
		if err := e.eng.HandleReport(e.ctx, d); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !parked() {
			t.Fatalf("%s ended the park", name)
		}
	}
	// A failed late unbind from the real agent keeps the park too.
	a.FailUnbind("disk full")
	a.Pause(false)
	for deadline := time.Now().Add(5 * time.Second); slices.Contains(a.Commands(), wire.CommandUnbind) == false; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the agent never took the queued unbind")
		}
	}
	time.Sleep(200 * time.Millisecond) // its result is in
	if !parked() {
		t.Fatal("a failed unbind ended the park")
	}
}

// T-210 M1 (review of #313): the agent's late success comes after the
// delete Run's wait but BEFORE it writes the park. The engine takes the
// park write and a late result under one mutex and remembers the result,
// so the Run ends as a confirmed unbind: no park, no warning, the
// credential is revoked. (The other order, park first and result after,
// is TestParkEndsOnLateUnbindResult.) Without this, the agent that has
// finished never answers a retry and the park stays until a person forgets
// it.
func TestLateUnbindBeforeParkWrite(t *testing.T) {
	e := newEnv(t, true, withInstall, withShortUnbind)
	adm := e.admin()
	vmid, a := e.adoptWithAgent(adm, "wiki")
	a.Pause(true)
	e.eng.SetBeforeParkHook(func() {
		// The wait is over; the agent comes back, takes the queued unbind
		// and answers before the Run decides on the park.
		a.Pause(false)
		for deadline := time.Now().Add(5 * time.Second); !a.Unbound(); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Error("the agent did not answer the queued unbind")
				return
			}
		}
	})
	defer e.eng.SetBeforeParkHook(nil)

	run := e.deleteTarget(adm, "wiki")
	if run.Status != "succeeded" || run.Warnings != 0 {
		t.Fatalf("run %+v, want succeeded without a warning", run)
	}
	out := ""
	for _, s := range run.Targets[0].Steps {
		out += s.Output
	}
	if !strings.Contains(out, "its answer came after the wait") {
		t.Fatalf("step output %q", out)
	}
	if g := e.unmanaged(adm, vmid); g.AgentLeft || !g.Adoptable {
		t.Fatalf("parked after a confirmed late unbind: %+v", g)
	}
	if _, err := e.st.AgentByTarget(e.ctx, "wiki"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the credential stays: %v", err)
	}
	if n := countType(a.Commands(), wire.CommandUnbind); n != 1 {
		t.Fatalf("%d unbind commands, want 1", n)
	}
}
