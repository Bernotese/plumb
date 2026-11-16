package api

// T-241 part 2: D-427 (the follow-up of a restore survives a controller
// stop), D-439 (a refused re-enroll stays visible and only a re-enroll
// passes the gate), one re-enroll Plan (host D12), the network pre-check
// (T-244 R1) and D-428 (every system resolution has a comment).

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// cutRestore makes the state that a controller stop leaves: a restore Run
// still "running" in the store (not active in this process), its
// guest:config step with output, and the "running" restore Issue.
func cutRestore(t *testing.T, s *safetyEnv, output string) store.Run {
	t.Helper()
	e := s.e
	head, _ := e.st.Head(e.ctx)
	docs, _ := e.st.RevisionDocs(e.ctx, head.ID)
	changes, _ := json.Marshal([]plan.TargetPlan{{Target: "web1", Resources: []plan.ResourceChange{{Resource: wire.GuestConfigID, Action: wire.ActionUpdate,
		Risk: wire.RiskDestructive, Changes: []wire.Change{{Attr: plan.AttrRestore, To: "backup 3f2a9c0d1e4b5a67 of 2026-11-15T03:00:12Z", Risk: wire.RiskDestructive}}}}}})
	p, err := e.st.CreatePlan(e.ctx, store.Plan{BaseRevision: head.ID, Origin: plan.OriginRestore, Risk: "destructive", Author: "user:admin", Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := e.st.ApprovePlan(e.ctx, p.ID, "user:admin", store.NewRevision{Author: "user:admin", Docs: docs})
	if err != nil {
		t.Fatal(err)
	}
	seq, err := e.st.AddStep(e.ctx, store.Step{RunID: run.ID, Target: "web1", Resource: wire.GuestConfigID, Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpdateStep(e.ctx, run.ID, seq, "running", nil, output); err != nil { // as the stepper writes it
		t.Fatal(err)
	}
	ch := []wire.Change{{Attr: "restore", From: "running: if this restore does not end, the guest may have no disk", To: "restore the safety backup 5afe000000000001", Risk: wire.RiskDestructive}}
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: plan.GuestRestoreFailedID, Status: wire.StatusDiffers, Changes: ch}}}); err != nil {
		t.Fatal(err)
	}
	return run
}

func lastComment(t *testing.T, s *safetyEnv, resource string) string {
	t.Helper()
	is, _ := s.e.st.ListIssues(s.e.ctx, store.IssueResolved, "web1", 0, 100) // newest first
	for i := 0; i < len(is); i++ {
		if is[i].Resource == resource {
			cs, _ := s.e.st.Comments(s.e.ctx, is[i].ID)
			if len(cs) == 0 {
				return ""
			}
			return cs[len(cs)-1].Body
		}
	}
	return ""
}

// D-427: a stop after the restore call (the step stored "restored"): the
// start makes exactly one re-enroll Plan and ends the Issue with a reason.
func TestRestoreStopAfterTheCall(t *testing.T) {
	s := newSafetyEnv(t)
	run := cutRestore(t, s, "restored backup 3f2a9c0d1e4b5a67 of 2026-11-15T03:00:12Z")
	for i := 0; i < 2; i++ { // a second start changes nothing
		if err := s.e.eng.Recover(s.e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if re, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll); len(re) != 1 {
		iss, _ := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
		t.Fatalf("re-enroll Plans: %d; issue %s", len(re), iss.Changes)
	}
	// D-440: the Issue waits in the "restored" state for the re-enroll Run.
	if !s.openIssue(plan.GuestRestoreFailedID) {
		t.Fatal("the Issue ended at the Plan, not at its Run")
	}
	re := s.reenrollDone(t)
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Fatal("the Issue stays open after the re-enroll Run worked")
	}
	if c := lastComment(t, s, plan.GuestRestoreFailedID); !strings.Contains(c, "the re-enroll Plan "+itoa(re)+" worked after the restore") {
		t.Errorf("comment %q", c)
	}
	steps, _ := s.e.st.Steps(s.e.ctx, run.ID)
	if steps[0].Status != "applied" || !strings.HasPrefix(steps[0].Output, "restored backup") ||
		!strings.Contains(steps[0].Output, "the restore worked, then a controller stop cut the Run; the follow-up was made at the next start") {
		t.Errorf("the restored step: %+v", steps[0])
	}
	// T-253 (host step 4): the Run does not contradict its step. The
	// restore worked, so the Run succeeded, and nothing says to restore
	// again.
	if r, _ := s.e.st.GetRun(s.e.ctx, run.ID); r.Status != "succeeded" {
		t.Errorf("the cut restore Run: %s, want succeeded", r.Status)
	}
	if strings.Contains(strings.ToLower(steps[0].Output), "restore again") || strings.Contains(steps[0].Output, "may have no disk") {
		t.Errorf("the step tells a person to restore again: %q", steps[0].Output)
	}
}

