package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// T-092: the hello part of an agent row, and update Plans that do not go
// stale (D-278).
func TestAgentHello(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	if err := s.PutEnrollToken(ctx, "web1", "h", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "h", t0, Agent{ID: "a1", CredentialHash: "c", Version: "v1"}); err != nil {
		t.Fatal(err)
	}
	if a, err := s.AgentByTarget(ctx, "web1"); err != nil || a.Hello != nil {
		t.Fatalf("before a hello: %+v %v", a, err)
	}
	h := AgentHello{Version: "v2", BinarySHA256: "ab", Capabilities: json.RawMessage(`{"commands":["update"]}`), At: t0}
	if err := s.SetAgentHello(ctx, "a1", h); err != nil {
		t.Fatal(err)
	}
	a, err := s.AgentByTarget(ctx, "web1")
	if err != nil || a.Hello == nil || a.Hello.Version != "v2" || a.Hello.BinarySHA256 != "ab" ||
		string(a.Hello.Capabilities) != `{"commands":["update"]}` || a.Hello.Reverted != nil || !a.Hello.At.Equal(t0) {
		t.Fatalf("after a hello: %+v %v", a.Hello, err)
	}
	// No capabilities (an old agent) and a revert.
	h = AgentHello{Version: "v1", Reverted: json.RawMessage(`{"reason":"no-hello"}`), At: t0}
	if err := s.SetAgentHello(ctx, "a1", h); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.AgentByTarget(ctx, "web1"); a.Hello.Capabilities != nil || string(a.Hello.Reverted) != `{"reason":"no-hello"}` {
		t.Fatalf("second hello: %+v", a.Hello)
	}
	if err := s.SetAgentHello(ctx, "gone", h); err != ErrNotFound {
		t.Errorf("unknown agent: %v", err)
	}
	if err := s.SetAgentHello(ctx, "a1", AgentHello{Capabilities: json.RawMessage(`{`), At: t0}); err == nil {
		t.Error("bad JSON stored")
	}
}

func TestAgentUpdatePlanNotStale(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	rev, err := s.CommitRevision(ctx, NewRevision{Author: "system", Docs: []DocRow{row("Block", "a", `{"kind":"Block","name":"a","spec":{}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	upd, err := s.CreatePlan(ctx, Plan{BaseRevision: rev.ID, Origin: "agent_update", Proposal: json.RawMessage(`{}`),
		Changes: json.RawMessage(`[]`), Risk: "disruptive", Reason: "r", Author: "system"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreatePlan(ctx, Plan{BaseRevision: rev.ID, Origin: "proposal", Proposal: json.RawMessage(`{}`),
		Changes: json.RawMessage(`[]`), Risk: "read", Reason: "r", Author: "system"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitRevision(ctx, NewRevision{Author: "system", Docs: []DocRow{row("Block", "b", `{"kind":"Block","name":"b","spec":{}}`)}}); err != nil {
		t.Fatal(err)
	}
	ids, err := s.MarkStalePlans(ctx)
	if err != nil || len(ids) != 1 || ids[0] != other.ID {
		t.Fatalf("stale %v %v", ids, err)
	}
	head, _ := s.Head(ctx)
	run, err := s.ApproveBuildPlan(ctx, upd.ID, "user:a", head.ID)
	if err != nil || run.RevisionID != head.ID {
		t.Fatalf("approve: %+v %v", run, err)
	}
	if p, _ := s.GetPlan(ctx, upd.ID); p.Status != PlanApproved {
		t.Errorf("status %s", p.Status)
	}
}
