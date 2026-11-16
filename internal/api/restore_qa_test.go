package api

// T-245 (C9 of T-236), the C5 part: a second opinion on the restore
// follow-ups of T-241 (D-420, D-427 … D-429, D-439) by tests that their
// authors did not write: the gate in each state of guest:restore-failed,
// the proof that ends with a later restore, a stop between the follow-up
// Plan and the end of the Issue, the safety backup that a restore keeps,
// and a restore that did not start.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// qaGateTry runs the four Plans of the gate check and returns their codes:
// a re-enroll of web1, a document Plan of web1, a Plan of web2 (another
// Target), and a restore Plan of web1 (the way back).
func qaGateTry(t *testing.T, s *safetyEnv) [4]int {
	t.Helper()
	var out [4]int
	out[0], _ = s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "qa"})
	out[1], _ = s.adm.do("POST", "/api/v1/plans", map[string]any{"reason": "qa", "put": []any{restoreTarget("web1", false)}}) // a real change
	out[2], _ = s.adm.do("POST", "/api/v1/plans", map[string]any{"reason": "qa", "put": []any{targetDoc("web2", "other")}})
	out[3], _ = s.adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "qa"})
	// Drop what was made, so the next try starts the same.
	pending, _ := s.e.st.ListPlans(s.e.ctx, store.PlanPending, 0, 100)
	for _, p := range pending {
		s.adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil)
	}
	return out
}

// D-439 by state: while guest:restore-failed is "running" or "failed",
// only the way back (a restore) passes for web1; in the "restored, the
// re-enroll was refused" state also a re-enroll of web1, and nothing else.
// A Plan of another Target passes in every state (the gate is per Target).
func TestQARestoreGateByState(t *testing.T) {
	s := newSafetyEnv(t)
	s.e.seed(s.adm, "web2")
	want := func(state string, got [4]int, reenroll int) {
		t.Helper()
		if got[0] != reenroll || got[1] != 409 || got[2] != 201 || got[3] != 201 {
			t.Errorf("%s: re-enroll %d (want %d), document Plan %d (want 409), other Target %d (want 201), restore %d (want 201)",
				state, got[0], reenroll, got[1], got[2], got[3])
		}
	}
	cutRestore(t, s, "")
	want("running", qaGateTry(t, s), 409)
	if err := s.e.eng.Recover(s.e.ctx); err != nil {
		t.Fatal(err)
	}
	iss, _ := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if !strings.Contains(string(iss.Changes), "failed: the controller stopped") {
		t.Fatalf("not failed: %s", iss.Changes)
	}
	want("failed", qaGateTry(t, s), 409)
}

// D-439 in the restored state, through a real Run (host D10): the
// re-enroll passes, and an apply token may approve it as before (it is
// disruptive). D-440: the disks are the restored ones, so a document Plan
// passes too.
func TestQARestoreGateRestoredState(t *testing.T) {
	s := newSafetyEnv(t)
	s.e.seed(s.adm, "web2")
	var oldIP string
	s.fr.after = func() {
		s.e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { oldIP, g.IP = g.IP, "192.0.2.99/24" })
	}
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
		t.Fatalf("restore: %s %s", status, out)
	}
	s.fr.after = nil
	s.e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { g.IP = oldIP })
	got := qaGateTry(t, s)
	if got[0] != 201 || got[1] != 201 || got[2] != 201 || got[3] != 201 {
		t.Fatalf("restored: %v, want [201 201 201 201]", got)
	}
	code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "qa"})
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code != 201 {
		t.Fatalf("re-enroll: %d %s", code, body)
	}
	if code, body := s.e.token(TierPlan, "").do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 403 {
		t.Errorf("a plan token approved the re-enroll: %d %s", code, body)
	}
	if code, body := s.e.token(TierApply, wire.RiskDisruptive).do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("an apply token (disruptive) may approve the re-enroll as before: %d %s", code, body)
	}
	s.e.eng.Wait()
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Error("the Issue stays after the re-enroll worked")
	}
}

