package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-092: agent version, update step and version gate through the API.

var (
	oldSHA = strings.Repeat("a", 64)
	newSHA = strings.Repeat("b", 64)
)

// servedCaps: the agent of this controller knows the update command and a
// type "unit" that older agents lack.
func servedCaps() wire.Capabilities {
	c := wire.BaselineCapabilities()
	c.Commands = append(c.Commands, wire.CommandUpdate)
	c.Resources["unit"] = []string{"content", "name", "state"}
	return c
}

// slice3Caps: an agent that can update itself, without "unit".
func slice3Caps() wire.Capabilities {
	c := wire.BaselineCapabilities()
	c.Commands = append(c.Commands, wire.CommandUpdate)
	return c
}

// noModeCaps: an agent whose file type lacks "mode" (the motd Block sets it).
func noModeCaps() wire.Capabilities {
	c := slice3Caps()
	c.Resources["file"] = slices.DeleteFunc(slices.Clone(c.Resources["file"]), func(a string) bool { return a == "mode" })
	return c
}

func newUpdateEnv(t *testing.T) *env {
	return newEnv(t, false, func(c *plan.Config) {
		c.Agent = plan.AgentBuild{SHA256: newSHA, Version: "v2", Capabilities: servedCaps()}
		c.UpdateHelloWait = 500 * time.Millisecond
	})
}

type agentStatusView struct {
	AgentVersion  *string          `json:"agent_version"`
	AgentUpdate   string           `json:"agent_update"`
	AgentMissing  []string         `json:"agent_missing"`
	AgentReverted *agentRevertView `json:"agent_reverted"`
}

func (e *env) agentStatus(c *client, name string) agentStatusView {
	e.t.Helper()
	code, body := c.do("GET", "/api/v1/targets/"+name, nil)
	if code != 200 {
		e.t.Fatalf("get target: %d %s", code, body)
	}
	var v struct{ Status agentStatusView }
	if err := json.Unmarshal(body, &v); err != nil {
		e.t.Fatal(err)
	}
	return v.Status
}

// seedAgent seeds web1 with an agent that says hello with caps and sha.
func (e *env) seedAgent(c *client, caps wire.Capabilities) {
	e.t.Helper()
	a := e.agent("web1")
	a.SetHello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps})
	delete(e.agents, "web1") // seed starts it again otherwise
	e.agents["web1"] = a
	code, body := c.do("POST", "/api/v1/plans", map[string]any{"reason": "seed",
		"put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), doc(state.KindBlock, "motd", motdSpec), targetDoc("web1", "hello")}})
	if code != 201 {
		e.t.Fatalf("seed plan: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code, body := c.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		e.t.Fatalf("seed approve: %d %s", code, body)
	}
	e.eng.Wait()
}

// hello makes the agent of web1 say hello again (a reconnect).
func (e *env) hello(h wire.Hello) {
	e.t.Helper()
	a := e.agents["web1"]
	a.SetHello(h)
	if code := a.Report(e.ctx, wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "test", Hostname: "fake", OS: "fake 1",
		Addrs: []string{}, BinarySHA256: h.BinarySHA256, Capabilities: h.Capabilities, Reverted: h.Reverted}}); code != 204 {
		e.t.Fatalf("hello: %d", code)
	}
}

func pendingUpdates(t *testing.T, e *env) []int64 {
	t.Helper()
	plans, err := e.st.PendingPlans(e.ctx, plan.OriginAgentUpdate)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, p := range plans {
		ids = append(ids, p.ID)
	}
	return ids
}

