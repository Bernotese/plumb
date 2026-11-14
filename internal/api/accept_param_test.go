package api

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/wire"
)

// D-362: the motd Block renders content from {{ .text }}, so Accept of a
// hand edit sets the parameter, not an override. The Plan stays human-only
// (D-323), and the Target gets blocks[0].params.text.
func TestAcceptToParameter(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	rc := &recorder{t: t}
	e.motd("hand edit")
	id := e.openIssueID()
	if id == 0 {
		t.Fatal("no issue")
	}
	var iv struct {
		Raw          bool   `json:"raw"`
		AcceptMode   string `json:"accept_mode"`
		AcceptReason string `json:"accept_reason"`
		AcceptParams []struct {
			Use   string `json:"use"`
			Index int    `json:"index"`
			Param string `json:"param"`
		} `json:"accept_params"`
	}
	_, body := rc.do(e.token(TierRead, ""), "GET", "/api/v1/issues/"+itoa(id), "/issues/{id}", nil)
	if err := json.Unmarshal(body, &iv); err != nil {
		t.Fatal(err)
	}
	if iv.AcceptMode != "param" || iv.Raw || iv.AcceptReason != "" || len(iv.AcceptParams) != 1 ||
		iv.AcceptParams[0].Use != "motd" || iv.AcceptParams[0].Index != 0 || iv.AcceptParams[0].Param != "text" {
		t.Fatalf("issue view: %s", body)
	}

	// A plan token makes the Accept; it stays human-only.
	code, body := rc.do(e.token(TierPlan, ""), "POST", "/api/v1/issues/"+itoa(id)+"/resolve", "/issues/{id}/resolve", resolveBody(issue.ActionAccept))
	var p struct {
		ID        int64    `json:"id"`
		HumanOnly []string `json:"human_only"`
	}
	json.Unmarshal(body, &p)
	if code != 201 || !slices.Contains(p.HumanOnly, plan.HumanOnlyAcceptGuestData) {
		t.Fatalf("accept: %d %s", code, body)
	}
	e.tokenRefused(p.ID)
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()

	_, body = adm.do("GET", "/api/v1/targets/web1", nil)
	var tv struct {
		Document struct {
			Spec struct {
				Blocks []struct {
					Use    string         `json:"use"`
					Params map[string]any `json:"params"`
				} `json:"blocks"`
				Overrides map[string]any `json:"overrides"`
			} `json:"spec"`
		} `json:"document"`
		Raw []rawView `json:"raw"`
	}
	if err := json.Unmarshal(body, &tv); err != nil {
		t.Fatal(err)
	}
	b := tv.Document.Spec.Blocks
	if len(b) != 1 || b[0].Params["text"] != "hand edit" || len(tv.Document.Spec.Overrides) != 0 || len(tv.Raw) != 0 {
		t.Fatalf("target after accept: %s", body)
	}
	_, body = adm.do("GET", "/api/v1/issues/"+itoa(id), nil)
	var st struct{ Status string }
	json.Unmarshal(body, &st)
	if st.Status != "resolved" {
		t.Fatalf("issue after the Run: %s", body)
	}
}

// Accept on a firewall stays refused: no accept action, accept_mode none.
func TestAcceptModeNoneWithoutAccept(t *testing.T) {
	v := issueView{Resource: "firewall:main", Actions: []string{"revert"}}
	m := issue.AcceptMode(nil, "web1", v.Resource, []wire.Change{{Attr: "policy", From: "accept"}}, slices.Contains(v.Actions, "accept"))
	if m.Mode != "none" {
		t.Fatalf("%+v", m)
	}
	if err := issue.AcceptRefused("firewall:main", nil); err == nil {
		t.Fatal("firewall accept allowed")
	}
}
