package issue_test

import (
	"errors"
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

// Review #53: a report between the check and the transaction changes the
// Issue; the ignore is refused and nothing changes.
func TestIgnoreOnceRefusesChangedIssue(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	report := func(changes ...wire.Change) {
		t.Helper()
		if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{
			Resource: "file:/etc/motd", Status: wire.StatusDiffers, Changes: changes}}}); err != nil {
			t.Fatal(err)
		}
	}
	plain := wire.Change{Attr: "mode", From: "0600", To: "0644", Risk: wire.RiskReversible}
	report(plain)
	iss := e.openIssue("web1", "file:/etc/motd")
	check := func(changes []wire.Change) error {
		if len(changes) != 1 || changes[0].Sensitive {
			t.Errorf("check saw %+v", changes)
		}
		// A sensitive change arrives right after the check.
		report(plain, wire.Change{Attr: "content", Sensitive: true, Risk: wire.RiskReversible})
		return nil
	}
	if _, err := e.iss.IgnoreOnce(e.ctx, iss.ID, "token:tok_x", check); !errors.Is(err, store.ErrIssueChanged) {
		t.Fatalf("ignore of a changed issue: %v", err)
	}
	got, _ := e.st.GetIssue(e.ctx, iss.ID)
	if got.Status != store.IssueOpen {
		t.Errorf("the issue must stay open: %+v", got)
	}
	if _, err := e.st.IgnoredOnce(e.ctx, "web1", "file:/etc/motd"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a refused ignore left a memory: %v", err)
	}
	// Read again, check again: now it works.
	if _, err := e.iss.IgnoreOnce(e.ctx, iss.ID, "user:admin", nil); err != nil {
		t.Fatalf("second try: %v", err)
	}
}