func runSteps(t *testing.T, e *env, c *client, runID int64) (string, []stepView) {
	t.Helper()
	e.eng.Wait()
	code, body := c.do("GET", "/api/v1/runs/"+itoa(runID), nil)
	if code != 200 {
		t.Fatalf("run: %d %s", code, body)
	}
	var v struct {
		Status  string
		Targets []struct{ Steps []stepView }
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	var steps []stepView
	for _, tv := range v.Targets {
		steps = append(steps, tv.Steps...)
	}
	return v.Status, steps
}

func approve(t *testing.T, c *client, id int64) int64 {
	t.Helper()
	code, body := c.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil)
	if code != 202 {
		t.Fatalf("approve %d: %d %s", id, code, body)
	}
	var r struct{ ID int64 }
	json.Unmarshal(body, &r)
	return r.ID
}

func TestAgentUpdateFlow(t *testing.T) {
	e := newUpdateEnv(t)
	c := e.admin()
	caps := slice3Caps()
	e.seedAgent(c, caps)
	st := e.agentStatus(c, "web1")
	if st.AgentVersion == nil || *st.AgentVersion != "test" || st.AgentUpdate != plan.AgentUpdateAvailable || len(st.AgentMissing) != 0 || st.AgentReverted != nil {
		t.Fatalf("status %+v", st)
	}
	// A hello with other capabilities than the served agent: system proposes.
	e.hello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps})
	ids := pendingUpdates(t, e)
	if len(ids) != 1 {
		t.Fatalf("system Plans %v", ids)
	}
	p, err := e.st.GetPlan(e.ctx, ids[0])
	if err != nil || p.Author != plan.PrincipalSystem || p.Risk != string(wire.RiskDisruptive) {
		t.Fatalf("plan %+v %v", p, err)
	}
	// One pending update Plan per Target (D-277); another hello adds none.
	code, body := c.do("POST", "/api/v1/targets/web1/agent-update", nil)
	if code != 409 || problemType(body) != "nothing-to-do" || !strings.Contains(string(body), "pending") {
		t.Fatalf("second proposal: %d %s", code, body)
	}
	e.hello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps})
	if len(pendingUpdates(t, e)) != 1 {
		t.Fatal("second system Plan")
	}
	// The Plan view: one step agent:binary, version from → to, no revision.
	code, body = c.do("GET", "/api/v1/plans/"+itoa(ids[0]), nil)
	if code != 200 || !strings.Contains(string(body), `"origin":"agent_update"`) || !strings.Contains(string(body), `"resource":"agent:binary"`) ||
		!strings.Contains(string(body), `"to":"v2"`) {
		t.Fatalf("plan view: %d %s", code, body)
	}

	served := servedCaps()
	var sent wire.UpdateBody
	e.agents["web1"].OnUpdate(func(b wire.UpdateBody) (wire.Item, *wire.Hello) {
		sent = b
		return wire.Item{Resource: wire.AgentBinaryID, Status: wire.StatusApplied}, &wire.Hello{BinarySHA256: newSHA, Capabilities: &served}
	})
	run := approve(t, c, ids[0])
	status, steps := runSteps(t, e, c, run)
	if status != "succeeded" || len(steps) != 1 || steps[0].Resource != wire.AgentBinaryID || steps[0].Status != "applied" {
		t.Fatalf("run %s %+v", status, steps)
	}
	if sent.SHA256 != newSHA || sent.PlanID != ids[0] {
		t.Errorf("update body %+v", sent)
	}
	if st := e.agentStatus(c, "web1"); st.AgentUpdate != plan.AgentUpdateNone {
		t.Errorf("after the update: %+v", st)
	}
	code, body = c.do("GET", "/api/v1/plans/"+itoa(ids[0]), nil)
	if code != 200 || !strings.Contains(string(body), `"revision":null`) {
		t.Errorf("an update Plan commits no revision: %s", body)
	}
	code, body = c.do("POST", "/api/v1/targets/web1/agent-update", nil)
	if code != 409 || problemType(body) != "nothing-to-do" {
		t.Errorf("update of a current agent: %d %s", code, body)
	}
}

