package api

// T-254, D-440 (found on pve-dev, T-247 F4): after a restore the Issue
// stays in the "restored" state until the re-enroll Run worked. A stale or
// dropped re-enroll Plan leaves it open, and the note at read time says to
// plan the re-enroll again.

import (
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
)

func TestRestoreIssueWaitsForTheReenrollRun(t *testing.T) {
	for name, kill := range map[string]func(t *testing.T, s *safetyEnv, id int64){
		// The host case: another Plan moves the head, the re-enroll goes stale.
		"another Plan moved the head": func(t *testing.T, s *safetyEnv, id int64) {
			s.e.seed(s.adm, "web2")
		},
		// The head moves with no Run, so no sweep marks the Plan stale:
		// it is pending on an old base (the pending branch of the check).
		"the head moved without a Run": func(t *testing.T, s *safetyEnv, id int64) {
			head, err := s.e.st.Head(s.e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := s.e.st.RevisionDocs(s.e.ctx, head.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.e.st.CommitRevision(s.e.ctx, store.NewRevision{Author: "user:qa", Reason: "same docs", Docs: rows}); err != nil {
				t.Fatal(err)
			}
			if p, _ := s.e.st.GetPlan(s.e.ctx, id); p.Status != store.PlanPending {
				t.Fatalf("the Plan is %s, want pending off the head", p.Status)
			}
		},
		"the re-enroll Plan was dropped": func(t *testing.T, s *safetyEnv, id int64) {
			if code, body := s.adm.do("DELETE", "/api/v1/plans/"+itoa(id), nil); code != 204 {
				t.Fatalf("drop: %d %s", code, body)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSafetyEnv(t)
			if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
				t.Fatalf("restore: %s %s", status, out)
			}
			first := s.reenrollOf(t, "web1")
			iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
			if err != nil || first == 0 {
				t.Fatalf("the Issue ended at the Plan (%v), or no Plan (%d)", err, first)
			}
			if v := s.issue(t, iss.ID); !strings.Contains(v.ActionsNote, "approve the re-enroll Plan "+itoa(first)+".") {
				t.Errorf("note while the Plan is good: %q", v.ActionsNote)
			}
			kill(t, s, first)
			// The Issue stays open, and its note now says to plan it again.
			v := s.issue(t, iss.ID)
			if v.Status != "open" || !strings.Contains(v.ActionsNote, "the re-enroll Plan "+itoa(first)+" can no longer run (it is stale") || !strings.Contains(v.ActionsNote, "plan the re-enroll again") {
				t.Fatalf("the Issue after the Plan went bad: %+v", v)
			}
			if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(first)+"/approve", nil); code != 409 {
				t.Fatalf("the old re-enroll Plan still approves: %d %s", code, body)
			}
			// The way out: a new re-enroll (the gate lets only it pass), its Run ends the Issue.
			code, body := s.adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "targets": []string{"web1"}})
			if code != 409 || problemType(body) != "restore-failed" {
				t.Errorf("another Plan of web1: %d %s", code, body)
			}
			code, body = s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "the way out"})
			if code != 201 {
				t.Fatalf("re-enroll again: %d %s", code, body)
			}
			id := s.reenrollDone(t)
			if s.openIssue(plan.GuestRestoreFailedID) {
				t.Fatal("the Issue stays after the re-enroll Run worked")
			}
			if c := lastComment(t, s, plan.GuestRestoreFailedID); !strings.Contains(c, "the re-enroll Plan "+itoa(id)+" worked after the restore") {
				t.Errorf("comment %q", c)
			}
		})
	}
}
