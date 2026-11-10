package issue_test

import (
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func TestConcurrentApprovalAppliesOnce(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	a := e.agents["web1"]
	count := func() int {
		n := 0
		for _, c := range a.Commands() {
			if c == wire.CommandApply {
				n++
			}
		}
		return n
	}
	before := count()
	head, _ := e.st.Head(e.ctx)
	p := e.propose(plan.Proposal{Put: []state.Doc{target("web1", "once", 10)}})
	start, results := make(chan struct{}), make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := e.eng.Approve(e.ctx, p.ID, "user:admin", nil); results <- err }()
	}
	close(start)
	wins := 0
	for range 2 {
		if err := <-results; err == nil {
			wins++
		} else if !errors.Is(err, store.ErrPlanNotPending) && !errors.Is(err, plan.ErrTargetBusy) {
			// Since review T-010 M1 the loser can also meet the target
			// reservation of the winner first; both refusals are correct.
			t.Errorf("duplicate approval: %v", err)
		}
	}
	e.eng.Wait()
	if after, err := e.st.Head(e.ctx); err != nil || after.ID != head.ID+1 || wins != 1 || count() != before+1 {
		t.Fatalf("approval must create one revision and one apply: head=%+v wins=%d applies=%d err=%v", after, wins, count()-before, err)
	}
}

func TestStaleIssuePlanReopensAndCanBeRetried(t *testing.T) {
	for _, action := range []string{issue.ActionAccept, issue.ActionRevert} {
		t.Run(action, func(t *testing.T) {
			e := newEnv(t)
			e.seed("web1")
			a := e.agents["web1"]
			if err := a.Change(e.ctx, "file:/etc/motd", map[string]any{"path": "/etc/motd", "content": "outside", "mode": "0644"}); err != nil {
				t.Fatal(err)
			}
			iss := e.openIssue("web1", "file:/etc/motd")
			p, err := e.iss.Resolve(e.ctx, iss.ID, action, "user:admin")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.iss.Resolve(e.ctx, iss.ID, action, "user:admin"); !errors.Is(err, issue.ErrNotOpen) {
				t.Fatalf("duplicate resolution: %v", err)
			}
			// Move the head without changing this target or its drift.
			e.approve(e.propose(plan.Proposal{Put: []state.Doc{doc(state.KindBlock, "extra", motd)}}).ID)
			if got, err := e.st.GetIssue(e.ctx, iss.ID); err != nil || got.Status != store.IssueOpen || got.PlanID != 0 {
				t.Fatalf("stale resolution must reopen the issue: %+v, %v", got, err)
			}
			if _, err := e.eng.Approve(e.ctx, p.ID, "user:admin", nil); !errors.Is(err, store.ErrPlanNotPending) {
				t.Fatalf("stale resolution approved: %v", err)
			}
			retry, err := e.iss.Resolve(e.ctx, iss.ID, action, "user:admin")
			if err != nil {
				t.Fatal(err)
			}
			if run := e.approve(retry.ID); run.Status != "succeeded" {
				t.Fatalf("retry run: %+v", run)
			}
			want := "hello web1"
			if action == issue.ActionAccept {
				want = "outside"
			}
			got, err := e.st.GetIssue(e.ctx, iss.ID)
			if err != nil || got.Status != store.IssueResolved || got.Resolution != action || a.Guest("file:/etc/motd")["content"] != want {
				t.Fatalf("resolution after retry: %+v, %v", got, err)
			}
		})
	}
}

func TestRestartDuringRunFinalizesSteps(t *testing.T) {
	e := newEnv(t)
	p := e.propose(plan.Proposal{Put: []state.Doc{doc(state.KindImage, "ubuntu", image), doc(state.KindBlock, "motd", motd), target("web1", "hi", 10)}})
	run, err := e.eng.Approve(e.ctx, p.ID, "user:admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The guest exists but its agent has not enrolled; resource steps wait.
	eventually(t, "run waiting for enrollment", func() bool {
		steps, err := e.st.Steps(e.ctx, run.ID)
		return err == nil && len(steps) == 2 && steps[0].Status == "applied" && steps[1].Status == "pending"
	})
	e.eng.Close()
	e.start()
	if got, err := e.st.GetRun(e.ctx, run.ID); err != nil || got.Status != "failed" || got.FinishedAt.IsZero() {
		t.Fatalf("interrupted run: %+v, %v", got, err)
	}
	steps, err := e.st.Steps(e.ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if step.Status == "pending" || step.Status == "running" || step.FinishedAt.IsZero() {
			t.Errorf("restart left an unfinished step in a failed run: %+v", step)
		}
	}
	if _, err := e.eng.Approve(e.ctx, p.ID, "user:admin", nil); !errors.Is(err, store.ErrPlanNotPending) {
		t.Fatalf("restart replayed an approved plan: %v", err)
	}
	e.agent("web1")
	e.online("web1")
	if retried := e.approve(e.propose(plan.Proposal{Targets: []string{"web1"}}).ID); retried.Status != "succeeded" {
		t.Fatalf("a fresh plan cannot recover the target: %+v", retried)
	}
}

func TestCrashDuringResolveRunRecoversIssue(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{
		Resource: "file:/etc/motd", Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "content", From: "outside", To: "hello web1", Risk: wire.RiskReversible}},
	}}}); err != nil {
		t.Fatal(err)
	}
	iss := e.openIssue("web1", "file:/etc/motd")
	p, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	e.stop("web1")
	// Revoke the credential so the Run waits for a fresh enrollment.
	if _, _, err := e.link.NewEnrollToken(e.ctx, "web1"); err != nil {
		t.Fatal(err)
	}
	run, err := e.eng.Approve(e.ctx, p.ID, "user:admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "resolve run waiting for enrollment", func() bool {
		steps, err := e.st.Steps(e.ctx, run.ID)
		return err == nil && len(steps) == 1 && steps[0].Status == "pending"
	})
	// Start the replacement before Close: cleanup must not mask missing
	// crash recovery by finalizing the old engine's Run gracefully.
	first := e.eng
	t.Cleanup(first.Close)
	e.start()
	if got, err := e.st.GetRun(e.ctx, run.ID); err != nil || got.Status != "failed" || got.FinishedAt.IsZero() {
		t.Errorf("crash left the Run unfinished: %+v, %v", got, err)
	}
	steps, err := e.st.Steps(e.ctx, run.ID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("crash recovery steps: %+v, %v", steps, err)
	}
	for _, step := range steps {
		if step.Status == "pending" || step.Status == "running" || step.FinishedAt.IsZero() {
			t.Errorf("crash left an unfinished step: %+v", step)
		}
	}
	if got, err := e.st.GetIssue(e.ctx, iss.ID); err != nil || got.Status != store.IssueOpen || got.PlanID != 0 {
		t.Errorf("crash must reopen the resolving Issue: %+v, %v", got, err)
	}
}
