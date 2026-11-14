package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// T-170 (host finding F1 of T-158): a pending Plan lists the document
// changes that its approval commits.

type docsPlan struct {
	ID        int64                  `json:"id"`
	Documents *[]plan.DocumentChange `json:"documents"`
	Truncated bool                   `json:"documents_truncated"`
}

func (e *env) docsOf(c *client, id int64) docsPlan {
	e.t.Helper()
	code, body := c.do("GET", "/api/v1/plans/"+itoa(id), nil)
	if code != 200 {
		e.t.Fatalf("get plan: %d %s", code, body)
	}
	var p docsPlan
	if err := json.Unmarshal(body, &p); err != nil || p.Documents == nil {
		e.t.Fatalf("plan without documents: %s %v", body, err)
	}
	return p
}

// The F1 case: a changed Block that no Target uses. The list equals the
// revision diff that the approval commits.
func TestPlanDocumentsBlockOnly(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.runPlanAnyStatus(adm, map[string]any{"reason": "unused", "put": []state.Doc{
		doc(state.KindBlock, "spare", `{"resources":[{"file":{"path":"/etc/spare","content":"one"}}]}`)}})
	head, _ := e.st.Head(e.ctx)

	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "update", "put": []state.Doc{
		doc(state.KindBlock, "spare", `{"resources":[{"file":{"path":"/etc/spare","content":"two"}}]}`),
		doc(state.KindBlock, "fresh", `{"resources":[{"file":{"path":"/etc/fresh","content":"x"}}]}`)},
		"delete": []map[string]string{{"kind": "Block", "name": "motd-unused-none"}}})
	if code != 422 { // a delete of an unknown document is refused; without it:
		t.Fatalf("unknown delete: %d %s", code, body)
	}
	code, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "update", "put": []state.Doc{
		doc(state.KindBlock, "spare", `{"resources":[{"file":{"path":"/etc/spare","content":"two"}}]}`),
		doc(state.KindBlock, "fresh", `{"resources":[{"file":{"path":"/etc/fresh","content":"x"}}]}`)}})
	var created docsPlan
	if code != 201 || json.Unmarshal(body, &created) != nil || created.Documents == nil {
		t.Fatalf("create: %d %s", code, body)
	}
	p := e.docsOf(adm, created.ID)
	docs := *p.Documents
	if len(docs) != 2 || docs[0].Name != "fresh" || docs[0].Op != "added" || string(docs[0].Before) != "null" ||
		docs[1].Name != "spare" || docs[1].Op != "changed" || !strings.Contains(string(docs[1].Before), `"one"`) || !strings.Contains(string(docs[1].After), `"two"`) {
		t.Fatalf("documents: %+v", docs)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	code, body = adm.do("GET", "/api/v1/revisions/"+itoa(head.ID+1)+"/diff?from="+itoa(head.ID), nil)
	var diff struct{ Changes []plan.DocumentChange }
	if code != 200 || json.Unmarshal(body, &diff) != nil {
		t.Fatalf("diff: %d %s", code, body)
	}
	a, _ := json.Marshal(docs)
	b, _ := json.Marshal(diff.Changes)
	if string(a) != string(b) {
		t.Fatalf("documents before approval\n%s\n!= committed diff\n%s", a, b)
	}
	// An approved Plan has its revision diff, no documents (D1 of #267).
	if _, got := adm.do("GET", "/api/v1/plans/"+itoa(p.ID), nil); strings.Contains(string(got), `"documents"`) {
		t.Fatalf("documents of an approved plan: %s", got)
	}
	// The list of Plans has no documents (rebuilding them per item costs).
	_, list := adm.do("GET", "/api/v1/plans?limit=5", nil)
	if strings.Contains(string(list), `"documents"`) {
		t.Fatalf("documents in the list: %s", list)
	}
}

