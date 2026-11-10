package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// motd sets the content of file:/etc/motd on the guest of web1 from
// outside Plumb. "hello" is the desired content of the seed.
func (e *env) motd(content string) {
	e.t.Helper()
	e.waitWatch("web1")
	if err := e.agents["web1"].Change(e.ctx, "file:/etc/motd",
		map[string]any{"path": "/etc/motd", "content": content, "mode": "0644"}); err != nil {
		e.t.Fatal(err)
	}
}

// waitWatch waits until the agent of a target has its watch list: before
// that, a change on the guest reports nothing.
func (e *env) waitWatch(target string) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(e.agents[target].Commands(), wire.CommandWatch) {
		if time.Now().After(deadline) {
			e.t.Fatal("no watch list")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// openIssueID returns the id of the Issue of web1 file:/etc/motd that is
// not resolved, or 0.
func (e *env) openIssueID() int64 {
	iss, err := e.st.OpenIssue(e.ctx, "web1", "file:/etc/motd")
	if err != nil {
		return 0
	}
	return iss.ID
}

// recorder validates every answer it records against the contract.
type recorder struct {
	t     *testing.T
	calls []recorded
}

func (rc *recorder) do(c *client, method, path, template string, body any) (int, []byte) {
	rec := c.raw(method, path, body)
	ct, _, _ := strings.Cut(rec.Header().Get("Content-Type"), ";")
	rc.calls = append(rc.calls, recorded{method, template, rec.Code, ct, rec.Body.Bytes()})
	return rec.Code, rec.Body.Bytes()
}

func (rc *recorder) check() {
	k := loadContract(rc.t)
	for _, r := range rc.calls {
		k.check(rc.t, r)
	}
}

func resolveBody(action string) map[string]any { return map[string]any{"action": action} }

// D-091: Ignore once resolves without a Plan; equal reports stay quiet
// until the resource is unchanged; other changes open a new Issue.
func TestIgnoreOnceFlow(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	rc := &recorder{t: t}

	e.motd("hand edit")
	id := e.openIssueID()
	if id == 0 {
		t.Fatal("no issue")
	}
	_, body := rc.do(adm, "GET", "/api/v1/issues/"+itoa(id), "/issues/{id}", nil)
	if !strings.Contains(string(body), `"ignore_once","ignore_rule"]`) {
		t.Errorf("actions: %s", body)
	}
	plans, _ := e.st.ListPlans(e.ctx, "", 0, 100)
	code, body := rc.do(adm, "POST", "/api/v1/issues/"+itoa(id)+"/resolve", "/issues/{id}/resolve", resolveBody(issue.ActionIgnoreOnce))
	if code != 200 || !strings.Contains(string(body), `"status":"resolved"`) || !strings.Contains(string(body), `"action":"ignore"`) ||
		!strings.Contains(string(body), `"actions":[]`) {
		t.Fatalf("ignore once: %d %s", code, body)
	}
	if after, _ := e.st.ListPlans(e.ctx, "", 0, 100); len(after) != len(plans) {
		t.Errorf("ignore once made a Plan")
	}
	if code, body := rc.do(adm, "POST", "/api/v1/issues/"+itoa(id)+"/resolve", "/issues/{id}/resolve", resolveBody(issue.ActionIgnoreOnce)); code != 409 {
		t.Errorf("ignore a resolved issue: %d %s", code, body)
	}

	// The same drift again: no Issue.
	e.motd("hand edit")
	if e.openIssueID() != 0 {
		t.Fatal("an equal report opened an Issue")
	}
	// Other changes: a new Issue.
	e.motd("another edit")
	second := e.openIssueID()
	if second == 0 || second == id {
		t.Fatalf("other changes: issue %d", second)
	}
	// Back to the desired state: the ignore ends. The first drift opens again.
	e.motd("hello")
	if e.openIssueID() != 0 {
		t.Fatal("unchanged did not resolve the issue")
	}
	e.motd("hand edit")
	if e.openIssueID() == 0 {
		t.Fatal("after unchanged, the ignored drift must open an Issue again")
	}
	rc.check()
}

// D-091: who may ignore once. The route needs tier plan (read: forbidden).
func TestIgnoreOnceAccess(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	callers := []struct {
		name string
		c    *client
	}{
		{"read", e.token(TierRead, "")},
		{"plan", e.token(TierPlan, "")},
		{"apply-reversible", e.token(TierApply, wire.RiskReversible)},
		{"apply-disruptive", e.token(TierApply, wire.RiskDisruptive)},
		{"human", adm},
	}
	cases := []struct {
		name   string
		change wire.Change
	}{
		{"reversible", wire.Change{Attr: "content", From: "a", To: "b", Risk: wire.RiskReversible}},
		{"disruptive", wire.Change{Attr: "mode", From: "0600", To: "0644", Risk: wire.RiskDisruptive}},
		{"destructive", wire.Change{Attr: "state", From: "absent", To: "present", Risk: wire.RiskDestructive}},
		{"sensitive", wire.Change{Attr: "content", Sensitive: true, Risk: wire.RiskReversible}},
	}
	want := map[string]map[string]string{ // caller → case → "" (200) or problem type
		"read":             {"reversible": "forbidden", "disruptive": "forbidden", "destructive": "forbidden", "sensitive": "forbidden"},
		"plan":             {"reversible": "human-required", "disruptive": "human-required", "destructive": "human-required", "sensitive": "human-required"},
		"apply-reversible": {"reversible": "", "disruptive": "risk-too-high", "destructive": "human-required", "sensitive": "human-required"},
		"apply-disruptive": {"reversible": "", "disruptive": "", "destructive": "human-required", "sensitive": "human-required"},
		"human":            {"reversible": "", "disruptive": "", "destructive": "", "sensitive": ""},
	}
	for _, cl := range callers {
		for _, c := range cases {
			raw, _ := json.Marshal([]wire.Change{c.change})
			iss, _, err := e.st.UpsertIssue(e.ctx, "web1", fmt.Sprintf("file:/%s/%s", cl.name, c.name), raw)
			if err != nil {
				t.Fatal(err)
			}
			code, body := cl.c.do("POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", resolveBody(issue.ActionIgnoreOnce))
			exp := want[cl.name][c.name]
			switch {
			case exp == "" && code != 200:
				t.Errorf("%s × %s: %d %s, want 200", cl.name, c.name, code, body)
			case exp != "" && (code != 403 || problemType(body) != exp):
				t.Errorf("%s × %s: %d %s, want 403 %s", cl.name, c.name, code, body, exp)
			}
			got, _ := e.st.GetIssue(e.ctx, iss.ID)
			if (exp == "") != (got.Status == store.IssueResolved) {
				t.Errorf("%s × %s: issue is %s", cl.name, c.name, got.Status)
			}
		}
	}
	e.noSecrets()
}

// D-092, D-099: Ignore by rule is a Plan with one Rule that only a human
// approves; the rule drops drift at intake and records it.
func TestIgnoreRuleFlow(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	planTok := e.token(TierPlan, "")
	applyTok := e.token(TierApply, wire.RiskDisruptive)
	rc := &recorder{t: t}

	e.motd("hand edit")
	id := e.openIssueID()
	code, body := rc.do(planTok, "POST", "/api/v1/issues/"+itoa(id)+"/resolve", "/issues/{id}/resolve",
		map[string]any{"action": issue.ActionIgnoreRule, "rule": map[string]any{"attrs": []string{"content"}}})
	var pl struct {
		ID        int64
		Origin    string
		Risk      string
		IssueID   int64    `json:"issue_id"`
		HumanOnly []string `json:"human_only"`
		Targets   []any
	}
	json.Unmarshal(body, &pl)
	if code != 201 || pl.Origin != "ignore_rule" || pl.Risk != "read" || pl.IssueID != id || len(pl.Targets) != 0 ||
		len(pl.HumanOnly) != 1 || pl.HumanOnly[0] != "rule-change" {
		t.Fatalf("ignore rule: %d %s", code, body)
	}
	if iss, _ := e.st.GetIssue(e.ctx, id); iss.Status != store.IssueResolving {
		t.Errorf("issue is %s, want resolving", iss.Status)
	}
	if code, body := rc.do(applyTok, "POST", "/api/v1/plans/"+itoa(pl.ID)+"/approve", "/plans/{id}/approve", nil); code != 403 || problemType(body) != "human-required" {
		t.Fatalf("token approve: %d %s", code, body)
	}
	if code, body := rc.do(adm, "POST", "/api/v1/plans/"+itoa(pl.ID)+"/approve", "/plans/{id}/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	iss, _ := e.st.GetIssue(e.ctx, id)
	if iss.Status != store.IssueResolved || iss.Resolution != store.ResolutionIgnore || iss.ResolvedBy != "user:admin" {
		t.Fatalf("after commit: %+v", iss)
	}
	rule := issue.RuleName(id)
	if code, body := rc.do(adm, "GET", "/api/v1/rules", "/rules", nil); code != 200 || !strings.Contains(string(body), `"name":"`+rule+`"`) {
		t.Errorf("rules: %d %s", code, body)
	}
	if code, body := rc.do(adm, "GET", "/api/v1/rules/"+rule, "/rules/{name}", nil); code != 200 ||
		!strings.Contains(string(body), `"spec":{"action":"ignore","attrs":["content"],"resource":"file:/etc/motd","target":"web1"}`) {
		t.Errorf("rule: %d %s", code, body)
	}
	rc.do(adm, "GET", "/api/v1/rules/nope", "/rules/{name}", nil)

	// Drift that the rule covers opens no Issue; the drop is visible.
	e.motd("edited again")
	if e.openIssueID() != 0 {
		t.Fatal("a rule did not drop the drift")
	}
	_, body = rc.do(adm, "GET", "/api/v1/targets/web1", "/targets/{name}", nil)
	if !strings.Contains(string(body), `"ignored_drift":[{"resource":"file:/etc/motd","rule":"`+rule+`","attrs":["content"]`) {
		t.Errorf("ignored_drift: %s", body)
	}
	if strings.Contains(string(body), "edited again") {
		t.Errorf("ignored_drift holds a value: %s", body)
	}
	// A sensitive change is ignored too (the match uses attr only).
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: "file:/etc/motd",
		Status: wire.StatusDiffers, Changes: []wire.Change{{Attr: "content", Sensitive: true, Risk: wire.RiskReversible}}}}}); err != nil {
		t.Fatal(err)
	}
	if e.openIssueID() != 0 {
		t.Error("a sensitive change that a rule covers opened an Issue")
	}
	// A Plan still shows and applies the difference, marked ignored.
	code, body = rc.do(adm, "POST", "/api/v1/plans", "/plans", map[string]any{"reason": "new text", "put": []state.Doc{targetDoc("web1", "new text")}})
	if code != 201 || !strings.Contains(string(body), `"ignored":true`) {
		t.Errorf("plan with ignored drift: %d %s", code, body)
	}
	if c, b := adm.do("POST", "/api/v1/plans/"+planIDOf(body)+"/approve", nil); c != 202 {
		t.Fatalf("apply ignored drift: %d %s", c, b)
	}
	e.eng.Wait()
	if got := e.agents["web1"].Guest("file:/etc/motd")["content"]; got != "new text" {
		t.Fatalf("ignored drift was not applied: %v", got)
	}
	e.approve(adm, "restore desired text", targetDoc("web1", "hello"))

	// Unchanged ends the record.
	e.motd("hello")
	if _, body = adm.do("GET", "/api/v1/targets/web1", nil); strings.Contains(string(body), "ignored_drift") {
		t.Errorf("record after unchanged: %s", body)
	}
	e.motd("edited again") // leave a record for rule deletion to prune
	// The rule is removed (by a human): the drift opens an Issue again.
	code, body = planTok.do("POST", "/api/v1/plans", map[string]any{"reason": "drop rule", "delete": []map[string]string{{"kind": "Rule", "name": rule}}})
	if code != 201 || !strings.Contains(string(body), `"human_only":["rule-change"]`) {
		t.Fatalf("delete rule: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+planIDOf(body)+"/approve", nil); code != 202 {
		t.Fatalf("approve delete: %d %s", code, body)
	}
	e.eng.Wait()
	if records, err := e.st.IgnoredDriftOf(e.ctx, "web1"); err != nil || len(records) != 0 {
		t.Fatalf("removed rule kept dropped-drift records: %+v %v", records, err)
	}
	e.motd("edited again")
	if e.openIssueID() == 0 {
		t.Error("after the rule is gone, the drift must open an Issue")
	}
	rc.check()
	e.noSecrets()
}