// D-427: a stop inside the PVE task (nothing stored): the Issue becomes
// "failed", no re-enroll, the gate holds.
func TestRestoreStopInsideTheCall(t *testing.T) {
	s := newSafetyEnv(t)
	run := cutRestore(t, s, "")
	if err := s.e.eng.Recover(s.e.ctx); err != nil {
		t.Fatal(err)
	}
	// Nothing stored "restored": the Run stays failed (T-253).
	if r, _ := s.e.st.GetRun(s.e.ctx, run.ID); r.Status != "failed" {
		t.Errorf("a restore Run cut inside the task: %s, want failed", r.Status)
	}
	iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil || !strings.Contains(string(iss.Changes), "failed: the controller stopped during the restore") || !strings.Contains(string(iss.Changes), "5afe000000000001") {
		t.Fatalf("issue %s %v", iss.Changes, err)
	}
	if re, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll); len(re) != 0 {
		t.Fatalf("a re-enroll after a cut restore call: %d", len(re))
	}
	if code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "r"}); code != 409 || problemType(body) != "restore-failed" {
		t.Errorf("re-enroll through the gate: %d %s", code, body)
	}
}

// D-439: the restore worked, the re-enroll Plan was refused. The Issue
// stays open with the state and the way out; only a re-enroll Plan of the
// Target passes; its working Run ends the Issue with a comment.
func TestRestoredReenrollRefused(t *testing.T) {
	s := newSafetyEnv(t)
	// As host D10: the restored guest holds something that the re-enroll
	// Diff refuses (here a create-only address changed in Proxmox).
	var oldIP string
	s.fr.after = func() {
		s.e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { oldIP, g.IP = g.IP, "192.0.2.99/24" })
	}
	status, out := s.restore(t, "3f2a9c0d1e4b5a67", false)
	if status != "succeeded" {
		t.Fatalf("restore: %s %s", status, out)
	}
	iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil || !strings.Contains(string(iss.Changes), plan.RestoredRefusedPrefix+"the re-enroll Plan was refused") {
		t.Fatalf("issue %s %v", iss.Changes, err)
	}
	v := s.issue(t, iss.ID)
	if !strings.Contains(v.ActionsNote, "POST /targets/web1/reenroll") {
		t.Errorf("actions note %q", v.ActionsNote)
	}
	// Plumb's reason is cut (it can list config lines; condition 3).
	if n := len([]rune(v.ActionsNote)); n > 300+400 {
		t.Errorf("the note is not cut: %d runes", n)
	}
	// D-440: the gate is open in the restored state; another Plan of the
	// Target meets only the planning's own checks (here the create-only
	// address that the guest holds).
	code, body := s.adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "put": []any{restoreTarget("web1", false)}})
	if problemType(body) == "restore-failed" {
		t.Errorf("another Plan: %d %s", code, body)
	}
	// A person fixes it in Proxmox; the re-enroll Plan passes; who
	// approves it is unchanged (D-439 (6)).
	s.e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { g.IP = oldIP })
	code, body = s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "after the restore"})
	if code != 201 {
		t.Fatalf("re-enroll: %d %s", code, body)
	}
	var p struct {
		ID   int64
		Risk string
	}
	json.Unmarshal(body, &p)
	if p.Risk != "disruptive" {
		t.Errorf("re-enroll risk %s", p.Risk)
	}
	if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	s.e.eng.Wait()
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Fatal("the Issue stays after the re-enroll worked")
	}
	if c := lastComment(t, s, plan.GuestRestoreFailedID); !strings.Contains(c, "re-enroll Plan") || !strings.Contains(c, "worked after the restore") {
		t.Errorf("comment %q", c)
	}
}