func TestAgentUpdateFailures(t *testing.T) {
	tests := []struct {
		name string
		fn   func(wire.UpdateBody) (wire.Item, *wire.Hello)
		want string
	}{
		{"item failed", func(wire.UpdateBody) (wire.Item, *wire.Hello) {
			return wire.Item{Resource: wire.AgentBinaryID, Status: wire.StatusFailed, Output: "sha256 mismatch"}, nil
		}, "sha256 mismatch"},
		{"reverted", func(wire.UpdateBody) (wire.Item, *wire.Hello) {
			caps := slice3Caps()
			return wire.Item{Resource: wire.AgentBinaryID, Status: wire.StatusApplied}, &wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps,
				Reverted: &wire.UpdateRevert{From: "test", To: "v2", ToSHA256: newSHA, Reason: wire.RevertCrashLoop, At: "2026-11-12T10:00:00Z"}}
		}, "crash-loop"},
		{"old binary", func(wire.UpdateBody) (wire.Item, *wire.Hello) {
			caps := slice3Caps()
			return wire.Item{Resource: wire.AgentBinaryID, Status: wire.StatusApplied}, &wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps}
		}, "old binary"},
		{"no hello", func(wire.UpdateBody) (wire.Item, *wire.Hello) {
			return wire.Item{Resource: wire.AgentBinaryID, Status: wire.StatusApplied}, nil
		}, "no hello of the new agent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newUpdateEnv(t)
			c := e.admin()
			e.seedAgent(c, slice3Caps())
			code, body := c.do("POST", "/api/v1/targets/web1/agent-update", map[string]any{"reason": "test"})
			if code != 201 {
				t.Fatalf("propose: %d %s", code, body)
			}
			var p struct{ ID int64 }
			json.Unmarshal(body, &p)
			e.agents["web1"].OnUpdate(tc.fn)
			status, steps := runSteps(t, e, c, approve(t, c, p.ID))
			if status != "failed" || len(steps) != 1 || steps[0].Status != "failed" || !strings.Contains(steps[0].Output, tc.want) {
				t.Fatalf("run %s %+v", status, steps)
			}
		})
	}
}

func TestAgentUpdateRevertedShownAndNotProposedAgain(t *testing.T) {
	e := newUpdateEnv(t)
	c := e.admin()
	caps := slice3Caps()
	e.seedAgent(c, caps)
	// D-275: the served binary reverted here, so system proposes nothing.
	e.hello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps,
		Reverted: &wire.UpdateRevert{From: "test", To: "v2\x01" + strings.Repeat("x", 100), ToSHA256: newSHA, Reason: wire.RevertNoHello, At: "not a time"}})
	if ids := pendingUpdates(t, e); len(ids) != 0 {
		t.Fatalf("system proposed again: %v", ids)
	}
	st := e.agentStatus(c, "web1")
	rv := st.AgentReverted
	if rv == nil || rv.Reason != wire.RevertNoHello || len(rv.To) != 64 || !strings.HasPrefix(rv.To, "v2?") || !strings.HasSuffix(rv.To, "...") || rv.At.IsZero() {
		t.Fatalf("reverted %+v", rv)
	}
	// A person may still propose it.
	code, body := c.do("POST", "/api/v1/targets/web1/agent-update", nil)
	if code != 201 {
		t.Fatalf("person: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if _, err := e.st.MarkPlanStale(e.ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	// A reason outside the two drops the whole value.
	e.hello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps,
		Reverted: &wire.UpdateRevert{From: "test", To: "v2", ToSHA256: newSHA, Reason: "<script>", At: "2026-11-12T10:00:00Z"}})
	if st := e.agentStatus(c, "web1"); st.AgentReverted != nil {
		t.Errorf("bad reason kept: %+v", st.AgentReverted)
	}
	// Without the revert, system proposes again.
	if ids := pendingUpdates(t, e); len(ids) != 1 {
		t.Errorf("system Plans %v", ids)
	}
}

