package api

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/wire"
)

// The API must advertise only ignore actions and reject create-only fixes.
func TestAddressIssueActionsQA(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	raw, err := json.Marshal([]wire.Change{{Attr: "ip", From: []string{"192.0.2.107"}, To: "192.0.2.41", Risk: wire.RiskRead}})
	if err != nil {
		t.Fatal(err)
	}
	iss, _, err := e.st.UpsertIssue(e.ctx, "web1", wire.GuestAddressID, raw)
	if err != nil {
		t.Fatal(err)
	}
	rc := &recorder{t: t}
	code, body := rc.do(adm, "GET", "/api/v1/issues/"+itoa(iss.ID), "/issues/{id}", nil)
	var view struct {
		Actions []string `json:"actions"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	if code != 200 || !slices.Equal(view.Actions, []string{"ignore_once", "ignore_rule"}) {
		t.Fatalf("address actions: %d %s", code, body)
	}
	for _, action := range []string{issue.ActionRevert, issue.ActionAccept} {
		code, body := rc.do(adm, "POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", "/issues/{id}/resolve", resolveBody(action))
		if code != 409 {
			t.Fatalf("%s: %d %s", action, code, body)
		}
	}
	rc.check()
}

func TestReportedAddressReadTokenQA(t *testing.T) {
	e := newEnv(t, true)
	e.seed(e.admin(), "web1")
	if code := e.agents["web1"].SendAddrs(e.ctx, []string{"192.0.2.107"}); code != 204 {
		t.Fatalf("report: %d", code)
	}
	rc := &recorder{t: t}
	code, body := rc.do(e.token(TierRead, ""), "GET", "/api/v1/targets/web1", "/targets/{name}", nil)
	var view struct {
		Status struct {
			IPs      []string `json:"ips"`
			Reported []string `json:"reported_ips"`
			At       *string  `json:"reported_at"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	if code != 200 || !slices.Equal(view.Status.Reported, []string{"192.0.2.107"}) || view.Status.At == nil || slices.Contains(view.Status.IPs, "192.0.2.107") {
		t.Fatalf("reported status: %d %s", code, body)
	}
	rc.check()
}
