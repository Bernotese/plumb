package issue_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Review T-010 B1: a Run that was running when the controller died is
// closed at the next start, and its Issues are open again.
func TestRecoverAfterCrash(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	a := e.agents["web1"]
	a.Change(e.ctx, "file:/etc/motd", map[string]any{"path": "/etc/motd", "content": "x"})
	iss := e.openIssue("web1", "file:/etc/motd")
	p, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	// What a crash leaves behind: the approval is committed and the Run
	// row says running, but no goroutine works on it.
	head, _ := e.st.Head(e.ctx)
	rows, _ := e.st.RevisionDocs(e.ctx, head.ID)
	_, run, err := e.st.ApprovePlan(e.ctx, p.ID, "user:admin", store.NewRevision{Author: "user:admin", Reason: "x", Docs: rows})
	if err != nil {
		t.Fatal(err)
	}
	e.st.AddStep(e.ctx, store.Step{RunID: run.ID, Target: "web1", Resource: "file:/etc/motd", Status: "running"})
	e.st.AddStep(e.ctx, store.Step{RunID: run.ID, Target: "web1", Resource: "file:/x", Status: "applied"})

	// Restart.
	e.eng.Close()
	e.start()
	if err := e.eng.Recover(e.ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.GetRun(e.ctx, run.ID)
	if got.Status != "failed" || got.FinishedAt.IsZero() {
		t.Errorf("run after recovery: %+v", got)
	}
	steps, _ := e.st.Steps(e.ctx, run.ID)
	if steps[0].Status != "failed" || !strings.Contains(steps[0].Output, "restarted") || steps[1].Status != "applied" {
		t.Errorf("steps after recovery: %+v", steps)
	}
	if i, _ := e.st.GetIssue(e.ctx, iss.ID); i.Status != store.IssueOpen {
		t.Errorf("issue after recovery: %s", i.Status)
	}
	// The Issue can be resolved again.
	a.SetHandler(e.link.Handler())
	e.online("web1")
	if _, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin"); err != nil {
		t.Errorf("resolve after recovery: %v", err)
	}
	// A second Recover changes nothing.
	if err := e.eng.Recover(e.ctx); err != nil {
		t.Fatal(err)
	}
}

// Review T-010 M1: one Run per target; no Plan against a target that a Run
// works on.
func TestBusyTarget(t *testing.T) {
	e := newEnv(t)
	e.seed("web1", "web2")
	p := e.propose(plan.Proposal{Put: []state.Doc{target("web1", "a", 10)}})
	other := e.propose(plan.Proposal{Put: []state.Doc{target("web2", "b", 10)}})
	e.stop("web1") // still online for 90 s, but it will not answer the apply
	busyRun, err := e.eng.Approve(e.ctx, p.ID, "user:admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The Run waits for the apply result of web1.
	_, err = e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Put: []state.Doc{target("web1", "c", 10)}}, Author: "user:admin"})
	if !errors.Is(err, plan.ErrTargetBusy) || !strings.Contains(err.Error(), "web1") {
		t.Errorf("propose for a busy target: %v", err)
	}
	// web2 is not busy; its Plan is stale only because the head moved.
	if _, err := e.eng.Approve(e.ctx, other.ID, "user:admin", nil); errors.Is(err, plan.ErrTargetBusy) {
		t.Errorf("an idle target was reported busy: %v", err)
	}
	e.eng.Wait()
	run, _ := e.st.GetRun(e.ctx, busyRun.ID)
	if run.Status != "failed" {
		t.Errorf("run without an answer: %s", run.Status)
	}
	// After the Run, planning works again.
	if _, err := e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Put: []state.Doc{target("web2", "c", 10)}}, Author: "user:admin"}); err != nil {
		t.Errorf("propose after the Run: %v", err)
	}
}

// Approve refuses a target that another Run holds, before any other check.
func TestApproveBusy(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	p1 := e.propose(plan.Proposal{Put: []state.Doc{target("web1", "a", 10)}})
	e.stop("web1")
	if _, err := e.eng.Approve(e.ctx, p1.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	// A Plan made before p1 was approved, on the old base, for the same
	// target: busy wins over stale, and nothing changes.
	p2, err := e.st.CreatePlan(e.ctx, store.Plan{BaseRevision: p1.BaseRevision, Origin: plan.OriginProposal, Risk: "read",
		Author: "user:admin", Changes: json.RawMessage(`[{"target":"web1","resources":[]}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.eng.Approve(e.ctx, p2.ID, "user:admin", nil); !errors.Is(err, plan.ErrTargetBusy) {
		t.Errorf("approve for a busy target: %v", err)
	}
	if got, _ := e.st.GetPlan(e.ctx, p2.ID); got.Status != store.PlanPending {
		t.Errorf("refused plan changed: %s", got.Status)
	}
	e.eng.Wait()
}

// Review T-010 m1, m3, m4.
func TestResolveRules(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	put := doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},`+
		`"blocks":[{"use":"motd","params":{"text":"hello web1"}},{"use":"web"}]}`)
	if run := e.approve(e.propose(plan.Proposal{Put: []state.Doc{put}}).ID); run.Status != "succeeded" {
		t.Fatal(run.Status)
	}
	a := e.agents["web1"]
	a.Change(e.ctx, "file:/etc/motd", map[string]any{"path": "/etc/motd", "content": "x"})
	a.Change(e.ctx, "package:nginx", nil)
	motdIssue := e.openIssue("web1", "file:/etc/motd")
	pkgIssue := e.openIssue("web1", "package:nginx")

	// m1: of two quick resolves, the second fails.
	p, err := e.iss.Resolve(e.ctx, motdIssue.ID, issue.ActionRevert, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.iss.Resolve(e.ctx, motdIssue.ID, issue.ActionRevert, "user:admin"); !errors.Is(err, issue.ErrNotOpen) {
		t.Errorf("second resolve: %v", err)
	}
	// m3: the revert also fixes the package; its Issue names the Plan.
	if got, _ := e.st.GetIssue(e.ctx, pkgIssue.ID); got.Status != store.IssueResolving || got.PlanID != p.ID {
		t.Errorf("covered issue: %+v", got)
	}
	if run := e.approve(p.ID); run.Status != "succeeded" {
		t.Fatal(run.Status)
	}
	for _, id := range []int64{motdIssue.ID, pkgIssue.ID} {
		if got, _ := e.st.GetIssue(e.ctx, id); got.Resolution != "revert" || got.ResolvedBy != "user:admin" {
			t.Errorf("issue %d: %+v", id, got)
		}
	}

	// m4: accept is refused for an image drift.
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: wire.GuestConfigID,
		Status: wire.StatusDiffers, Changes: []wire.Change{{Attr: "image", From: "tpl-2", To: "tpl-1", Risk: wire.RiskDestructive}}}}}); err != nil {
		t.Fatal(err)
	}
	img := e.openIssue("web1", wire.GuestConfigID)
	if _, err := e.iss.Resolve(e.ctx, img.ID, issue.ActionAccept, "user:admin"); !errors.Is(err, issue.ErrAcceptImage) {
		t.Errorf("accept of an image drift: %v", err)
	}
}