// D-092: a dropped or stale ignore_rule Plan leaves the Issue open.
func TestIgnoreRuleDroppedPlan(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.motd("hand edit")
	id := e.openIssueID()
	code, body := adm.do("POST", "/api/v1/issues/"+itoa(id)+"/resolve", resolveBody(issue.ActionIgnoreRule))
	if code != 201 {
		t.Fatalf("ignore rule: %d %s", code, body)
	}
	if code, _ := adm.do("DELETE", "/api/v1/plans/"+planIDOf(body), nil); code != 204 {
		t.Fatalf("discard: %d", code)
	}
	if iss, _ := e.st.GetIssue(e.ctx, id); iss.Status != store.IssueOpen {
		t.Fatalf("after discard: %s, want open", iss.Status)
	}
	// Stale: another revision commits first.
	_, body = adm.do("POST", "/api/v1/issues/"+itoa(id)+"/resolve", resolveBody(issue.ActionIgnoreRule))
	stale := planIDOf(body)
	_, other := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "other", "put": []state.Doc{doc(state.KindImage, "debian", imageSpec)}})
	if code, body := adm.do("POST", "/api/v1/plans/"+planIDOf(other)+"/approve", nil); code != 202 {
		t.Fatalf("approve other: %d %s", code, body)
	}
	e.eng.Wait()
	if code, _ := adm.do("POST", "/api/v1/plans/"+stale+"/approve", nil); code != 409 {
		t.Errorf("stale approve: %d", code)
	}
	if iss, _ := e.st.GetIssue(e.ctx, id); iss.Status != store.IssueOpen {
		t.Errorf("after stale: %s, want open", iss.Status)
	}
}

