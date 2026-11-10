package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// approve plans docs as adm, approves and waits for the Run.
func (e *env) approve(c *client, reason string, put ...state.Doc) {
	e.t.Helper()
	code, body := c.do("POST", "/api/v1/plans", map[string]any{"reason": reason, "put": put})
	if code != 201 {
		e.t.Fatalf("%s: plan %d %s", reason, code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code, body := c.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		e.t.Fatalf("%s: approve %d %s", reason, code, body)
	}
	e.eng.Wait()
}

func (e *env) hashes(rev int64) map[string]string {
	e.t.Helper()
	docs, err := e.st.RevisionDocs(e.ctx, rev)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range docs {
		out[d.Kind+"/"+d.Name] = d.Hash
	}
	return out
}

// T-031 slice test: three revisions, diff 1→3, rollback to 1 (deletes a
// target: destructive, so no token can approve it), approve, same hashes.
func TestRevisionHistoryAndRollback(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")                             // revision 1
	e.approve(adm, "two", targetDoc("web1", "two")) // revision 2
	e.agent("web2")
	e.approve(adm, "three", targetDoc("web2", "new")) // revision 3

	readTok := e.token(TierRead, "")
	var list struct {
		Items []revisionInfo
		Next  *string
	}
	code, body := readTok.do("GET", "/api/v1/revisions?limit=2", nil)
	json.Unmarshal(body, &list)
	if code != 200 || len(list.Items) != 2 || list.Items[0].ID != 3 || list.Items[0].Documents != 4 || list.Next == nil {
		t.Fatalf("list: %d %s", code, body)
	}
	code, body = readTok.do("GET", "/api/v1/revisions?limit=2&cursor="+*list.Next, nil)
	json.Unmarshal(body, &list)
	if code != 200 || len(list.Items) != 1 || list.Items[0].ID != 1 || list.Items[0].Documents != 3 || list.Next != nil {
		t.Fatalf("list page 2: %d %s", code, body)
	}

	// Diff 1→3: web1 changed, web2 added, with both versions.
	var diff revisionDiff
	code, body = readTok.do("GET", "/api/v1/revisions/3/diff?from=1", nil)
	json.Unmarshal(body, &diff)
	if code != 200 || *diff.From != 1 || diff.To != 3 || len(diff.Changes) != 2 {
		t.Fatalf("diff 1→3: %d %s", code, body)
	}
	c0, c1 := diff.Changes[0], diff.Changes[1]
	if c0.Name != "web1" || c0.Op != "changed" || string(c0.Before) == "null" || string(c0.After) == "null" ||
		c1.Name != "web2" || c1.Op != "added" || string(c1.Before) != "null" {
		t.Fatalf("diff changes: %s", body)
	}
	// Default base n-1; revision 1 against the empty state.
	code, body = readTok.do("GET", "/api/v1/revisions/3/diff", nil)
	json.Unmarshal(body, &diff)
	if code != 200 || *diff.From != 2 || len(diff.Changes) != 1 {
		t.Fatalf("diff 3: %d %s", code, body)
	}
	code, body = readTok.do("GET", "/api/v1/revisions/1/diff", nil)
	diff = revisionDiff{}
	json.Unmarshal(body, &diff)
	if code != 200 || diff.From != nil || len(diff.Changes) != 3 {
		t.Fatalf("diff 1: %d %s", code, body)
	}
	for _, path := range []string{"/api/v1/revisions/9/diff", "/api/v1/revisions/3/diff?from=9", "/api/v1/revisions/x/diff"} {
		if code, body := readTok.do("GET", path, nil); code != 404 || problemType(body) != "not-found" {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}

	// Rollback to 1: a destructive Plan with origin rollback.
	planTok := e.token(TierPlan, "")
	code, body = planTok.do("POST", "/api/v1/revisions/1/rollback", map[string]string{"reason": "back to one"})
	var pl struct {
		ID           int64
		Origin, Risk string
		BaseRevision int64  `json:"base_revision"`
		RollbackTo   *int64 `json:"rollback_to"`
	}
	json.Unmarshal(body, &pl)
	if code != 201 || pl.Origin != "rollback" || pl.Risk != string(wire.RiskDestructive) || pl.BaseRevision != 3 ||
		pl.RollbackTo == nil || *pl.RollbackTo != 1 {
		t.Fatalf("rollback: %d %s", code, body)
	}
	applyTok := e.token(TierApply, wire.RiskDisruptive)
	if code, body := applyTok.do("POST", "/api/v1/plans/"+itoa(pl.ID)+"/approve", nil); code != 403 || problemType(body) != "risk-too-high" {
		t.Fatalf("token approves a destructive rollback: %d %s", code, body)
	}
	// A successful rollback request stores a pending Plan, with no apply side effects.
	e.eng.Wait()
	pending, err := e.st.GetPlan(e.ctx, pl.ID)
	if err != nil || pending.Status != "pending" || pending.Origin != "rollback" {
		t.Fatalf("pending rollback: %+v %v", pending, err)
	}
	headBefore, err := e.st.Head(e.ctx)
	if err != nil || headBefore.ID != 3 {
		t.Fatalf("rollback changed head before approval: %+v %v", headBefore, err)
	}
	runs, err := e.st.ListRuns(e.ctx, 0, 100)
	if err != nil || len(runs) != 3 || len(e.drv.Guests()) != 2 {
		t.Fatalf("rollback applied before approval: runs=%+v err=%v guests=%v", runs, err, e.drv.Guests())
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(pl.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve rollback: %d %s", code, body)
	}
	e.eng.Wait()
	head, _ := e.st.Head(e.ctx)
	want, got := e.hashes(1), e.hashes(head.ID)
	if head.ID != 4 || len(got) != len(want) {
		t.Fatalf("head %d: %v, want %v", head.ID, got, want)
	}
	for k, h := range want {
		if got[k] != h {
			t.Errorf("%s: hash %s, want %s", k, got[k], h)
		}
	}
	// Provenance: revision 4 points at the rollback Plan, which names its
	// goal, author and reason. A normal Plan has no goal.
	planAuthor := "token:" + planTok.bearer[:strings.Index(planTok.bearer, ".")]
	if head.PlanID != pl.ID || head.Author != planAuthor || head.Reason != "back to one" {
		t.Errorf("revision 4: %+v", head)
	}
	if rp, err := e.st.GetPlan(e.ctx, pl.ID); err != nil || rp.Origin != "rollback" || rp.RollbackTo != 1 || rp.Author != planAuthor || rp.ApprovedBy != "user:admin" {
		t.Errorf("rollback plan: %+v %v", rp, err)
	}
	if _, body := adm.do("GET", "/api/v1/plans/1", nil); !strings.Contains(string(body), `"rollback_to":null`) {
		t.Errorf("normal plan: %s", body)
	}

	// The head equals revision 1 now: nothing to do. Unknown revision: 404.
	if code, body := planTok.do("POST", "/api/v1/revisions/1/rollback", map[string]string{"reason": "again"}); code != 409 || problemType(body) != "nothing-to-do" {
		t.Errorf("rollback to the head set: %d %s", code, body)
	}
	if code, body := planTok.do("POST", "/api/v1/revisions/99/rollback", map[string]string{"reason": "x"}); code != 404 {
		t.Errorf("rollback to 99: %d %s", code, body)
	}
	if code, _ := planTok.do("POST", "/api/v1/revisions/2/rollback", map[string]string{}); code != 422 {
		t.Errorf("rollback without reason: %d", code)
	}
	if code, body := readTok.do("POST", "/api/v1/revisions/2/rollback", map[string]string{"reason": "x"}); code != 403 {
		t.Errorf("read token rollback: %d %s", code, body)
	}

	// A rollback Plan goes stale like every Plan.
	code, body = adm.do("POST", "/api/v1/revisions/2/rollback", map[string]string{"reason": "to two"})
	json.Unmarshal(body, &pl)
	if code != 201 || pl.Risk == string(wire.RiskDestructive) {
		t.Fatalf("rollback to 2: %d %s", code, body)
	}
	e.approve(adm, "moves the head", targetDoc("web1", "other"))
	_, body = adm.do("GET", "/api/v1/plans/"+itoa(pl.ID), nil)
	var st struct{ Status string }
	json.Unmarshal(body, &st)
	if st.Status != "stale" {
		t.Errorf("rollback after a head move: %s", body)
	}
	if code, _ := adm.do("POST", "/api/v1/plans/"+itoa(pl.ID)+"/approve", nil); code != 409 {
		t.Errorf("approve a stale rollback: %d", code)
	}
}