// The proof is the stored step of the Run that the Issue names: a later
// restore that fails rewrites the Issue without it, and a re-enroll Plan
// made before is then refused at its approve. (Finding H1 of T-245 C5: on
// the #374 head this fails; the "restored" state counted as "failed
// before". #374 fixes it before the merge.)
func TestQARestoreProofEndsWithALaterRestore(t *testing.T) {
	s := newSafetyEnv(t)
	var oldIP string
	s.fr.after = func() {
		s.e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { oldIP, g.IP = g.IP, "192.0.2.99/24" })
	}
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
		t.Fatalf("restore: %s %s", status, out)
	}
	s.fr.after = nil
	s.e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { g.IP = oldIP })
	code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "qa"})
	if code != 201 {
		t.Fatalf("re-enroll: %d %s", code, body)
	}
	var re struct{ ID int64 }
	json.Unmarshal(body, &re)

	// The guest holds the good restored disks: the later restore takes a
	// safety backup first (D-420: only a broken guest may skip it).
	safeties := s.fr.safeties
	s.fr.fail = errors.New("proxmox: restore failed")
	if status, _ := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "failed" {
		t.Fatalf("the later restore: %s", status)
	}
	if s.fr.safeties != safeties+1 {
		t.Errorf("the restore of a restored guest took no safety backup")
	}
	iss, _ := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if !strings.Contains(string(iss.Changes), "failed: the guest may have no disk") || strings.Contains(string(iss.Changes), plan.AttrRestoreRun) {
		t.Fatalf("the Issue after the failed restore: %s", iss.Changes)
	}
	if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(re.ID)+"/approve", nil); code != 409 {
		t.Fatalf("the re-enroll Plan from before passed: %d %s", code, body)
	}
	if code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "qa"}); code != 409 || problemType(body) != "restore-failed" {
		t.Errorf("a new re-enroll after the failed restore: %d %s", code, body)
	}
}

// D-427: a stop after the follow-up Plan was made but before the Issue
// ended. The next start finds the pending re-enroll Plan: still one Plan,
// and the Issue ends with a comment.
func TestQARestoreStopAfterTheFollowUpPlan(t *testing.T) {
	s := newSafetyEnv(t)
	run := cutRestore(t, s, "restored backup 3f2a9c0d1e4b5a67 of 2026-11-15T03:00:12Z")
	// The state between the follow-up Plan and the end of the Issue.
	ch := []wire.Change{
		{Attr: "restore", From: "restored: the follow-up (a re-enroll Plan, or the install command of an adopted Target) comes next", To: "the follow-up of the restore", Risk: wire.RiskDisruptive},
		{Attr: plan.AttrRestoreRun, To: run.ID, Risk: wire.RiskRead},
	}
	if err := s.e.iss.Intake(s.e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: plan.GuestRestoreFailedID, Status: wire.StatusDiffers, Changes: ch}}}); err != nil {
		t.Fatal(err)
	}
	if code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "the follow-up"}); code != 201 {
		t.Fatalf("the follow-up Plan: %d %s", code, body)
	}
	for i := 0; i < 2; i++ {
		if err := s.e.eng.Recover(s.e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	re, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll)
	if len(re) != 1 {
		t.Fatalf("re-enroll Plans: %d", len(re))
	}
	// T-252: no second Plan is made, so the Issue stays open and names the
	// open one; the re-enroll Run that works ends it with a comment.
	iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil || !strings.Contains(string(iss.Changes), "approve the re-enroll Plan "+itoa(re[0].ID)) {
		t.Fatalf("issue %s %v", iss.Changes, err)
	}
	if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(re[0].ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	s.e.eng.Wait()
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Fatal("the Issue stays open after the re-enroll worked")
	}
	if c := lastComment(t, s, plan.GuestRestoreFailedID); c == "" {
		t.Error("the Issue ended without a comment (D-428)")
	}
}