// A secret value is never in the documents (D-088): only the reference.
func TestPlanDocumentsNoSecret(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	const value = "plan-docs-secret-7f3a91"
	if code, body := adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": value}); code != 200 {
		t.Fatalf("secret: %d %s", code, body)
	}
	e.agent("app1")
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "app", "put": []state.Doc{appBlock("file"), appTarget("app1", "app-db")}})
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	if strings.Contains(string(body), value) || !strings.Contains(string(body), `{"secret":"app-db"}`) {
		t.Fatalf("documents with a secret value or without the reference: %s", body)
	}
	var p docsPlan
	json.Unmarshal(body, &p)
	_, got := adm.do("GET", "/api/v1/plans/"+itoa(p.ID), nil)
	if strings.Contains(string(got), value) {
		t.Fatalf("secret value in the plan: %s", got)
	}
}

// A stale Plan shows what it would have committed on its own base; a
// lowered bound cuts the text and says so.
func TestPlanDocumentsStaleAndBound(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	put := func(name, content string) map[string]any {
		return map[string]any{"reason": name, "put": []state.Doc{
			doc(state.KindBlock, name, `{"resources":[{"file":{"path":"/etc/`+name+`","content":"`+content+`"}}]}`)}}
	}
	code, body := adm.do("POST", "/api/v1/plans", put("aaa", "1"))
	var first docsPlan
	json.Unmarshal(body, &first)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	e.runPlanAnyStatus(adm, put("bbb", "2")) // moves the head: the first Plan is stale
	p := e.docsOf(adm, first.ID)
	if docs := *p.Documents; len(docs) != 1 || docs[0].Name != "aaa" || docs[0].Op != "added" {
		t.Fatalf("stale plan documents: %+v", docs)
	}

	old := plan.MaxPlanDocuments
	plan.MaxPlanDocuments = 1
	defer func() { plan.MaxPlanDocuments = old }()
	code, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "two", "put": []state.Doc{
		doc(state.KindBlock, "ccc", `{"resources":[{"file":{"path":"/etc/c","content":"c"}}]}`),
		doc(state.KindBlock, "ddd", `{"resources":[{"file":{"path":"/etc/d","content":"d"}}]}`)}})
	var cut docsPlan
	if code != 201 || json.Unmarshal(body, &cut) != nil {
		t.Fatalf("create: %d %s", code, body)
	}
	docs := *cut.Documents
	if !cut.Truncated || len(docs) != 2 || string(docs[0].After) == "null" || string(docs[1].After) != "null" || docs[1].Name != "ddd" {
		t.Fatalf("bound: %+v truncated=%v", docs, cut.Truncated)
	}
}

// Review D1 of #267: a Plan whose proposal no longer validates (an old
// Plan after a schema change) still loads; the field is missing and the
// marker says so.
func TestPlanDocumentsUnavailable(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	head, _ := e.st.Head(e.ctx)
	for _, status := range []string{store.PlanPending, store.PlanApproved} {
		p, err := e.st.CreatePlan(e.ctx, store.Plan{BaseRevision: head.ID, Origin: plan.OriginProposal,
			Proposal: []byte(`{"put":[{"kind":"Block","name":"old","spec":{"no-such-field":1}}]}`), Changes: []byte(`[]`), Images: []byte(`[]`),
			Risk: "reversible", Reason: "old", Author: "user:admin"})
		if err != nil {
			t.Fatal(err)
		}
		if status == store.PlanApproved {
			rows, err := e.st.RevisionDocs(e.ctx, head.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := e.st.ApprovePlan(e.ctx, p.ID, "user:admin", store.NewRevision{Author: "user:admin", Reason: "old", Docs: rows}); err != nil {
				t.Fatal(err)
			}
			head, _ = e.st.Head(e.ctx)
		}
		code, body := adm.do("GET", "/api/v1/plans/"+itoa(p.ID), nil)
		if code != 200 || strings.Contains(string(body), `"documents"`) {
			t.Fatalf("%s: %d %s", status, code, body)
		}
		if want := status == store.PlanPending; strings.Contains(string(body), `"documents_unavailable":true`) != want {
			t.Fatalf("%s: marker %s", status, body)
		}
	}
}
