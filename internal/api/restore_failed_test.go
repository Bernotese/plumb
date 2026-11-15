package api

// T-234 (host finding F3 of T-225): while guest:restore-failed is open,
// the guest poll opens no guest:config Issue of the empty guest, an open
// one has no action, and every Plan for the Target but a restore and a
// delete by a person is refused. A restore that works ends the stale
// Issue, and the next poll compares the restored guest again.

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// poll runs one guest poll round that compares (the digest is reset).
func (s *safetyEnv) poll(t *testing.T) {
	t.Helper()
	b, err := s.e.st.GetBinding(s.e.ctx, state.KindTarget, "web1")
	if err != nil {
		t.Fatal(err)
	}
	b.Digest = ""
	if err := s.e.st.PutBinding(s.e.ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := s.e.eng.CheckGuests(s.e.ctx); err != nil {
		t.Fatal(err)
	}
}

type issueOut struct {
	ID               int64
	Status           string
	Actions          []string `json:"actions"`
	ActionsBlockedBy *int64   `json:"actions_blocked_by"`
	ActionsNote      string   `json:"actions_note"`
	Resolution       *struct{ By string }
	Comments         []struct{ Author, Body string }
}

func (s *safetyEnv) issue(t *testing.T, id int64) issueOut {
	t.Helper()
	code, body := s.adm.do("GET", "/api/v1/issues/"+itoa(id), nil)
	if code != 200 {
		t.Fatalf("issue %d: %d %s", id, code, body)
	}
	var v issueOut
	json.Unmarshal(body, &v)
	return v
}


func TestRestoreFailedNoDriftNoRevert(t *testing.T) {
	s := newSafetyEnv(t)
	e := s.e
	gid := driver.GuestID(s.ref)
	// A drift Issue that is open before the restore.
	if err := e.drv.Tamper(gid, func(g *driver.GuestSpec) { g.MemoryMB = 4096 }); err != nil {
		t.Fatal(err)
	}
	s.poll(t)
	pre, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID)
	if err != nil {
		t.Fatalf("no drift Issue before the restore: %v", err)
	}
	// The restore fails.
	s.fr.fail = errors.New("proxmox: restore failed")
	if status, _ := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "failed" {
		t.Fatalf("restore: %s", status)
	}
	rf, err := e.st.OpenIssue(e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil {
		t.Fatal(err)
	}
	// The guest is empty now; the poll does not compare it.
	e.drv.Tamper(gid, func(g *driver.GuestSpec) { g.MemoryMB, g.DiskGB = 512, 0 })
	s.poll(t)
	if now, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID); err != nil || now.ID != pre.ID || string(now.Changes) != string(pre.Changes) {
		t.Fatalf("the poll compared the empty guest: %+v %v (was %+v)", now, err, pre)
	}
	if _, err := e.st.OpenIssue(e.ctx, "web1", plan.GuestMissingID); err == nil {
		t.Error("guest:missing opened")
	}
	// The open drift Issue offers no action, and says why.
	v := s.issue(t, pre.ID)
	if len(v.Actions) != 0 || v.ActionsBlockedBy == nil || *v.ActionsBlockedBy != rf.ID || !strings.Contains(v.ActionsNote, "the guest may have no disk") {
		t.Fatalf("drift Issue view: %+v", v)
	}
	if r := s.issue(t, rf.ID); r.ActionsBlockedBy != nil || !slices.Contains(r.Actions, "ignore_once") {
		t.Errorf("guest:restore-failed lost its own action: %+v", r)
	}
	for _, a := range []string{"revert", "accept", "ignore_once", "ignore_rule"} {
		if code, body := s.adm.do("POST", "/api/v1/issues/"+itoa(pre.ID)+"/resolve", map[string]any{"action": a}); code != 409 || problemType(body) != "restore-failed" {
			t.Errorf("%s: %d %s", a, code, body)
		}
	}
	// No Plan for the Target, also not with a token.
	tok := e.token(TierApply, "disruptive")
	for name, try := range map[string]func() (int, []byte){
		"param change": func() (int, []byte) {
			return s.adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "put": []state.Doc{restoreTarget("web1", false)}})
		},
		"param change, token": func() (int, []byte) {
			return tok.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "put": []state.Doc{restoreTarget("web1", false)}})
		},
		"revert (targets)": func() (int, []byte) {
			return s.adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "targets": []string{"web1"}})
		},
		"re-enroll": func() (int, []byte) {
			return s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "x"})
		},
		"agent update": func() (int, []byte) {
			return s.adm.do("POST", "/api/v1/targets/web1/agent-update", map[string]any{"reason": "x"})
		},
		"repair": func() (int, []byte) {
			return s.adm.do("POST", "/api/v1/targets/web1/repair", map[string]any{"reason": "x"})
		},
	} {
		if code, body := try(); code != 409 || problemType(body) != "restore-failed" {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
	// A delete of the Target is allowed, by a person only.
	code, body := tok.do("POST", "/api/v1/plans", map[string]any{"reason": "gone", "delete": []map[string]any{{"kind": "Target", "name": "web1"}}})
	var del struct {
		ID        int64
		HumanOnly []string `json:"human_only"`
	}
	json.Unmarshal(body, &del)
	if code != 201 || !slices.Contains(del.HumanOnly, plan.HumanOnlyRestoreFailed) {
		t.Fatalf("delete: %d %s", code, body)
	}
	if code, body := tok.do("POST", "/api/v1/plans/"+itoa(del.ID)+"/approve", nil); code != 403 {
		t.Fatalf("a token approved the delete: %d %s", code, body)
	}
	s.adm.do("DELETE", "/api/v1/plans/"+itoa(del.ID), nil)

	// The way back: the safety backup. The stale drift Issue ends by
	// system with a comment; the next poll compares the restored guest
	// again, and its real drift opens a new Issue.
	s.fr.fail = nil
	if status, out := s.restore(t, "5afe000000000001", false); status != "succeeded" {
		t.Fatalf("restore of the safety backup: %s %s", status, out)
	}
	v = s.issue(t, pre.ID)
	if v.Status != "resolved" || v.Resolution == nil || v.Resolution.By != "system" || len(v.Comments) == 0 || !strings.Contains(v.Comments[len(v.Comments)-1].Body, "A restore replaced the guest") {
		t.Fatalf("stale Issue after the restore: %+v", v)
	}
	if err := e.eng.CheckGuests(e.ctx); err != nil { // no digest reset: the Run left it empty
		t.Fatal(err)
	}
	next, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID)
	if err != nil || next.ID == pre.ID {
		t.Fatalf("no new drift Issue of the restored guest: %+v %v", next, err)
	}
	if v := s.issue(t, next.ID); v.ActionsBlockedBy != nil || len(v.Actions) == 0 {
		t.Errorf("the new Issue has no actions: %+v", v)
	}
}

