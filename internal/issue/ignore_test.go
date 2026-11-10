package issue_test

import (
	"testing"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// A crash between the ignore_rule Plan and its commit loses nothing: the
// Issue stays resolving with its pending Plan, and the approval after the
// restart resolves it as ignore (T-032).
func TestIgnoreRuleSurvivesRestart(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{
		Resource: "file:/etc/motd", Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "content", From: "outside", To: "hello web1", Risk: wire.RiskReversible}},
	}}}); err != nil {
		t.Fatal(err)
	}
	iss := e.openIssue("web1", "file:/etc/motd")
	p, err := e.iss.ResolveWithRule(e.ctx, iss.ID, issue.ActionIgnoreRule, "user:admin", issue.RuleScope{})
	if err != nil {
		t.Fatal(err)
	}
	first := e.eng
	t.Cleanup(first.Close)
	e.start()
	if err := e.eng.Recover(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.st.GetIssue(e.ctx, iss.ID); got.Status != store.IssueResolving || got.PlanID != p.ID {
		t.Fatalf("after restart: %+v", got)
	}
	if run := e.approve(p.ID); run.Status != "succeeded" {
		t.Fatalf("run: %+v", run)
	}
	got, err := e.st.GetIssue(e.ctx, iss.ID)
	if err != nil || got.Status != store.IssueResolved || got.Resolution != store.ResolutionIgnore {
		t.Fatalf("after approval: %+v %v", got, err)
	}
	// The same drift is dropped now and recorded.
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{
		Resource: "file:/etc/motd", Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "content", From: "again", To: "hello web1", Risk: wire.RiskReversible}},
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.OpenIssue(e.ctx, "web1", "file:/etc/motd"); err == nil {
		t.Error("the rule did not drop the drift")
	}
	if d, _ := e.iss.IgnoredDrift(e.ctx, "web1"); len(d) != 1 || d[0].Rule != issue.RuleName(iss.ID) {
		t.Errorf("record: %+v", d)
	}
}