// Host D12: two restores in a row make one re-enroll Plan.
func TestOneReenrollPlan(t *testing.T) {
	s := newSafetyEnv(t)
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
		t.Fatalf("first: %s %s", status, out)
	}
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
		t.Fatalf("second: %s %s", status, out)
	}
	re, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll)
	if len(re) != 1 {
		t.Fatalf("re-enroll Plans: %d", len(re))
	}
	// T-252: no second Plan, so the Issue stays open and names the open one.
	iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil || !strings.Contains(string(iss.Changes), "approve the re-enroll Plan "+itoa(re[0].ID)) {
		t.Fatalf("issue %s %v", iss.Changes, err)
	}
	if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(re[0].ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	s.e.eng.Wait()
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Error("the Issue stays after the re-enroll worked")
	}
}

// T-244 R1: the network pre-check refuses a restore whose backup has a
// NIC on a bridge the install does not name: at the Plan (409) and again
// in the Run before the stop (nothing is changed, the guest runs on).
func TestRestoreNetworkPreCheck(t *testing.T) {
	narrow := false
	dr := &diskRestorer{fakeRestorer: &fakeRestorer{vols: map[string]plan.RestoreVolume{}}, cfg: map[string]string{"net0": "virtio=02:00:00:00:00:01,bridge=vmbr0", "net1": "virtio=02:00:00:00:00:02,bridge=vmbr9,tag=20"}}
	e := newEnv(t, true, func(c *plan.Config) {
		c.GuestRestore = dr
		c.InstallID = "iid"
		c.NetAllowed = func(b string, tag int) bool { return b == "vmbr0" || (b == "vmbr9" && tag == 20 && !narrow) }
	})
	adm := e.admin()
	e.seed(adm, "web1")
	e.approve(adm, "allow restart", restoreTarget("web1", true))
	b, _ := e.st.GetBinding(e.ctx, "Target", "web1")
	lineage, _ := e.st.BackupLineage(e.ctx, "web1", b.Gen)
	run, _ := e.st.StartGuestBackupRun(e.ctx, "web1", lineage, "scheduled")
	at := time.Date(2026, 11, 15, 3, 0, 12, 0, time.UTC)
	e.st.FinishGuestBackupRun(e.ctx, run, "done", "bk:v1", 100, at.Unix(), "")
	dr.vols["3f2a9c0d1e4b5a67"] = plan.RestoreVolume{ID: "3f2a9c0d1e4b5a67", Volid: "bk:v1", Kind: "plumb", Notes: "plumb:iid:web1:" + lineage, Time: at, Size: 100}

	// Allowed now: the Plan is made. Then a repair run drops the tag.
	code, body := adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r"})
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	narrow = true
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	r, _ := e.st.RunOfPlan(e.ctx, p.ID)
	steps, _ := e.st.Steps(e.ctx, r.ID)
	out := steps[len(steps)-1].Output
	if r.Status != "failed" || !strings.Contains(out, "bridge vmbr9 tag 20") || !strings.Contains(out, "--extra-vlan vmbr9:20") || !strings.Contains(out, "Nothing was changed") {
		t.Fatalf("run %s: %s", r.Status, out)
	}
	if len(dr.restored) != 0 || dr.safeties != 0 {
		t.Errorf("restored %v, safety backups %d", dr.restored, dr.safeties)
	}
	if st, _ := e.drv.Observe(e.ctx, driver.GuestID(b.Ref)); st.Power != "running" {
		t.Errorf("the guest was stopped: %s", st.Power)
	}
	if _, err := e.st.OpenIssue(e.ctx, "web1", plan.GuestRestoreFailedID); err == nil {
		t.Error("an Issue for a restore that never started")
	}
	// The next Plan is refused at once.
	code, body = adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r"})
	if code != 409 || problemType(body) != "not-restorable" || !strings.Contains(string(body), "bridge vmbr9 tag 20") {
		t.Errorf("propose after the repair run: %d %s", code, body)
	}
}

