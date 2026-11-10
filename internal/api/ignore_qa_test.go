package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func TestIgnoreRuleScopeLimitsQA(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.motd("outside")
	id := e.openIssueID()
	many := make([]string, 51)
	for i := range many {
		many[i] = fmt.Sprintf("attr%d", i)
	}
	for _, attrs := range [][]string{{}, {"content", "content"}, {""}, {strings.Repeat("x", 101)}, many} {
		raw, err := json.Marshal(attrs)
		if err != nil {
			t.Fatal(err)
		}
		code, body := adm.do("POST", "/api/v1/issues/"+itoa(id)+"/resolve", map[string]any{
			"action": issue.ActionIgnoreRule, "rule": map[string]any{"all_targets": true, "attrs": attrs},
		})
		if code != 422 || !strings.Contains(string(body), "/rule/attrs") {
			t.Errorf("scope attrs %v: %d %s", attrs, code, body)
		}
		code, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "invalid attrs", "put": []state.Doc{
			doc(state.KindRule, "bad-attrs", fmt.Sprintf(`{"action":"ignore","target":"*","resource":"file:/etc/motd","attrs":%s}`, raw)),
		}})
		if code != 422 || !strings.Contains(string(body), "/spec/attrs") {
			t.Errorf("document attrs %v: %d %s", attrs, code, body)
		}
	}
	code, body := e.token(TierPlan, "").do("POST", "/api/v1/issues/"+itoa(id)+"/resolve", map[string]any{
		"action": issue.ActionIgnoreRule, "rule": map[string]any{"all_targets": true, "attrs": many[:50]},
	})
	if code != 201 {
		t.Fatalf("50 attrs: %d %s", code, body)
	}
	if code, body = e.token(TierApply, wire.RiskDisruptive).do("POST", "/api/v1/plans/"+planIDOf(body)+"/approve", nil); code != 403 || problemType(body) != "human-required" {
		t.Fatalf("wildcard ignore_rule approval: %d %s", code, body)
	}
}

func TestIgnoreOnceApplyResetQA(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.motd("outside")
	id := e.openIssueID()
	head, err := e.st.Head(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := adm.do("POST", "/api/v1/issues/"+itoa(id)+"/resolve", resolveBody(issue.ActionIgnoreOnce)); code != 200 {
		t.Fatalf("ignore once: %d %s", code, body)
	}
	if after, err := e.st.Head(e.ctx); err != nil || after.ID != head.ID {
		t.Fatalf("ignore once changed revision: %+v %v", after, err)
	}
	e.approve(adm, "apply new desired state", targetDoc("web1", "new text"))
	if got := e.agents["web1"].Guest("file:/etc/motd")["content"]; got != "new text" {
		t.Fatalf("apply did not restore content: %v", got)
	}
	if _, err := e.st.IgnoredOnce(e.ctx, "web1", "file:/etc/motd"); err != store.ErrNotFound {
		t.Fatalf("apply kept ignore memory: %v", err)
	}
	e.approve(adm, "restore original desired state", targetDoc("web1", "hello"))
	e.motd("outside")
	if next := e.openIssueID(); next == 0 || next == id {
		t.Fatalf("apply must end ignore memory: issue %d", next)
	}
}

func TestAgentCannotMarkDriftIgnoredQA(t *testing.T) {
	e := newEnv(t, true)
	e.seed(e.admin(), "web1")
	e.waitWatch("web1")
	code := e.agents["web1"].Report(e.ctx, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{
		Resource: "file:/etc/motd", Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "content", From: "outside", To: "hello", Risk: wire.RiskReversible, Ignored: true}},
	}}})
	iss, err := e.st.OpenIssue(e.ctx, "web1", "file:/etc/motd")
	if code != 204 || err != nil || strings.Contains(string(iss.Changes), `"ignored"`) {
		t.Fatalf("forged ignored marker: HTTP %d, issue %+v, error %v", code, iss, err)
	}
}

func TestRuleKeepsUnmatchedIssueChangesQA(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Items: []wire.Item{{Resource: "file:/etc/motd", Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "content", Risk: wire.RiskReversible}, {Attr: "mode", Risk: wire.RiskReversible}},
	}}}); err != nil {
		t.Fatal(err)
	}
	id := e.openIssueID()
	e.approve(adm, "ignore content only", doc(state.KindRule, "content", `{"action":"ignore","target":"*","resource":"file:/etc/motd","attrs":["content"]}`))
	iss, err := e.st.GetIssue(e.ctx, id)
	var changes []wire.Change
	if err != nil || json.Unmarshal(iss.Changes, &changes) != nil || iss.Status != store.IssueOpen || len(changes) != 1 || changes[0].Attr != "mode" {
		t.Fatalf("partial rule lost unmatched drift: %+v %v", iss, err)
	}
	if records, err := e.st.IgnoredDriftOf(e.ctx, "web1"); err != nil || len(records) != 1 || len(records[0].Attrs) != 1 || records[0].Attrs[0] != "content" {
		t.Fatalf("partial rule record: %+v %v", records, err)
	}
}
