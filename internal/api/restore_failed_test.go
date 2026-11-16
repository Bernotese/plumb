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
	"time"

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
	// Its way back as data (L1 of #350): the safety backup of this Run.
	code, body := s.adm.do("GET", "/api/v1/issues/"+itoa(rf.ID), nil)
	var wb struct {
		Changes        []struct{ Attr string }
		RestoreWayBack *struct {
			BackupID string `json:"backup_id"`
			Time     string
		} `json:"restore_way_back"`
	}
	json.Unmarshal(body, &wb)
	if code != 200 || wb.RestoreWayBack == nil || wb.RestoreWayBack.BackupID != "5afe000000000001" || wb.RestoreWayBack.Time != "2026-11-15T04:00:12Z" || len(wb.Changes) != 1 || wb.Changes[0].Attr != "restore" {
		t.Errorf("restore_way_back: %d %s", code, body)
	}
	if v := s.issue(t, pre.ID); v.ActionsBlockedBy == nil {
		t.Errorf("drift Issue: %+v", v)
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
	code, body = tok.do("POST", "/api/v1/plans", map[string]any{"reason": "gone", "delete": []map[string]any{{"kind": "Target", "name": "web1"}}})
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
	// D-440: while the guest waits for its re-enroll, the poll opens no
	// drift Issue. The re-enroll Run sets the guest to the Target (the
	// memory of the restored guest is the desired one again); a new hand
	// change after it is drift with its actions.
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID); err == nil {
		t.Fatal("a drift Issue while the guest waits for its re-enroll")
	}
	// The re-enroll Run fails here (the fake agent has no grow command, and
	// the Plan grows the root file system). D-440: a failed re-enroll Run
	// leaves the Issue open, and its note says to plan the re-enroll again.
	re := s.reenrollOf(t, "web1")
	if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(re)+"/approve", nil); code != 202 {
		t.Fatalf("approve the re-enroll: %d %s", code, body)
	}
	e.eng.Wait()
	if r, _ := e.st.RunOfPlan(e.ctx, re); r.Status != "failed" {
		t.Fatalf("the re-enroll Run: %s", r.Status)
	}
	if v := s.issue(t, rf.ID); v.Status != "open" || !strings.Contains(v.ActionsNote, "the re-enroll Plan "+itoa(re)+" can no longer run (its Run failed): plan the re-enroll again") {
		t.Fatalf("the Issue after a failed re-enroll Run: %+v", v)
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

// M1 of #352: ending guest:restore-failed lifts the gate, so only a human
// admin session may, never while the guest has no disk, and never by a
// Rule; a Rule does not keep the Issue from opening.
func TestRestoreFailedIgnoreOnlyWithADisk(t *testing.T) {
	s := newSafetyEnv(t)
	e := s.e
	gid := driver.GuestID(s.ref)
	// A Rule that would ignore every guest:restore-failed.
	e.approve(s.adm, "quiet", doc(state.KindRule, "quiet-restore", `{"action":"ignore","target":"*","resource":"guest:restore-failed"}`))
	s.fr.fail = errors.New("proxmox: restore failed")
	status, out := s.restore(t, "3f2a9c0d1e4b5a67", false)
	rf, err := e.st.OpenIssue(e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil {
		t.Fatalf("%s %s; a Rule kept the Issue of a failed restore from opening: %v", status, out, err)
	}
	v := s.issue(t, rf.ID)
	if !slices.Equal(v.Actions, []string{"ignore_once"}) || v.ActionsNote != plan.RestoreFailedIgnoreNote {
		t.Fatalf("restore Issue view: %+v", v)
	}
	e.drv.Tamper(gid, func(g *driver.GuestSpec) { g.DiskGB = 0 })
	ignore := func(c *client, action string) (int, string) {
		code, body := c.do("POST", "/api/v1/issues/"+itoa(rf.ID)+"/resolve", map[string]any{"action": action})
		return code, problemType(body)
	}
	if code, typ := ignore(e.token(TierApply, "disruptive"), "ignore_once"); code != 403 || typ != "human-required" {
		t.Errorf("token: %d %s", code, typ)
	}
	if code, typ := ignore(s.adm, "ignore_rule"); code != 409 || typ != "action-not-allowed" {
		t.Errorf("ignore by rule: %d %s", code, typ)
	}
	if code, typ := ignore(s.adm, "ignore_once"); code != 409 || typ != "restore-not-ended" {
		t.Errorf("admin, no disk: %d %s", code, typ)
	}
	if !s.openIssue(plan.GuestRestoreFailedID) {
		t.Fatal("the Issue ended while the guest has no disk")
	}
	// A person brought the guest back in Proxmox: now the admin may end it.
	e.drv.Tamper(gid, func(g *driver.GuestSpec) { g.DiskGB = 10 })
	if code, typ := ignore(s.adm, "ignore_once"); code != 200 {
		t.Fatalf("admin, disk back: %d %s", code, typ)
	}
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Fatal("the Issue stays after the ignore")
	}
	// The same report again (a later failure with equal changes): the
	// earlier ignore does not keep it closed.
	var ch []wire.Change
	json.Unmarshal(rf.Changes, &ch)
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: plan.GuestRestoreFailedID, Status: wire.StatusDiffers, Changes: ch}}}); err != nil {
		t.Fatal(err)
	}
	if !s.openIssue(plan.GuestRestoreFailedID) {
		t.Error("an earlier ignore kept the Issue of a new failure closed")
	}
}