// D-420/D-429: a restore that worked keeps one safety backup. With a new
// safety backup of its own, it keeps that one; a restore of the safety
// backup after a failed restore takes none and keeps the restored one.
func TestQARestoreKeepsTheRightSafetyBackup(t *testing.T) {
	s := newSafetyEnv(t)
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
		t.Fatalf("restore: %s %s", status, out)
	}
	if len(s.fr.dropped) != 1 || s.fr.dropped[0] != "bk:safety-1" {
		t.Fatalf("kept after a restore with its own safety backup: %v", s.fr.dropped)
	}
	s.fr.dropped = nil
	s.fr.fail = errors.New("proxmox: restore failed")
	if status, _ := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "failed" {
		t.Fatal("the restore did not fail")
	}
	if len(s.fr.dropped) != 0 {
		t.Fatalf("a failed restore deleted safety backups (keep %v)", s.fr.dropped)
	}
	s.fr.fail = nil
	before := s.fr.safeties
	// The way back: the safety backup that the failed restore took.
	if status, out := s.restore(t, "5afe000000000002", false); status != "succeeded" {
		t.Fatalf("the way back: %s %s", status, out)
	}
	if s.fr.safeties != before {
		t.Errorf("the way back after a failed restore took a new safety backup")
	}
	if len(s.fr.dropped) != 1 || s.fr.dropped[0] != "bk:safety-2" {
		t.Fatalf("kept after the way back: %v, want the restored volume bk:safety-2", s.fr.dropped)
	}
}

// A restore that did not start (Proxmox refused before it changed a disk):
// the Run fails with that text, no Issue opens, so the gate does not
// close; the guest runs again.
func TestQARestoreNotStartedLeavesNoGate(t *testing.T) {
	s := newSafetyEnv(t)
	s.e.seed(s.adm, "web2")
	s.fr.fail = fmt.Errorf("%w: proxmox refused the restore before it began", plan.ErrRestoreNotStarted)
	status, out := s.restore(t, "3f2a9c0d1e4b5a67", false)
	if status != "failed" || !strings.Contains(out, "did not start") || !strings.Contains(out, "disks are as before") || strings.Contains(out, "may have no disk") {
		t.Fatalf("run %s: %s", status, out)
	}
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Fatal("an Issue for a restore that did not start")
	}
	if st, _ := s.e.drv.Observe(s.e.ctx, driver.GuestID(s.ref)); st.Power != driver.PowerRunning {
		t.Errorf("the guest is %s", st.Power)
	}
	if code, body := s.adm.do("POST", "/api/v1/plans", map[string]any{"reason": "qa", "put": []any{restoreTarget("web1", true)}}); code == 409 && problemType(body) == "restore-failed" {
		t.Errorf("the gate is closed after a restore that did not start: %s", body)
	}
}

// From the restored state, a restore that does not start (Proxmox proved
// that it changed nothing) puts the restored state back: the old proof is
// true again (the disks are still the restored ones), so the re-enroll
// passes; a document Plan stays 409.
func TestQARestoreNotStartedFromRestoredState(t *testing.T) {
	s := newSafetyEnv(t)
	var oldIP string
	s.fr.after = func() {
		s.e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { oldIP, g.IP = g.IP, "192.0.2.99/24" })
	}
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
		t.Fatalf("restore: %s %s", status, out)
	}
	s.fr.after = nil
	s.e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { g.IP = oldIP })
	s.fr.fail = fmt.Errorf("%w: proxmox refused the restore before it began", plan.ErrRestoreNotStarted)
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "failed" || !strings.Contains(out, "did not start") {
		t.Fatalf("the restore that did not start: %s %s", status, out)
	}
	iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil || !strings.Contains(string(iss.Changes), plan.RestoredRefusedPrefix) || !strings.Contains(string(iss.Changes), plan.AttrRestoreRun) {
		t.Fatalf("the Issue after it: %s %v", iss.Changes, err)
	}
	if code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "qa"}); code != 201 {
		t.Errorf("the re-enroll after a restore that did not start: %d %s", code, body)
	}
	// D-440: the restored state is back with its proof, so a document Plan passes.
	if code, body := s.adm.do("POST", "/api/v1/plans", map[string]any{"reason": "qa", "put": []any{restoreTarget("web1", false)}}); code != 201 {
		t.Errorf("a document Plan: %d %s", code, body)
	}
}
