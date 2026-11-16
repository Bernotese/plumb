package api

// T-252 (found on pve-dev, T-247 F1): the "one re-enroll Plan" rule counts
// only a re-enroll Plan that can still do the work. A Plan whose Run ended
// (ok or failed), or a dropped one, never stops a new re-enroll Plan.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
)

// reenrollOf returns the open re-enroll Plans (pending) of the env.
func pendingReenrolls(t *testing.T, s *safetyEnv) []int64 {
	t.Helper()
	ps, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll)
	var out []int64
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

func TestSecondRestoreAfterAFinishedReenroll(t *testing.T) {
	for name, finish := range map[string]func(t *testing.T, s *safetyEnv, id int64){
		"the re-enroll Run succeeded": func(t *testing.T, s *safetyEnv, id int64) {
			if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
				t.Fatalf("approve: %d %s", code, body)
			}
			s.e.eng.Wait()
		},
		"the re-enroll Run failed": func(t *testing.T, s *safetyEnv, id int64) {
			s.e.drv.Fail(fake.OpUpdate, errors.New("config write refused"))
			if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
				t.Fatalf("approve: %d %s", code, body)
			}
			s.e.eng.Wait()
			if r, _ := s.e.st.RunOfPlan(s.e.ctx, id); r.Status != "failed" {
				t.Fatalf("the re-enroll Run did not fail: %s", r.Status)
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
				t.Fatalf("first restore: %s %s", status, out)
			}
			first := pendingReenrolls(t, s)
			if len(first) != 1 {
				t.Fatalf("re-enroll Plans after the first restore: %v", first)
			}
			finish(t, s, first[0])
			if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
				t.Fatalf("second restore: %s %s", status, out)
			}
			second := pendingReenrolls(t, s)
			if len(second) != 1 || second[0] == first[0] {
				t.Fatalf("no new re-enroll Plan after the second restore: %v (first %d)", second, first[0])
			}
			if c := lastComment(t, s, plan.GuestRestoreFailedID); !strings.Contains(c, "approve the re-enroll Plan "+itoa(second[0])) || strings.Contains(c, itoa(first[0])+" is still open") {
				t.Errorf("the Issue's end names %q", c)
			}
		})
	}
}

// An approved re-enroll Plan whose Run has not ended still counts: no
// second Plan, and the Issue stays open and names it.
func TestReenrollApprovedRunStillCounts(t *testing.T) {
	s := newSafetyEnv(t)
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
		t.Fatalf("first restore: %s %s", status, out)
	}
	first := pendingReenrolls(t, s)
	if len(first) != 1 {
		t.Fatalf("re-enroll Plans: %v", first)
	}
	// Approved in the store with a Run that has not ended (not run by this
	// process): the state while a re-enroll Run works.
	head, _ := s.e.st.Head(s.e.ctx)
	docs, _ := s.e.st.RevisionDocs(s.e.ctx, head.ID)
	if _, _, err := s.e.st.ApprovePlan(s.e.ctx, first[0], "user:admin", store.NewRevision{Author: "user:admin", Docs: docs}); err != nil {
		t.Fatal(err)
	}
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
		t.Fatalf("second restore: %s %s", status, out)
	}
	if got := pendingReenrolls(t, s); len(got) != 0 {
		t.Fatalf("a second re-enroll Plan while the first one runs: %v", got)
	}
	iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil || !strings.Contains(string(iss.Changes), "the re-enroll Plan "+itoa(first[0])+" is still open") {
		t.Fatalf("issue %s %v", iss.Changes, err)
	}
}

// M1 of the #381 review: an open re-enroll Plan of another Target never
// stops this Target's own re-enroll Plan.
func TestReenrollOfAnotherTargetDoesNotCount(t *testing.T) {
	s := newSafetyEnv(t)
	s.e.seed(s.adm, "web2")
	s.e.approve(s.adm, "allow restart", restoreTarget("web2", true))
	code, body := s.adm.do("POST", "/api/v1/targets/web2/reenroll", map[string]any{"reason": "web2's own"})
	if code != 201 {
		t.Fatalf("re-enroll web2: %d %s", code, body)
	}
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
		t.Fatalf("restore web1: %s %s", status, out)
	}
	ps, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll)
	targets := map[string]int{}
	for _, p := range ps {
		var tps []plan.TargetPlan
		json.Unmarshal(p.Changes, &tps)
		for _, tp := range tps {
			targets[tp.Target]++
		}
	}
	if targets["web1"] != 1 || targets["web2"] != 1 {
		t.Fatalf("re-enroll Plans by Target: %v", targets)
	}
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Error("web1's Issue stays open: web2's Plan was counted")
	}
}