// D-428 sweep: after restores, drift and a Rule, every Issue that the
// system resolved has a comment (host case R4: an Issue ended "gone"
// with no word).
func TestSystemResolutionsHaveComments(t *testing.T) {
	s := newSafetyEnv(t)
	e := s.e
	// Drift that goes away on its own.
	e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { g.Cores = 7 })
	e.eng.CheckGuests(e.ctx)
	e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { g.Cores = 2 })
	e.eng.CheckGuests(e.ctx)
	// A failed restore, then the way back.
	s.fr.fail = errors.New("restore failed")
	s.restore(t, "3f2a9c0d1e4b5a67", false)
	s.fr.fail = nil
	s.restore(t, "5afe000000000001", false)
	s.reenrollDone(t) // D-440: the re-enroll Run ends that Issue
	// A cut restore found at a start.
	cutRestore(t, s, "restored backup 3f2a9c0d1e4b5a67 of 2026-11-15T03:00:12Z")
	e.eng.Recover(e.ctx)
	// The follow-up of the cut restore made a new re-enroll Plan; its Run
	// is one more system end with a comment.
	re, _ := e.st.PendingPlans(e.ctx, plan.OriginReenroll)
	if len(re) > 0 {
		newest := re[0].ID
		for _, p := range re {
			newest = max(newest, p.ID)
		}
		if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(newest)+"/approve", nil); code != 202 {
			t.Fatalf("approve re-enroll: %d %s", code, body)
		}
		e.eng.Wait()
	}
	resolved, _ := e.st.ListIssues(e.ctx, store.IssueResolved, "", 0, 1000)
	n := 0
	for _, iss := range resolved {
		if iss.ResolvedBy != "system" {
			continue
		}
		n++
		if cs, _ := e.st.Comments(e.ctx, iss.ID); len(cs) == 0 {
			t.Errorf("Issue %d (%s, %s) resolved by the system without a comment", iss.ID, iss.Resource, iss.Resolution)
		}
	}
	if n < 3 {
		t.Fatalf("only %d system resolutions; the sweep proves little", n)
	}
}

// proofState writes a guest:restore-failed Issue of web1 in the "restored,
// re-enroll refused" state that names a Run with a guest:config step of
// target with output, made for a Plan of origin.
func proofState(t *testing.T, s *safetyEnv, origin, target, output string) {
	t.Helper()
	e := s.e
	head, _ := e.st.Head(e.ctx)
	docs, _ := e.st.RevisionDocs(e.ctx, head.ID)
	p, err := e.st.CreatePlan(e.ctx, store.Plan{BaseRevision: head.ID, Origin: origin, Risk: "destructive", Author: "user:admin", Changes: json.RawMessage("[]")})
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := e.st.ApprovePlan(e.ctx, p.ID, "user:admin", store.NewRevision{Author: "user:admin", Docs: docs})
	if err != nil {
		t.Fatal(err)
	}
	seq, _ := e.st.AddStep(e.ctx, store.Step{RunID: run.ID, Target: target, Resource: wire.GuestConfigID, Status: "running"})
	e.st.UpdateStep(e.ctx, run.ID, seq, "applied", nil, output)
	e.st.FinishRun(e.ctx, run.ID, "succeeded", 0)
	ch := []wire.Change{
		{Attr: "restore", From: plan.RestoredRefusedPrefix + "the re-enroll Plan was refused", To: "remove the addition in Proxmox, then POST /targets/web1/reenroll", Risk: wire.RiskDisruptive},
		{Attr: plan.AttrRestoreRun, To: run.ID, Risk: wire.RiskRead},
	}
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: plan.GuestRestoreFailedID, Status: wire.StatusDiffers, Changes: ch}}}); err != nil {
		t.Fatal(err)
	}
}

