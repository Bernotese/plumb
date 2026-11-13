package api

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/wire"
)

// D-323: a Plan made by an Accept adopts data that a guest reported, so
// only a person approves it. The server sets the reason from the origin;
// a new Accept after a discard gets it again.
func TestAcceptHumanOnly(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	rc := &recorder{t: t}
	planner := e.token(TierPlan, "")

	e.motd("hand edit")
	id := e.openIssueID()
	if id == 0 {
		t.Fatal("no issue")
	}
	accept := func() string {
		t.Helper()
		code, body := rc.do(planner, "POST", "/api/v1/issues/"+itoa(id)+"/resolve", "/issues/{id}/resolve", resolveBody(issue.ActionAccept))
		if code != 201 {
			t.Fatalf("accept: %d %s", code, body)
		}
		var v struct {
			Origin    string   `json:"origin"`
			HumanOnly []string `json:"human_only"`
		}
		json.Unmarshal(body, &v)
		if v.Origin != plan.OriginAccept || !slices.Equal(v.HumanOnly, []string{plan.HumanOnlyAcceptGuestData}) {
			t.Fatalf("accept plan: %s", body)
		}
		return planIDOf(body)
	}

	pid := accept()
	approve := "/api/v1/plans/" + pid + "/approve"
	for _, c := range []*client{
		e.token(TierApply, wire.RiskReversible),
		e.token(TierApply, wire.RiskDisruptive),
	} {
		if code, body := rc.do(c, "POST", approve, "/plans/{id}/approve", nil); code != 403 || problemType(body) != "human-required" {
			t.Errorf("apply token: %d %s", code, body)
		}
	}
	for _, tier := range []string{TierRead, TierPlan} {
		if code, body := rc.do(e.token(tier, ""), "POST", approve, "/plans/{id}/approve", nil); code != 403 {
			t.Errorf("%s token: %d %s", tier, code, body)
		}
	}

	// Re-proposal: discard, the Issue opens again, a new Accept is marked again.
	if code, body := rc.do(planner, "DELETE", "/api/v1/plans/"+pid, "/plans/{id}", nil); code/100 != 2 {
		t.Fatalf("discard: %d %s", code, body)
	}
	if iss, err := e.st.GetIssue(e.ctx, id); err != nil || iss.Status != "open" {
		t.Fatalf("issue after discard: %+v %v", iss, err)
	}
	pid = accept()
	if code, body := rc.do(e.token(TierApply, wire.RiskDisruptive), "POST", "/api/v1/plans/"+pid+"/approve", "/plans/{id}/approve", nil); code != 403 || problemType(body) != "human-required" {
		t.Errorf("apply token after re-proposal: %d %s", code, body)
	}

	// A person's session approves.
	if code, body := rc.do(adm, "POST", "/api/v1/plans/"+pid+"/approve", "/plans/{id}/approve", nil); code != 202 {
		t.Fatalf("session approve: %d %s", code, body)
	}
	e.eng.Wait()
	rc.check()
}

// A Revert adopts no guest data: it stays a normal Plan.
func TestRevertNotHumanOnly(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.motd("hand edit")
	id := e.openIssueID()
	if id == 0 {
		t.Fatal("no issue")
	}
	code, body := e.token(TierPlan, "").do("POST", "/api/v1/issues/"+itoa(id)+"/resolve", resolveBody(issue.ActionRevert))
	var v struct {
		HumanOnly []string `json:"human_only"`
	}
	json.Unmarshal(body, &v)
	if code != 201 || len(v.HumanOnly) != 0 {
		t.Fatalf("revert: %d %s", code, body)
	}
}