// A Plan made before the restore failed is refused at its approval.
func TestRestoreFailedRefusesOlderPlan(t *testing.T) {
	s := newSafetyEnv(t)
	code, body := s.adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "put": []state.Doc{restoreTarget("web1", false)}})
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	tok := s.e.token(TierApply, "disruptive")
	code, body = s.adm.do("POST", "/api/v1/plans", map[string]any{"reason": "gone", "delete": []map[string]any{{"kind": "Target", "name": "web1"}}})
	var del struct {
		ID        int64
		HumanOnly []string `json:"human_only"`
	}
	json.Unmarshal(body, &del)
	if code != 201 || slices.Contains(del.HumanOnly, plan.HumanOnlyRestoreFailed) {
		t.Fatalf("delete before the failure: %d %s", code, body)
	}
	s.fr.fail = errors.New("proxmox: restore failed")
	// The restore Plan makes the older Plan stale when it is approved;
	// a pending Plan of another kind is checked again at approval.
	s.restore(t, "3f2a9c0d1e4b5a67", false)
	code, body = s.adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil)
	if code != 409 || problemType(body) != "restore-failed" {
		t.Fatalf("an older Plan after a failed restore: %d %s", code, body)
	}
	// An older delete Plan needs a person now.
	if code, body := tok.do("POST", "/api/v1/plans/"+itoa(del.ID)+"/approve", nil); code != 403 || problemType(body) != "human-required" {
		t.Fatalf("a token approved an older delete after a failed restore: %d %s", code, body)
	}
}