func TestAgentUpdateManualAndUnknown(t *testing.T) {
	e := newUpdateEnv(t)
	c := e.admin()
	e.seed(c, "web1") // the fake agent sends no capabilities: before slice 3
	st := e.agentStatus(c, "web1")
	if st.AgentUpdate != plan.AgentUpdateManual {
		t.Fatalf("status %+v", st)
	}
	if code, body := c.do("POST", "/api/v1/targets/web1/agent-update", nil); code != 422 || !strings.Contains(string(body), "one-time install") {
		t.Errorf("manual: %d %s", code, body)
	}
	if ids := pendingUpdates(t, e); len(ids) != 0 {
		t.Errorf("system proposed for an old agent: %v", ids)
	}
	if code, _ := c.do("POST", "/api/v1/targets/nope/agent-update", nil); code != 404 {
		t.Errorf("no such target: %d", code)
	}
}

// D-259, D-268: a Plan that needs what the agent lacks is refused at
// creation and at approval; the status says required.
func TestAgentGate(t *testing.T) {
	e := newUpdateEnv(t)
	c := e.admin()
	caps := slice3Caps()
	e.seedAgent(c, caps)
	put := map[string]any{"reason": "text", "put": []state.Doc{targetDoc("web1", "changed")}}
	code, body := c.do("POST", "/api/v1/plans", put)
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	// The agent now lacks file.mode: approval refuses the Plan.
	reduced := noModeCaps()
	e.hello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &reduced})
	code, body = c.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil)
	var prob struct {
		Detail  string
		Targets []string
		Missing []string
	}
	json.Unmarshal(body, &prob)
	if code != 409 || problemType(body) != "agent-outdated" || !slices.Equal(prob.Targets, []string{"web1"}) ||
		!slices.Equal(prob.Missing, []string{"file.mode"}) || !strings.HasPrefix(prob.Detail, wire.GateMessage) {
		t.Fatalf("approve: %d %s", code, body)
	}
	code, body = c.do("POST", "/api/v1/plans", map[string]any{"reason": "text", "put": []state.Doc{targetDoc("web1", "again")}})
	if code != 409 || problemType(body) != "agent-outdated" {
		t.Fatalf("propose with an outdated agent: %d %s", code, body)
	}
	st := e.agentStatus(c, "web1")
	if st.AgentUpdate != plan.AgentUpdateRequired || !slices.Equal(st.AgentMissing, []string{"file.mode"}) {
		t.Errorf("status %+v", st)
	}
	// The watch list leaves out what the agent lacks.
	var last wire.WatchBody
	cmds, bodies := e.agents["web1"].Commands(), e.agents["web1"].Bodies()
	for i, cmd := range cmds {
		if cmd == wire.CommandWatch {
			json.Unmarshal(bodies[i], &last)
		}
	}
	for _, r := range last.Resources {
		if r.Type == "file" {
			t.Errorf("watch list holds %s", r.ID)
		}
	}
}

// D-276: a new Target is not gated at Plan time (no hello); the Run checks
// after the enrollment and sends no apply.
func TestAgentGateInRun(t *testing.T) {
	e := newUpdateEnv(t)
	c := e.admin()
	a := e.agent("web1")
	reduced := noModeCaps()
	a.SetHello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &reduced})
	code, body := c.do("POST", "/api/v1/plans", map[string]any{"reason": "seed",
		"put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), doc(state.KindBlock, "motd", motdSpec), targetDoc("web1", "hello")}})
	if code != 201 {
		t.Fatalf("plan: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	status, steps := runSteps(t, e, c, approve(t, c, p.ID))
	if status != "failed" {
		t.Fatalf("run %s", status)
	}
	for _, s := range steps {
		if s.Resource == "file:/etc/motd" && (s.Status != "failed" || !strings.Contains(s.Output, wire.GateMessage)) {
			t.Errorf("step %+v", s)
		}
	}
	if slices.Contains(a.Commands(), wire.CommandApply) {
		t.Error("an apply reached the outdated agent")
	}
}