// M2 of #352: the Issue is open while Proxmox restores, so a controller
// stop inside the call leaves the gate closed; a restore that works ends
// it.
func TestRestoreIssueOpenDuringTheCall(t *testing.T) {
	s := newSafetyEnv(t)
	e := s.e
	var during issueOut
	var open bool
	s.fr.during = func() {
		iss, err := e.st.OpenIssue(e.ctx, "web1", plan.GuestRestoreFailedID)
		open = err == nil
		if open {
			var ch []wire.Change
			json.Unmarshal(iss.Changes, &ch)
			during.Status = iss.Status
			for _, c := range ch {
				if c.Attr == "restore" {
					during.ActionsNote, _ = c.From.(string)
				}
			}
			w, _ := plan.RestoreWayBack(ch)
			if w == nil || w.BackupID != "5afe000000000001" {
				t.Errorf("way back during the call: %+v", w)
			}
		}
	}
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
		t.Fatalf("restore: %s %s", status, out)
	}
	if !open || !strings.HasPrefix(during.ActionsNote, "running: if this restore does not end") {
		t.Fatalf("no Issue during the restore call: %v %+v", open, during)
	}
	s.reenrollDone(t) // D-440: the Issue ends at the re-enroll Run
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Error("the Issue stays after the re-enroll Run worked")
	}
	// A stop inside the call: the state that a restart finds is the Issue
	// above with no running Run; the gate then holds (poll and Plans).
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: plan.GuestRestoreFailedID, Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "restore", From: "running: if this restore does not end, the guest may have no disk", To: "restore another backup", Risk: wire.RiskDestructive}}}}}); err != nil {
		t.Fatal(err)
	}
	e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { g.DiskGB, g.MemoryMB = 0, 512 })
	s.poll(t)
	if _, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID); err == nil {
		t.Error("a drift Issue of the empty guest after the stop")
	}
	if code, body := s.adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "targets": []string{"web1"}}); code != 409 || problemType(body) != "restore-failed" {
		t.Errorf("a Plan after the stop: %d %s", code, body)
	}
}

// The unbind of an adopted Target whose last restore failed is allowed,
// for a person only (G6 of #352).
func TestRestoreFailedAdoptedUnbind(t *testing.T) {
	e := newEnv(t, true, withInstall)
	adm := e.admin()
	e.adoptWithAgent(adm, "wiki")
	if err := e.iss.Intake(e.ctx, "wiki", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: plan.GuestRestoreFailedID, Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "restore", From: "failed: the guest may have no disk", To: "restore another backup", Risk: wire.RiskDestructive}}}}}); err != nil {
		t.Fatal(err)
	}
	tok := e.token(TierApply, "disruptive")
	code, body := tok.do("POST", "/api/v1/plans", map[string]any{"reason": "unbind", "delete": []map[string]any{{"kind": "Target", "name": "wiki"}}})
	var p struct {
		ID        int64
		HumanOnly []string `json:"human_only"`
		Targets   []struct{ Target string }
	}
	json.Unmarshal(body, &p)
	if code != 201 || !slices.Contains(p.HumanOnly, plan.HumanOnlyRestoreFailed) || len(p.Targets) != 1 {
		t.Fatalf("unbind: %d %s", code, body)
	}
	if code, body := tok.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 403 || problemType(body) != "human-required" {
		t.Fatalf("a token approved the unbind: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("a person approves the unbind: %d %s", code, body)
	}
}

