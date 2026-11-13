package api

import (
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func pauseForAudit(t *testing.T, e *env, mode *plan.RestoreMode) {
	t.Helper()
	if err := e.st.Restore(e.ctx, time.Now(), json.RawMessage(`{}`), "unused"); err != nil {
		t.Fatal(err)
	}
	if err := mode.Load(e.ctx, e.st); err != nil {
		t.Fatal(err)
	}
}

func TestRestoredApprovalKinds(t *testing.T) {
	e, _, _, _ := restoredEnv(t)
	for _, origin := range []string{plan.OriginRevert, plan.OriginBuild, plan.OriginAgentUpdate, plan.OriginProposal} {
		t.Run(origin, func(t *testing.T) {
			// A retention-only image lane has removals and no build. All kinds
			// must stop before even the caller's approval permission check.
			images := json.RawMessage(`[]`)
			if origin == plan.OriginProposal {
				images = json.RawMessage(`[{"image":"web","removes":[{"kind":"version","name":"old","version":1}]}]`)
			}
			if origin == plan.OriginBuild {
				images = json.RawMessage(`[{"image":"web","build":{"version":2,"trigger":"manual"},"removes":[]}]`)
			}
			p, err := e.st.CreatePlan(e.ctx, store.Plan{Risk: "reversible", Origin: origin, Author: "system", Reason: "audit", Proposal: json.RawMessage(`{}`), Changes: json.RawMessage(`[]`), Images: images})
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.eng.Approve(e.ctx, p.ID, "system", func(store.Plan) error { return errors.New("permission check reached") })
			if !errors.Is(err, plan.ErrRestoredMode) {
				t.Fatalf("approval guard: %v", err)
			}
			if p, err := e.st.GetPlan(e.ctx, p.ID); err != nil || p.Status != store.PlanPending {
				t.Fatalf("plan changed: %+v %v", p, err)
			}
		})
	}
}

func TestRestoredImageSchedule(t *testing.T) {
	mode := &plan.RestoreMode{}
	c := &clock{}
	c.set("2026-11-11 10:00")
	e := newEnv(t, false, withBuilds, c.opt(), func(cfg *plan.Config) { cfg.Restore = mode })
	e.buildAgents(nil)
	e.runPlanAnyStatus(e.admin(), map[string]any{"reason": "image", "put": scheduledImage(`{"every":"day","at":"03:00"}`)})
	before, err := e.st.ImageScheduleSlot(e.ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	c.set("2026-11-12 03:01")
	pauseForAudit(t, e, mode)
	e.tick()
	after, err := e.st.ImageScheduleSlot(e.ctx, "web")
	if err != nil || !after.Equal(before) || len(e.schedulePlans()) != 0 {
		t.Fatalf("paused schedule consumed a slot or made a plan: %s -> %s, %v", before, after, err)
	}
}

type auditAgents struct {
	plan.Agents
	watches atomic.Int32
}

func (a *auditAgents) Send(target string, typ wire.CommandType, body any) (string, error) {
	if typ == wire.CommandWatch {
		a.watches.Add(1)
	}
	return a.Agents.Send(target, typ, body)
}

func TestRestoredHelloWriters(t *testing.T) {
	mode := &plan.RestoreMode{}
	var spy *auditAgents
	e := newEnv(t, false, func(c *plan.Config) {
		c.Restore = mode
		c.Agent = plan.AgentBuild{SHA256: newSHA, Version: "v2", Capabilities: servedCaps()}
		spy = &auditAgents{Agents: c.Agents}
		c.Agents = spy
	})
	e.seedAgent(e.admin(), servedCaps()) // matching capabilities: no update proposal
	pauseForAudit(t, e, mode)
	before := spy.watches.Load()
	caps := slice3Caps()
	e.hello(wire.Hello{BinarySHA256: oldSHA, Capabilities: &caps})
	if got := spy.watches.Load(); got != before {
		t.Fatalf("hello sent %d watch commands", got-before)
	}
	if ids := pendingUpdates(t, e); len(ids) != 0 {
		t.Fatalf("hello proposed updates: %v", ids)
	}
}