// L1 of the #374 review: the gate's proof comes only from a restore Run
// of this Target. A "restored" step of a Plan that is no restore, or of
// another Target, never opens it.
func TestRestoredProofOnlyFromThisTargetsRestore(t *testing.T) {
	for name, c := range map[string]struct{ origin, target string }{
		"not a restore Plan": {plan.OriginReenroll, "web1"},
		"another Target":     {plan.OriginRestore, "web2"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSafetyEnv(t)
			proofState(t, s, c.origin, c.target, "restored backup 3f2a9c0d1e4b5a67 of 2026-11-15T03:00:12Z")
			code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "r"})
			if code != 409 || problemType(body) != "restore-failed" {
				t.Errorf("re-enroll through the gate: %d %s", code, body)
			}
		})
	}
	// The control: a restore Run of this Target opens it.
	s := newSafetyEnv(t)
	proofState(t, s, plan.OriginRestore, "web1", "restored backup 3f2a9c0d1e4b5a67 of 2026-11-15T03:00:12Z")
	if code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "r"}); code != 201 {
		t.Errorf("the proven case: %d %s", code, body)
	}
}

// toRestoredRefused brings web1 into the D-439 state "restored; the agent
// is not enrolled" (as TestRestoredReenrollRefused) and returns a function
// that undoes the cause in Proxmox.
func toRestoredRefused(t *testing.T, s *safetyEnv) (fix func()) {
	t.Helper()
	var oldIP string
	s.fr.after = func() {
		s.e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { oldIP, g.IP = g.IP, "192.0.2.99/24" })
	}
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" {
		t.Fatalf("restore: %s %s", status, out)
	}
	s.fr.after = nil
	iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil || !strings.Contains(string(iss.Changes), plan.RestoredRefusedPrefix) {
		t.Fatalf("not in the restored state: %s %v", iss.Changes, err)
	}
	return func() { s.e.drv.Tamper(driver.GuestID(s.ref), func(g *driver.GuestSpec) { g.IP = oldIP }) }
}

// High QA finding of #374: a restore from the "restored" state is a normal
// restore: it takes a safety backup; when it fails, the Issue says
// "failed" without the old Run, and the gate refuses every re-enroll, also
// one planned before.
func TestRestoreFromRestoredStateFails(t *testing.T) {
	s := newSafetyEnv(t)
	fix := toRestoredRefused(t, s)
	fix()
	code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "planned before"})
	if code != 201 {
		t.Fatalf("re-enroll in the restored state: %d %s", code, body)
	}
	var re struct{ ID int64 }
	json.Unmarshal(body, &re)
	safeties := s.fr.safeties
	s.fr.fail = errors.New("restore failed")
	// The old proof ends at the START of the new restore: during the call
	// the Issue says "running" and names no Run.
	var during string
	s.fr.during = func() {
		if iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID); err == nil {
			during = string(iss.Changes)
		}
	}
	status, out := s.restore(t, "3f2a9c0d1e4b5a67", false)
	if !strings.Contains(during, "running: if this restore does not end") || strings.Contains(during, plan.AttrRestoreRun) {
		t.Errorf("the Issue during the new restore call: %s", during)
	}
	if status != "failed" || s.fr.safeties != safeties+1 {
		t.Fatalf("restore: %s %s, safety backups %d → %d", status, out, safeties, s.fr.safeties)
	}
	iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil || !strings.Contains(string(iss.Changes), "failed: the guest may have no disk") || strings.Contains(string(iss.Changes), plan.AttrRestoreRun) {
		t.Fatalf("issue %s %v", iss.Changes, err)
	}
	if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(re.ID)+"/approve", nil); code != 409 || problemType(body) != "restore-failed" {
		t.Errorf("the re-enroll planned before: %d %s", code, body)
	}
	if code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "r"}); code != 409 || problemType(body) != "restore-failed" {
		t.Errorf("a new re-enroll: %d %s", code, body)
	}
}

