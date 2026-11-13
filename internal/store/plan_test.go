package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func TestApprovePlan(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	docs := []DocRow{row("Block", "a", `{"kind":"Block","name":"a","spec":{}}`)}
	p1, err := s.CreatePlan(ctx, Plan{Origin: "proposal", Risk: "reversible", Reason: "one", Author: "user:a"})
	if err != nil || p1.Status != PlanPending || p1.BaseRevision != 0 {
		t.Fatalf("create: %+v %v", p1, err)
	}
	p2, _ := s.CreatePlan(ctx, Plan{Origin: "proposal", Risk: "reversible", Reason: "two", Author: "user:a"})

	rev, run, err := s.ApprovePlan(ctx, p1.ID, "user:b", NewRevision{Author: "user:a", Reason: "one", Docs: docs})
	if err != nil {
		t.Fatal(err)
	}
	if rev.ID != 1 || rev.PlanID != p1.ID || run.RevisionID != 1 || run.PlanID != p1.ID || run.Status != "running" {
		t.Fatalf("rev %+v run %+v", rev, run)
	}
	got, _ := s.GetPlan(ctx, p1.ID)
	if got.Status != PlanApproved || got.ApprovedBy != "user:b" || got.ApprovedAt.IsZero() {
		t.Errorf("approved plan: %+v", got)
	}
	if _, _, err := s.ApprovePlan(ctx, p1.ID, "user:b", NewRevision{Author: "user:a", Docs: docs}); !errors.Is(err, ErrPlanNotPending) {
		t.Errorf("approve twice: %v", err)
	}
	// p2 was planned on the empty state; the head moved.
	if _, _, err := s.ApprovePlan(ctx, p2.ID, "user:b", NewRevision{Author: "user:a", Docs: docs}); !errors.Is(err, ErrPlanStale) {
		t.Errorf("stale: %v", err)
	}
	if got, _ := s.GetPlan(ctx, p2.ID); got.Status != PlanStale {
		t.Errorf("p2 = %s", got.Status)
	}
	if head, _ := s.Head(ctx); head.ID != 1 {
		t.Errorf("a stale approval committed revision %d", head.ID)
	}

	// Two approvals at the same time on the same base: exactly one wins.
	var plans []Plan
	for range 5 {
		p, _ := s.CreatePlan(ctx, Plan{BaseRevision: 1, Origin: "proposal", Risk: "read", Author: "user:a"})
		plans = append(plans, p)
	}
	var wg sync.WaitGroup
	wins := make(chan int64, len(plans))
	for _, p := range plans {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.ApprovePlan(ctx, p.ID, "user:b", NewRevision{Author: "user:a", Docs: docs}); err == nil {
				wins <- p.ID
			}
		}()
	}
	wg.Wait()
	close(wins)
	if n := len(wins); n != 1 {
		t.Errorf("%d approvals on one base succeeded", n)
	}

	// Steps.
	seq, err := s.AddStep(ctx, Step{RunID: run.ID, Target: "web1", Resource: "file:/x", Status: "pending"})
	if err != nil || seq != 1 {
		t.Fatalf("step: %d %v", seq, err)
	}
	s.UpdateStep(ctx, run.ID, seq, "running", nil, "")
	s.UpdateStep(ctx, run.ID, seq, "applied", json.RawMessage(`[{"attr":"mode","risk":"reversible"}]`), "ok")
	steps, _ := s.Steps(ctx, run.ID)
	if len(steps) != 1 || steps[0].Status != "applied" || steps[0].Output != "ok" || steps[0].FinishedAt.IsZero() || steps[0].StartedAt.IsZero() {
		t.Errorf("steps = %+v", steps)
	}
	// rolled_back (D-300) only on a failed step.
	if err := s.MarkStepRolledBack(ctx, run.ID, seq); err != nil {
		t.Fatal(err)
	}
	if steps, _ := s.Steps(ctx, run.ID); steps[0].RolledBack {
		t.Error("an applied step was marked rolled back")
	}
	fw, _ := s.AddStep(ctx, Step{RunID: run.ID, Target: "web1", Resource: "firewall:main", Status: "pending"})
	s.UpdateStep(ctx, run.ID, fw, "failed", nil, "no round trip")
	if err := s.MarkStepRolledBack(ctx, run.ID, fw); err != nil {
		t.Fatal(err)
	}
	if steps, _ := s.Steps(ctx, run.ID); len(steps) != 2 || !steps[1].RolledBack || steps[0].RolledBack {
		t.Errorf("rolled back: %+v", steps)
	}
	if err := s.FinishRun(ctx, run.ID, "succeeded", 0); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetRun(ctx, run.ID); r.Status != "succeeded" || r.FinishedAt.IsZero() {
		t.Errorf("run = %+v", r)
	}
}

func TestIssues(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	a, opened, err := s.UpsertIssue(ctx, "web1", "file:/x", json.RawMessage(`[1]`))
	if err != nil || !opened || a.Status != IssueOpen {
		t.Fatalf("open: %+v %v %v", a, opened, err)
	}
	b, opened, _ := s.UpsertIssue(ctx, "web1", "file:/x", json.RawMessage(`[2]`))
	if opened || b.ID != a.ID || string(b.Changes) != `[2]` {
		t.Errorf("update: %+v %v", b, opened)
	}
	p, _ := s.CreatePlan(ctx, Plan{Origin: "revert", IssueID: a.ID, Risk: "read", Author: "user:a"})
	if err := s.SetIssueResolving(ctx, a.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.IssuesOfPlan(ctx, p.ID); len(list) != 1 || list[0].Status != IssueResolving {
		t.Errorf("of plan: %+v", list)
	}
	s.ReopenIssue(ctx, a.ID)
	if got, _ := s.GetIssue(ctx, a.ID); got.Status != IssueOpen || got.PlanID != 0 {
		t.Errorf("reopen: %+v", got)
	}
	if err := s.ResolveIssue(ctx, a.ID, "gone", "system"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveIssue(ctx, a.ID, "gone", "system"); !errors.Is(err, ErrNotFound) {
		t.Errorf("resolve twice: %v", err)
	}
	// After resolution, new drift opens a new Issue.
	c, opened, _ := s.UpsertIssue(ctx, "web1", "file:/x", json.RawMessage(`[3]`))
	if !opened || c.ID == a.ID {
		t.Errorf("new drift after resolve: %+v", c)
	}
	if list, _ := s.Issues(ctx, ""); len(list) != 1 {
		t.Errorf("open issues: %+v", list)
	}

	// Bindings.
	s.PutBinding(ctx, Binding{Kind: "Target", Name: "web1", Ref: "100", Digest: "d1"})
	s.PutBinding(ctx, Binding{Kind: "Target", Name: "web1", Ref: "100", Digest: "d2"})
	if b, err := s.GetBinding(ctx, "Target", "web1"); err != nil || b.Digest != "d2" {
		t.Errorf("binding: %+v %v", b, err)
	}
	s.DeleteBinding(ctx, "Target", "web1")
	if _, err := s.GetBinding(ctx, "Target", "web1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted binding: %v", err)
	}
}
