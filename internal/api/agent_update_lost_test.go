package api

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-102: the result of an update is lost at the swap (found on the real
// host in T-100, F1). The hellos end the step; no ApplyTimeout wait.

// silentAgents is plan.Agents whose Online the test sets: the fake agent
// would need 90 s of real time to look offline.
type silentAgents struct {
	plan.Agents
	mu     sync.Mutex
	silent map[string]bool
}

func (s *silentAgents) Online(target string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.silent[target] && s.Agents.Online(target)
}

func (s *silentAgents) setSilent(target string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.silent[target] = true
}

// lostApplyTimeout is far above every wait of these tests: a step that
// still waits for it makes the test fail on its duration.
const lostApplyTimeout = 30 * time.Second

func newLostEnv(t *testing.T) (*env, *client, *silentAgents) {
	t.Helper()
	sa := &silentAgents{silent: map[string]bool{}}
	e := newEnv(t, false, func(c *plan.Config) {
		c.Agent = plan.AgentBuild{SHA256: newSHA, Version: "v2", Capabilities: servedCaps()}
		c.UpdateHelloWait = 500 * time.Millisecond
		c.ApplyTimeout = lostApplyTimeout
		sa.Agents = c.Agents
		c.Agents = sa
	})
	c := e.admin()
	e.seedAgent(c, slice3Caps())
	return e, c, sa
}

func (e *env) proposeUpdate(c *client) int64 {
	e.t.Helper()
	code, body := c.do("POST", "/api/v1/targets/web1/agent-update", map[string]any{"reason": "test"})
	if code != 201 {
		e.t.Fatalf("propose: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	return p.ID
}

func (e *env) phase(c *client, name string) string {
	e.t.Helper()
	code, body := c.do("GET", "/api/v1/targets/"+name, nil)
	if code != 200 {
		e.t.Fatalf("get target: %d %s", code, body)
	}
	var v struct{ Status struct{ Phase string } }
	json.Unmarshal(body, &v)
	return v.Status.Phase
}

func revertOf(reason string) *wire.UpdateRevert {
	return &wire.UpdateRevert{From: "test", To: "v2", ToSHA256: newSHA, Reason: reason, At: "2026-11-13T00:25:10Z"}
}

func TestAgentUpdateResultLost(t *testing.T) {
	tests := []struct {
		name   string
		hello  func(sa *silentAgents) *wire.Hello
		status string // of the step
		want   string // in its output
	}{
		{"good hello", func(*silentAgents) *wire.Hello {
			caps := servedCaps()
			return &wire.Hello{BinarySHA256: newSHA, Capabilities: &caps}
		}, "applied", ""},
		{"reverted hello", func(*silentAgents) *wire.Hello {
			caps := slice3Caps()
			return &wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps, Reverted: revertOf(wire.RevertNoHello)}
		}, "failed", "the new agent failed in the guest (no-hello); the agent went back to its old binary"},
		{"no hello", func(sa *silentAgents) *wire.Hello {
			sa.setSilent("web1") // the old agent exits; nothing polls
			return nil
		}, "failed", "sent no result and no hello"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, c, sa := newLostEnv(t)
			id := e.proposeUpdate(c)
			a := e.agents["web1"]
			a.LoseUpdateResult(true)
			a.OnUpdate(func(wire.UpdateBody) (wire.Item, *wire.Hello) {
				return wire.Item{Resource: wire.AgentBinaryID, Status: wire.StatusApplied}, tc.hello(sa)
			})
			start := time.Now()
			status, steps := runSteps(t, e, c, approve(t, c, id))
			if d := time.Since(start); d > lostApplyTimeout/3 {
				t.Errorf("the step waited %v", d)
			}
			if len(steps) != 1 || steps[0].Status != tc.status || !strings.Contains(steps[0].Output, tc.want) {
				t.Fatalf("run %s %+v", status, steps)
			}
			if tc.status == "applied" && status != "succeeded" || tc.status == "failed" && status != "failed" {
				t.Fatalf("run status %s", status)
			}
			if tc.name != "reverted hello" {
				return
			}
			// A clean revert: the old agent is online, so no phase error;
			// the revert shows, and system proposes nothing (D-275).
			if ph := e.phase(c, "web1"); ph == "error" {
				t.Errorf("phase %s", ph)
			}
			if st := e.agentStatus(c, "web1"); st.AgentReverted == nil || st.AgentReverted.Reason != wire.RevertNoHello {
				t.Errorf("status %+v", st)
			}
			if ids := pendingUpdates(t, e); len(ids) != 0 {
				t.Errorf("system proposed again: %v", ids)
			}
		})
	}
}

// A revert of an earlier update to the same binary, sent while the update
// command still waits in the queue, is not this update's outcome.
func TestAgentUpdateStaleRevertBeforeTaken(t *testing.T) {
	e, c, _ := newLostEnv(t)
	caps := slice3Caps()
	e.hello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps, Reverted: revertOf(wire.RevertCrashLoop)})
	id := e.proposeUpdate(c)
	a := e.agents["web1"]
	served := servedCaps()
	a.OnUpdate(func(wire.UpdateBody) (wire.Item, *wire.Hello) {
		return wire.Item{Resource: wire.AgentBinaryID, Status: wire.StatusApplied}, &wire.Hello{BinarySHA256: newSHA, Capabilities: &served}
	})
	a.Pause(true)
	run := approve(t, c, id)
	// A reconnect of the old agent before its next poll: the old revert.
	deadline := time.Now().Add(5 * time.Second)
	for e.stepStatus(c, run) != "running" {
		if time.Now().After(deadline) {
			t.Fatalf("the step did not start: %s", e.runJSON(c, run))
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.hello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps, Reverted: revertOf(wire.RevertCrashLoop)})
	time.Sleep(200 * time.Millisecond)
	if body := e.runJSON(c, run); strings.Contains(body, `"status":"failed"`) {
		t.Fatalf("an old revert failed the step: %s", body)
	}
	a.Pause(false)
	status, steps := runSteps(t, e, c, run)
	if status != "succeeded" || len(steps) != 1 || steps[0].Status != "applied" {
		t.Fatalf("run %s %+v", status, steps)
	}
}