// Re-check of #352 (fail closed): when the "running" Issue cannot be
// written, the Run stops before the restore call: nothing is revoked or
// restored, and the guest runs again.
func TestRestoreStopsWhenTheIssueCannotBeWritten(t *testing.T) {
	s := newSafetyEnv(t)
	e := s.e
	e.failWrites(t, "INSERT", "issue")
	status, out := s.restore(t, "3f2a9c0d1e4b5a67", false)
	if status != "failed" || !strings.HasPrefix(out, "nothing was changed: the Issue of the restore could not be written") || !strings.Contains(out, "runs again") {
		t.Fatalf("%s: %s", status, out)
	}
	if len(s.fr.restored) != 0 {
		t.Fatalf("the restore call ran: %v", s.fr.restored)
	}
	if obs, err := e.drv.Observe(e.ctx, driver.GuestID(s.ref)); err != nil || obs.Power != driver.PowerRunning {
		t.Fatalf("the guest does not run again: %+v %v", obs.Power, err)
	}
	if _, err := e.st.AgentByTarget(e.ctx, "web1"); err != nil {
		t.Fatalf("the agent credential went: %v", err)
	}
}

// A stop after the "running" Issue but before the restore call (the pins
// cannot be dropped) ends that Issue: the disks are as before.
func TestRestoreRunningIssueEndsBeforeTheCall(t *testing.T) {
	s := newSafetyEnv(t)
	b, _ := s.e.st.GetBinding(s.e.ctx, state.KindTarget, "web1")
	if ok, err := s.e.st.PinFirstHostKeys(s.e.ctx, "web1", b.GuestKey(), []string{"SHA256:old"}, time.Now()); err != nil || !ok {
		t.Fatalf("pin: %v %v", ok, err)
	}
	s.e.failWrites(t, "DELETE", "guest_host_key")
	status, out := s.restore(t, "3f2a9c0d1e4b5a67", false)
	if status != "failed" || !strings.Contains(out, "the host key pins could not be dropped") || len(s.fr.restored) != 0 {
		t.Fatalf("%s: %s", status, out)
	}
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Fatal("the Issue of a restore that never started stays open")
	}
}

// The host path of F3 (Issue 52 of T-225): a guest:config Issue of the
// empty guest is open (made by an older build), and the restore of the
// safety backup puts back a guest that matches. No poll report ever says
// "unchanged" for it; the Run itself ends the Issue, and the next poll
// opens nothing.
func TestRestoreEndsTheEmptyGuestIssueWithoutAPollReport(t *testing.T) {
	s := newSafetyEnv(t)
	e := s.e
	s.fr.fail = errors.New("proxmox: restore failed")
	s.restore(t, "3f2a9c0d1e4b5a67", false)
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: wire.GuestConfigID, Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "disk", From: 0, To: 10, Risk: wire.RiskDisruptive}}}}}); err != nil {
		t.Fatal(err)
	}
	stale, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID)
	if err != nil {
		t.Fatal(err)
	}
	s.fr.fail = nil
	if status, out := s.restore(t, "5afe000000000001", false); status != "succeeded" {
		t.Fatalf("%s %s", status, out)
	}
	if v := s.issue(t, stale.ID); v.Status != "resolved" || v.Resolution == nil || v.Resolution.By != "system" {
		t.Fatalf("the Issue of the empty guest stays: %+v", v)
	}
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Fatal(err)
	}
	if iss, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID); err == nil {
		t.Fatalf("a drift Issue of a guest that matches: %+v", iss)
	}
}
