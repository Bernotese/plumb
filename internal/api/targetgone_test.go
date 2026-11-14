package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// T-209 sweep: the delete of a Target resolves its open Issues ("gone",
// by system, with the reason as a comment). Before, no report could
// resolve them (D-211 drops reports without a binding), and a new Target
// of the same name took them over.
func TestTargetDeleteResolvesItsIssues(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.motd("hand edit")
	id := e.motdIssue()
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "drop", "delete": []map[string]any{{"kind": "Target", "name": "web1"}}})
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	iss, err := e.st.GetIssue(e.ctx, id)
	if err != nil || iss.Status != "resolved" || iss.Resolution != "gone" || iss.ResolvedBy != "system" {
		t.Fatalf("issue after delete: %+v %v", iss, err)
	}
	_, body = adm.do("GET", "/api/v1/issues/"+itoa(id), nil)
	if !strings.Contains(string(body), "Resolved: the Target was deleted.") {
		t.Fatalf("no reason comment: %s", body)
	}
	if e.openIssueID() != 0 {
		t.Fatal("an Issue of the deleted Target is still open")
	}
}