// A restore from the "restored" state that does not start leaves the guest
// as before: the Issue goes back to the "restored" state with its proof.
func TestRestoreFromRestoredStateNotStarted(t *testing.T) {
	s := newSafetyEnv(t)
	fix := toRestoredRefused(t, s)
	before, _ := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	s.fr.fail = fmt.Errorf("%w: storage busy", plan.ErrRestoreNotStarted)
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "failed" || !strings.Contains(out, "did not start") {
		t.Fatalf("restore: %s %s", status, out)
	}
	iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil || string(iss.Changes) != string(before.Changes) {
		t.Fatalf("issue %s, was %s (%v)", iss.Changes, before.Changes, err)
	}
	fix()
	if code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "r"}); code != 201 {
		t.Errorf("re-enroll after a restore that did not start: %d %s", code, body)
	}
}

// A controller stop inside a restore from the "restored" state: the
// "running" Issue replaced the old proof at the start, so the start rule
// says "failed" and the gate refuses the re-enroll.
func TestRestoreFromRestoredStateStopInside(t *testing.T) {
	s := newSafetyEnv(t)
	toRestoredRefused(t, s)
	cutRestore(t, s, "") // the new restore wrote "running" and was cut inside the call
	if err := s.e.eng.Recover(s.e.ctx); err != nil {
		t.Fatal(err)
	}
	iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil || !strings.Contains(string(iss.Changes), "failed: the controller stopped during the restore") {
		t.Fatalf("issue %s %v", iss.Changes, err)
	}
	if code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "r"}); code != 409 {
		t.Errorf("re-enroll: %d %s", code, body)
	}
}

// A restore from the "restored" state that works is a normal one: the
// safety backup, then the follow-up ends the Issue.
func TestRestoreFromRestoredStateWorks(t *testing.T) {
	s := newSafetyEnv(t)
	fix := toRestoredRefused(t, s)
	fix()
	safeties := s.fr.safeties
	if status, out := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "succeeded" || s.fr.safeties != safeties+1 {
		t.Fatalf("restore: %s %s, safety backups %d → %d", status, out, safeties, s.fr.safeties)
	}
	if re, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll); len(re) != 1 {
		t.Errorf("re-enroll Plans: %d", len(re))
	}
	s.reenrollDone(t) // D-440: the Issue ends at the re-enroll Run
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Error("the Issue stays after the re-enroll Run worked")
	}
}

// reenrollDone approves the newest pending re-enroll Plan of web1 and waits
// for its Run (D-440: the restore Issue ends only at that Run).
func (s *safetyEnv) reenrollDone(t *testing.T) int64 {
	t.Helper()
	ps, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll)
	var id int64
	for _, p := range ps {
		var tps []plan.TargetPlan
		if json.Unmarshal(p.Changes, &tps) == nil && len(tps) == 1 && tps[0].Target == "web1" && p.ID > id {
			id = p.ID
		}
	}
	if id == 0 {
		t.Fatal("no pending re-enroll Plan of web1")
	}
	if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
		t.Fatalf("approve the re-enroll %d: %d %s", id, code, body)
	}
	s.e.eng.Wait()
	if r, _ := s.e.st.RunOfPlan(s.e.ctx, id); r.Status != "succeeded" {
		steps, _ := s.e.st.Steps(s.e.ctx, r.ID)
		var outs []string
		for _, st := range steps {
			if st.Status != "applied" && st.Status != "unchanged" {
				outs = append(outs, st.Resource+": "+st.Output)
			}
		}
		t.Fatalf("the re-enroll Run %d: %s %v", r.ID, r.Status, outs)
	}
	return id
}