func (e *env) runJSON(c *client, id int64) string {
	e.t.Helper()
	code, body := c.do("GET", "/api/v1/runs/"+itoa(id), nil)
	if code != 200 {
		e.t.Fatalf("run: %d %s", code, body)
	}
	return string(body)
}

// stepStatus is the status of the one step of an update Run ("" before it).
func (e *env) stepStatus(c *client, id int64) string {
	e.t.Helper()
	var v struct{ Targets []struct{ Steps []stepView } }
	json.Unmarshal([]byte(e.runJSON(c, id)), &v)
	if len(v.Targets) != 1 || len(v.Targets[0].Steps) != 1 {
		return ""
	}
	return v.Targets[0].Steps[0].Status
}

// Review B1 of #202: the agent took the command at a poll, but the poll
// answer was lost. It reconnects with the revert of an earlier update to
// the same binary. That revert is not this update's outcome; the later
// result decides.
func TestAgentUpdateOldRevertAfterLostPollAnswer(t *testing.T) {
	e, c, _ := newLostEnv(t)
	caps := slice3Caps()
	old := revertOf(wire.RevertCrashLoop)
	e.hello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps, Reverted: old})
	id := e.proposeUpdate(c)
	a := e.agents["web1"]
	taken, release := make(chan struct{}), make(chan struct{})
	served := servedCaps()
	a.OnUpdate(func(wire.UpdateBody) (wire.Item, *wire.Hello) {
		close(taken) // the link counts the command as taken from here
		<-release
		return wire.Item{Resource: wire.AgentBinaryID, Status: wire.StatusApplied}, &wire.Hello{BinarySHA256: newSHA, Capabilities: &served}
	})
	run := approve(t, c, id)
	<-taken
	e.hello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps, Reverted: old})
	time.Sleep(200 * time.Millisecond)
	if st := e.stepStatus(c, run); st != "running" {
		t.Fatalf("the old revert ended the step: %s %s", st, e.runJSON(c, run))
	}
	close(release)
	status, steps := runSteps(t, e, c, run)
	if status != "succeeded" || len(steps) != 1 || steps[0].Status != "applied" {
		t.Fatalf("run %s %+v", status, steps)
	}
}

// T-108 (T-106 F3 on the real host): the result reaches the controller
// but its reply is lost. The old process reconnects with a hello of the
// old sha and sends the result again before it exits; then the new binary
// says hello. The old-sha hello after the result decides nothing.
func TestAgentUpdateLateOldHello(t *testing.T) {
	tests := []struct {
		name   string
		last   func(caps wire.Capabilities) *wire.Hello // nil: no further hello
		status string
		want   string
	}{
		{"new binary follows", func(wire.Capabilities) *wire.Hello {
			served := servedCaps()
			return &wire.Hello{BinarySHA256: newSHA, Capabilities: &served}
		}, "applied", ""},
		{"revert follows", func(caps wire.Capabilities) *wire.Hello {
			return &wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps, Reverted: revertOf(wire.RevertCrashLoop)}
		}, "failed", "the agent went back to its old binary"},
		{"old binary stays", func(wire.Capabilities) *wire.Hello { return nil },
			"failed", "the agent came back with its old binary (no hello of the new agent within"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, c, _ := newLostEnv(t)
			caps := slice3Caps()
			id := e.proposeUpdate(c)
			a := e.agents["web1"]
			a.LoseUpdateResult(true) // the test sends the result itself
			swapped := make(chan struct{})
			a.OnUpdate(func(wire.UpdateBody) (wire.Item, *wire.Hello) {
				close(swapped)
				return wire.Item{}, nil
			})
			run := approve(t, c, id)
			<-swapped
			result := wire.Report{Type: wire.ReportResult, CommandID: a.UpdateCommandID(),
				Items: []wire.Item{{Resource: wire.AgentBinaryID, Status: wire.StatusApplied}}}
			// 1. The result arrives; the agent never sees the reply.
			if code := a.Report(e.ctx, result); code != 204 {
				t.Fatalf("result: %d", code)
			}
			// 2. The old process reconnects: hello with the old sha.
			e.hello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps})
			// 3. It sends the result again (the real link answered 409
			// once the step had failed; any answer is fine here).
			if code := a.Report(e.ctx, result); code != 204 && code != 409 {
				t.Fatalf("resent result: %d", code)
			}
			time.Sleep(200 * time.Millisecond)
			if st := e.stepStatus(c, run); st != "running" {
				t.Fatalf("the old-sha hello ended the step: %s %s", st, e.runJSON(c, run))
			}
			// 4. The old process exits; the next hello decides.
			if h := tc.last(caps); h != nil {
				e.hello(*h)
			}
			start := time.Now()
			status, steps := runSteps(t, e, c, run)
			if d := time.Since(start); d > lostApplyTimeout/3 {
				t.Errorf("the step waited %v", d)
			}
			if len(steps) != 1 || steps[0].Status != tc.status || !strings.Contains(steps[0].Output, tc.want) {
				t.Fatalf("run %s %+v", status, steps)
			}
			if tc.status == "applied" && status != "succeeded" || tc.status == "failed" && status != "failed" {
				t.Fatalf("run status %s", status)
			}
		})
	}
}
