package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/link/linktest"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// adoptWithAgent adopts the person's guest name and starts an agent that
// enrolls through the real link (the install command path of D-387).
func (e *env) adoptWithAgent(adm *client, name string) (int, *linktest.Agent) {
	e.t.Helper()
	vmid, digest := e.person(name)
	code, body := adm.do("POST", "/api/v1/targets/"+name+"/adopt", map[string]any{"node": "pve", "vmid": vmid, "digest": digest, "reason": "adopt"})
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code != 201 {
		e.t.Fatalf("adopt: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		e.t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	tok, _, err := e.link.NewEnrollToken(e.ctx, name)
	if err != nil {
		e.t.Fatal(err)
	}
	used := false
	a := linktest.New(e.link.Handler(), func() (string, bool) {
		if used {
			return "", false
		}
		used = true
		return tok, true
	})
	caps := wire.Capabilities{Commands: wire.AgentCommands(), Resources: resource.Capabilities()}
	a.SetHello(wire.Hello{Capabilities: &caps})
	go a.Run(e.ctx)
	deadline := time.Now().Add(10 * time.Second)
	for !e.link.Online(name) {
		if time.Now().After(deadline) {
			e.t.Fatalf("agent of %s not online", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return vmid, a
}

type runOut struct {
	ID       int64
	Status   string
	Warnings int
	Targets  []struct {
		Steps []struct {
			Resource, Status, Output string
		}
	}
}

func (e *env) deleteTarget(adm *client, name string) runOut {
	e.t.Helper()
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "drop", "delete": []map[string]any{{"kind": "Target", "name": name}}})
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code != 201 {
		e.t.Fatalf("propose delete: %d %s", code, body)
	}
	code, body = adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil)
	var r struct {
		Run struct{ ID int64 } `json:"run"`
		ID  int64
	}
	json.Unmarshal(body, &r)
	if code != 202 {
		e.t.Fatalf("approve delete: %d %s", code, body)
	}
	e.eng.Wait()
	_, body = adm.do("GET", "/api/v1/runs?limit=1", nil)
	var list struct{ Items []runOut }
	json.Unmarshal(body, &list)
	if len(list.Items) != 1 {
		e.t.Fatalf("runs: %s", body)
	}
	_, body = adm.do("GET", "/api/v1/runs/"+itoa(list.Items[0].ID), nil)
	var run runOut
	json.Unmarshal(body, &run)
	return run
}

func (e *env) agentLeft(adm *client, vmid int) bool {
	e.t.Helper()
	_, body := adm.do("GET", "/api/v1/guests/unmanaged", nil)
	var list struct {
		Items []struct {
			VMID      int
			AgentLeft bool `json:"agent_left"`
		}
	}
	json.Unmarshal(body, &list)
	for _, g := range list.Items {
		if g.VMID == vmid {
			return g.AgentLeft
		}
	}
	e.t.Fatalf("guest %d not unmanaged: %s", vmid, body)
	return false
}

// T-205 (host finding F8): the delete of an adopted Target sends unbind
// through the real link; the agent confirms and its credential goes.
func TestAdoptedDeleteUnbindsThroughRealLink(t *testing.T) {
	e := newEnv(t, true, withInstall)
	adm := e.admin()
	vmid, a := e.adoptWithAgent(adm, "wiki")
	run := e.deleteTarget(adm, "wiki")
	if run.Status != "succeeded" || run.Warnings != 0 || !a.Unbound() {
		t.Fatalf("run %+v, unbound %v", run, a.Unbound())
	}
	if _, err := e.st.AgentByTarget(e.ctx, "wiki"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the agent row stays: %v", err)
	}
	if e.agentLeft(adm, vmid) {
		t.Fatal("agent_left after a confirmed unbind")
	}
}

// D-396: an unbind that is not confirmed parks the agent. The Run says
// "warning" (pl-lead condition 2), the guest shows agent_left, the agent
// can do nothing else, a retry ends it, and an admin can forget it.
func TestAdoptedDeleteUnbindNotConfirmed(t *testing.T) {
	e := newEnv(t, true, withInstall)
	adm := e.admin()
	vmid, a := e.adoptWithAgent(adm, "wiki")
	a.FailUnbind("disk full")
	seq, _ := e.st.LastEventSeq(e.ctx)
	run := e.deleteTarget(adm, "wiki")
	if run.Status != "succeeded" || run.Warnings != 1 {
		t.Fatalf("run %+v", run)
	}
	var step struct{ Status, Output string }
	for _, s := range run.Targets[0].Steps {
		step.Status, step.Output = s.Status, s.Output
	}
	if step.Status != "warning" || !strings.Contains(step.Output, "disk full") || !strings.Contains(step.Output, "systemctl disable --now plumb-agent.service") {
		t.Fatalf("step %+v", step)
	}
	evs, _ := e.st.EventsAfter(e.ctx, seq, 200)
	found := false
	for _, ev := range evs {
		if ev.Type == "run.finished" && strings.Contains(string(ev.Data), `"warnings":1`) {
			found = true
		}
	}
	if !found {
		t.Fatal("run.finished has no warnings")
	}
	if !e.agentLeft(adm, vmid) {
		t.Fatal("no agent_left")
	}
	// The parked agent takes no other command.
	if _, err := e.link.Send("wiki", wire.CommandObserve, wire.ObserveBody{}); !errors.Is(err, link.ErrParked) {
		t.Fatalf("observe to a parked agent: %v", err)
	}
	// Plumb sends unbind again; this time it works.
	a.FailUnbind("")
	if err := e.eng.RetryUnbinds(e.ctx); err != nil {
		t.Fatal(err)
	}
	if !a.Unbound() || e.agentLeft(adm, vmid) {
		t.Fatalf("retry: unbound %v", a.Unbound())
	}
	if _, err := e.st.AgentByTarget(e.ctx, "wiki"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the agent row stays after the retry: %v", err)
	}
}

// D-396: the park ends by a person (admin session only).
func TestForgetAgent(t *testing.T) {
	e := newEnv(t, true, withInstall)
	adm := e.admin()
	vmid, a := e.adoptWithAgent(adm, "wiki")
	a.FailUnbind("read-only file system")
	e.deleteTarget(adm, "wiki")
	path := "/api/v1/guests/unmanaged/" + itoa(int64(vmid)) + "/forget-agent"
	for _, c := range []*client{e.token(TierApply, wire.RiskDisruptive), e.token(TierPlan, "")} {
		if code, body := c.do("POST", path, nil); code != 403 {
			t.Fatalf("token forget: %d %s", code, body)
		}
	}
	if !e.agentLeft(adm, vmid) {
		t.Fatal("a token ended the park")
	}
	// While the park lasts, neither the guest nor the name is adopted again.
	vmid2, d2 := e.person("wiki")
	if code, body := adm.do("POST", "/api/v1/targets/other/adopt", map[string]any{"node": "pve", "vmid": vmid, "digest": strings.Repeat("0", 40), "reason": "again"}); code != 409 || !strings.Contains(string(body), "agent-left") {
		t.Fatalf("adopt the guest again: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/targets/wiki/adopt", map[string]any{"node": "pve", "vmid": vmid2, "digest": d2, "reason": "same name"}); code != 409 || !strings.Contains(string(body), "agent-left") {
		t.Fatalf("adopt under the parked name: %d %s", code, body)
	}
	seq, _ := e.st.LastEventSeq(e.ctx)
	if code, body := adm.do("POST", path, nil); code != 204 {
		t.Fatalf("forget: %d %s", code, body)
	}
	// Review U2 of #309: the event says what may stay in the guest.
	evs, _ := e.st.EventsAfter(e.ctx, seq, 50)
	var forgot string
	for _, ev := range evs {
		if ev.Type == "agent.forgotten" {
			forgot = string(ev.Data)
		}
	}
	if !strings.Contains(forgot, `"repair_key_lines":[`) || !strings.Contains(forgot, "plumb-repair-") {
		t.Fatalf("agent.forgotten %s", forgot)
	}
	if e.agentLeft(adm, vmid) {
		t.Fatal("agent_left after forget")
	}
	if _, err := e.st.AgentByTarget(e.ctx, "wiki"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the credential stays after forget: %v", err)
	}
	if code, _ := adm.do("POST", path, nil); code != 404 {
		t.Fatalf("second forget: %d", code)
	}
}

// Review U1 of #309: another guest cannot be adopted under the name of a
// parked agent; the answer is 409 agent-left and no Plan is made.
func TestAdoptOtherGuestUnderParkedName(t *testing.T) {
	e := newEnv(t, true, withInstall)
	adm := e.admin()
	_, a := e.adoptWithAgent(adm, "wiki")
	a.FailUnbind("disk full")
	e.deleteTarget(adm, "wiki")
	plans := func() int {
		_, body := adm.do("GET", "/api/v1/plans?limit=100", nil)
		var l struct{ Items []json.RawMessage }
		json.Unmarshal(body, &l)
		return len(l.Items)
	}
	before := plans()
	vmidB, digestB := e.person("wiki") // guest B, same name
	code, body := adm.do("POST", "/api/v1/targets/wiki/adopt", map[string]any{"node": "pve", "vmid": vmidB, "digest": digestB, "reason": "B"})
	if code != 409 || !strings.Contains(string(body), "agent-left") {
		t.Fatalf("adopt guest B under the parked name: %d %s", code, body)
	}
	if n := plans(); n != before {
		t.Fatalf("a Plan was made: %d → %d", before, n)
	}
}
