package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/state"
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

// D-397 (pl-lead's caution): the delete keeps what the copy rule needs. A
// flooded Target (value cap flag, agent:values open) is deleted and made
// again: the flag and its Issue stay, and the new Plan is human-only.
func TestTargetDeleteKeepsValueFlag(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	if err := e.st.FlagGuestValues(e.ctx, "web1", time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	vals, _, err := e.st.UpsertIssue(e.ctx, "web1", "agent:values", json.RawMessage(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "drop", "delete": []map[string]any{{"kind": "Target", "name": "web1"}}})
	var p struct {
		ID        int64
		HumanOnly []string `json:"human_only"`
	}
	json.Unmarshal(body, &p)
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	if iss, _ := e.st.GetIssue(e.ctx, vals.ID); iss.Status == "resolved" {
		t.Fatal("agent:values was resolved by the delete")
	}
	if f, _ := e.st.GuestValueFlags(e.ctx, time.Now()); len(f) != 1 || f[0] != "web1" {
		t.Fatalf("flag after delete: %v", f)
	}
	// Made again under the name: still human-only.
	code, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "again", "put": []state.Doc{targetDoc("web1", "hello")}})
	json.Unmarshal(body, &p)
	if code != 201 || !slices.Contains(p.HumanOnly, "accept-guest-data") {
		t.Fatalf("plan for the new web1: %d %s", code, body)
	}
}

// Review T1 of #314 (D-397): the delete keeps the stored value hashes. A
// value that the guest of web1 reported stays guest data after web1 is
// deleted: (a) web1 made again with it and (b) web2 with it are both
// human-only, and a token cannot approve them.
func TestTargetDeleteKeepsValueHashes(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.seed(adm, "web2")
	e.motd("value-of-the-old-guest")
	e.motdIssue()
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "drop", "delete": []map[string]any{{"kind": "Target", "name": "web1"}}})
	var del struct{ ID int64 }
	json.Unmarshal(body, &del)
	if code != 201 {
		t.Fatalf("propose delete: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(del.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve delete: %d %s", code, body)
	}
	e.eng.Wait()
	if _, err := e.st.GetBinding(e.ctx, state.KindTarget, "web1"); err == nil {
		t.Fatal("web1 is still bound")
	}
	for _, target := range []string{"web1", "web2"} {
		p := e.proposeDocs(e.token(TierPlan, ""), targetDoc(target, "value-of-the-old-guest"))
		if !slices.Contains(p.HumanOnly, "accept-guest-data") {
			t.Fatalf("%s with the deleted guest's value: human_only %v", target, p.HumanOnly)
		}
		found := false
		for _, m := range p.Matches {
			found = found || (m.Target == target && m.Element == "value-of-the-old-guest")
		}
		if !found {
			t.Errorf("%s: matches %+v", target, p.Matches)
		}
		e.tokenRefused(p.ID)
		adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil)
	}
}