// D-099: a Plan that puts, changes or deletes a Rule is for a human
// approver only, for every origin; a plan token may still propose it. A
// committed rule for "*" resolves matching open Issues.
func TestRuleHumanOnly(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	planTok := e.token(TierPlan, "")
	applyTok := e.token(TierApply, wire.RiskDisruptive)
	rule := func(target string, attrs ...string) state.Doc {
		spec := map[string]any{"action": "ignore", "target": target, "resource": "file:/etc/motd"}
		if len(attrs) > 0 {
			spec["attrs"] = attrs
		}
		raw, _ := json.Marshal(spec)
		return state.Doc{Kind: state.KindRule, Name: "motd-all", Spec: raw}
	}
	humanOnly := func(name string, code int, body []byte) string {
		t.Helper()
		if code != 201 || !strings.Contains(string(body), `"human_only":["rule-change"]`) {
			t.Fatalf("%s: %d %s", name, code, body)
		}
		id := planIDOf(body)
		if c, b := applyTok.do("POST", "/api/v1/plans/"+id+"/approve", nil); c != 403 || problemType(b) != "human-required" {
			t.Fatalf("%s: token approve %d %s", name, c, b)
		}
		return id
	}
	// A rule must name an existing Target or "*".
	if code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "bad", "put": []state.Doc{rule("web9")}}); code != 422 ||
		!strings.Contains(string(body), "/spec/target") {
		t.Errorf("rule for a missing target: %d %s", code, body)
	}
	before, _ := e.st.Head(e.ctx)
	e.motd("hand edit")
	open := e.openIssueID()

	// Put a rule for "*" (plain proposal by a plan token).
	code, body := planTok.do("POST", "/api/v1/plans", map[string]any{"reason": "all", "put": []state.Doc{rule("*")}})
	id := humanOnly("put *", code, body)
	if c, b := adm.do("POST", "/api/v1/plans/"+id+"/approve", nil); c != 202 {
		t.Fatalf("approve: %d %s", c, b)
	}
	e.eng.Wait()
	if iss, _ := e.st.GetIssue(e.ctx, open); iss.Status != store.IssueResolved || iss.Resolution != store.ResolutionIgnore {
		t.Errorf("a new rule must resolve the matching open Issue: %+v", iss)
	}
	// Put the same rule again: no rule change, a normal Plan.
	if code, body := planTok.do("POST", "/api/v1/plans", map[string]any{"reason": "same", "put": []state.Doc{rule("*"), targetDoc("web1", "hello")}}); code != 201 ||
		!strings.Contains(string(body), `"human_only":[]`) {
		t.Errorf("unchanged rule: %d %s", code, body)
	} else {
		adm.do("DELETE", "/api/v1/plans/"+planIDOf(body), nil)
	}
	// Change the rule.
	code, body = planTok.do("POST", "/api/v1/plans", map[string]any{"reason": "narrow", "put": []state.Doc{rule("*", "mode")}})
	adm.do("DELETE", "/api/v1/plans/"+humanOnly("change", code, body), nil)
	// Rollback must enforce the same check when it removes or changes a Rule.
	code, body = planTok.do("POST", "/api/v1/revisions/"+itoa(before.ID)+"/rollback", map[string]string{"reason": "remove rule"})
	adm.do("DELETE", "/api/v1/plans/"+humanOnly("rollback deletes", code, body), nil)
	full, err := e.st.Head(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.approve(adm, "narrow rule", rule("*", "mode"))
	code, body = planTok.do("POST", "/api/v1/revisions/"+itoa(full.ID)+"/rollback", map[string]string{"reason": "restore all attrs"})
	adm.do("DELETE", "/api/v1/plans/"+humanOnly("rollback changes", code, body), nil)
	// Delete the rule.
	code, body = planTok.do("POST", "/api/v1/plans", map[string]any{"reason": "delete", "delete": []map[string]string{{"kind": "Rule", "name": "motd-all"}}})
	if id := humanOnly("delete", code, body); id != "" {
		if c, b := adm.do("POST", "/api/v1/plans/"+id+"/approve", nil); c != 202 {
			t.Fatalf("approve delete: %d %s", c, b)
		}
	}
	e.eng.Wait()
	// Rollback to a revision without the rule, and back to one with it.
	withRule, _ := e.st.Head(e.ctx)
	code, body = planTok.do("POST", "/api/v1/revisions/"+itoa(withRule.ID-1)+"/rollback", map[string]string{"reason": "rule back"})
	adm.do("DELETE", "/api/v1/plans/"+humanOnly("rollback adds", code, body), nil)
	code, body = planTok.do("POST", "/api/v1/revisions/"+itoa(before.ID)+"/rollback", map[string]string{"reason": "to seed"})
	if code == 201 && strings.Contains(string(body), "rule-change") {
		t.Errorf("rollback to a set with no rule from a head with no rule: %s", body)
	}
}

// planIDOf returns the id of a Plan answer as text.
func planIDOf(body []byte) string {
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	return itoa(p.ID)
}
