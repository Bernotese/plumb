package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// Commit directly to exercise approval's check without eager invalidation.
func commitBuildRecipe(t *testing.T, e *env, motd string) {
	t.Helper()
	set, err := state.Validate(builtImageDoc("web", motd))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := state.Rows(set)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.CommitRevision(e.ctx, store.NewRevision{Author: "user:qa", Docs: rows}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildApprovalChecksCurrentRecipe(t *testing.T) {
	e := newEnv(t, false, withBuildsNoAgents)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "setup", "put": builtImageDoc("web", "hi")})
	code, body := adm.do("POST", "/api/v1/images/web/build", nil)
	if code != 201 {
		t.Fatalf("build: %d %s", code, body)
	}
	var p planOut
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	commitBuildRecipe(t, e, "changed after proposal")
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 409 {
		t.Fatalf("approve changed recipe: %d %s", code, body)
	}
	if _, err := e.st.RunOfPlan(e.ctx, p.ID); err == nil {
		t.Fatal("stale recipe started a Run")
	}
}

// Part B of T-062 removed the flag of part A: builds are on without any
// setting (this replaces the QA test of the off state).
func TestManualBuildOnByDefault(t *testing.T) {
	e := newEnv(t, false)
	commitBuildRecipe(t, e, "hi")
	if code, body := e.admin().do("POST", "/api/v1/images/web/build", nil); code != 201 || strings.Contains(string(body), "not available yet") {
		t.Fatalf("build: %d %s", code, body)
	}
}

func TestBuildRunStaysPinnedAfterApproval(t *testing.T) {
	e := newEnv(t, false, withBuildsNoAgents)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "setup", "put": builtImageDoc("web", "hi")})
	head, err := e.st.Head(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	code, body := adm.do("POST", "/api/v1/images/web/build", nil)
	if code != 201 {
		t.Fatalf("build: %d %s", code, body)
	}
	var p planOut
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	commitBuildRecipe(t, e, "changed after approval")
	e.eng.Wait()
	run, err := e.st.RunOfPlan(e.ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.RevisionID != head.ID {
		t.Fatalf("Run revision %d, approved %d", run.RevisionID, head.ID)
	}
}
