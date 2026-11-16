package api

// T-232 (D-420, host finding F2 of T-225): a restore takes a safety backup
// of the guest first; a failed restore says that the guest may be empty,
// opens guest:restore-failed and names the safety backup as the way back;
// the safety backup can be restored; the skip is only for a broken guest.
// S3 of T-225: a Target whose guest is gone gets a clear refusal.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

type safetyEnv struct {
	e       *env
	fr      *fakeRestorer
	adm     *client
	lineage string
	ref     string
}

// newSafetyEnv seeds web1 (allow_restart) with one recorded scheduled
// backup (id 3f2a…) and a restorer whose safety backup is a real recorded
// pre-restore volume.
func newSafetyEnv(t *testing.T) *safetyEnv {
	t.Helper()
	fr := &fakeRestorer{vols: map[string]plan.RestoreVolume{}}
	e := newEnv(t, true, func(c *plan.Config) { c.GuestRestore = fr; c.InstallID = "iid" })
	adm := e.admin()
	e.seed(adm, "web1")
	e.approve(adm, "allow restart", restoreTarget("web1", true))
	ctx := context.Background()
	b, _ := e.st.GetBinding(ctx, state.KindTarget, "web1")
	lineage, _ := e.st.BackupLineage(ctx, "web1", b.Gen)
	at := time.Date(2026, 11, 15, 3, 0, 12, 0, time.UTC)
	run, _ := e.st.StartGuestBackupRun(ctx, "web1", lineage, "scheduled")
	e.st.FinishGuestBackupRun(ctx, run, "done", "bk:v1", 100, at.Unix(), "")
	fr.vols["3f2a9c0d1e4b5a67"] = plan.RestoreVolume{ID: "3f2a9c0d1e4b5a67", Volid: "bk:v1", Kind: "plumb", Notes: "plumb:iid:web1:" + lineage, Time: at, Size: 100}
	n := 0
	fr.safety = func() (plan.RestoreVolume, error) { // a recorded pre-restore job, like backup.Guests.SafetyBackup
		n++
		volid := fmt.Sprintf("bk:safety-%d", n)
		when := at.Add(time.Duration(n) * time.Hour)
		id, err := e.st.StartGuestBackupRun(ctx, "web1", lineage, "pre-restore")
		if err != nil {
			return plan.RestoreVolume{}, err
		}
		e.st.FinishGuestBackupRun(ctx, id, "done", volid, 200, when.Unix(), "")
		v := plan.RestoreVolume{ID: fmt.Sprintf("5afe00000000000%d", n), Volid: volid, Kind: "plumb", Notes: "plumb:iid:web1:" + lineage + ":r", Time: when, Size: 200}
		fr.vols[v.ID] = v
		return v, nil
	}
	return &safetyEnv{e: e, fr: fr, adm: adm, lineage: lineage, ref: b.Ref}
}

// restore proposes (admin) and approves a restore, waits, and returns the
// Run status and the step output.
func (s *safetyEnv) restore(t *testing.T, id string, skip bool) (string, string) {
	t.Helper()
	code, body := s.adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": id, "reason": "r", "skip_safety_backup": skip})
	if code != 201 {
		t.Fatalf("propose %s: %d %s", id, code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	s.e.eng.Wait()
	r, _ := s.e.st.RunOfPlan(s.e.ctx, p.ID)
	steps, _ := s.e.st.Steps(s.e.ctx, r.ID)
	return r.Status, steps[len(steps)-1].Output
}

func (s *safetyEnv) openIssue(resource string) bool {
	_, err := s.e.st.OpenIssue(s.e.ctx, "web1", resource)
	return err == nil
}

// The Plan says what Proxmox does and that a safety backup comes first.
func TestRestorePlanSaysSafetyBackup(t *testing.T) {
	s := newSafetyEnv(t)
	code, body := s.adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r"})
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	for _, w := range []string{"Proxmox removes the guest's disks before it writes the backup: if the restore fails, the guest is left empty",
		"Plumb first takes a safety backup of the guest as it is now (about 1 KiB on the backup storage", "if that backup fails, nothing is changed",
		`"attr":"safety_backup","to":"first, of the guest as it is now"`} {
		if !strings.Contains(string(body), w) {
			t.Errorf("plan without %q:\n%s", w, body)
		}
	}
}

// The safety backup fails: the Run stops before the stop, the revoke and
// the restore; the guest runs and keeps its agent.
func TestRestoreSafetyBackupFails(t *testing.T) {
	s := newSafetyEnv(t)
	var powerAtBackup driver.Power
	s.fr.safety = func() (plan.RestoreVolume, error) {
		st, _ := s.e.drv.Observe(s.e.ctx, driver.GuestID(s.ref))
		powerAtBackup = st.Power
		return plan.RestoreVolume{}, errors.New("backup: guest backup refused: the storage has 4 % free, less than 10 %")
	}
	status, out := s.restore(t, "3f2a9c0d1e4b5a67", false)
	if status != "failed" || out != "nothing was changed: the safety backup before the restore failed: the storage has 4 % free, less than 10 %. The guest was stopped for it and runs again" {
		t.Fatalf("%s: %q", status, out)
	}
	if len(s.fr.restored) != 0 {
		t.Errorf("restored: %v", s.fr.restored)
	}
	if powerAtBackup != driver.PowerStopped { // design point e: stop first, then the safety backup
		t.Errorf("the safety backup ran on a %s guest", powerAtBackup)
	}
	if st, _ := s.e.drv.Observe(s.e.ctx, driver.GuestID(s.ref)); st.Power != driver.PowerRunning {
		t.Errorf("guest power %s", st.Power)
	}
	if _, err := s.e.st.AgentByTarget(s.e.ctx, "web1"); err != nil {
		t.Errorf("the agent was revoked: %v", err)
	}
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Error("guest:restore-failed without a restore")
	}
}

// The restore fails after Proxmox began: the text says the guest may be
// empty and names the safety backup; guest:restore-failed opens; no raw
// Proxmox text. Then the safety backup is restored: the Issue ends, the
// re-enroll Plan follows, only the newest safety backup stays.
func TestRestoreFailsThenSafetyBackupRestores(t *testing.T) {
	s := newSafetyEnv(t)
	s.fr.fail = errors.New("proxmox: task UPID:pve:0001:0002:0003:qmrestore:9001:plumb@pve!plumb: failed: command 'zstd -d /var/lib/plumb-guest-backup/dump/x.vma.zst' failed: Restored data doesn't match checksum")
	status, out := s.restore(t, "3f2a9c0d1e4b5a67", false)
	want := []string{"the agent credential is revoked; the restore failed: the Proxmox task UPID:pve:0001:0002:0003:qmrestore:9001:plumb@pve!plumb: failed (see its log in Proxmox)",
		"the guest may have no disk now", "restore the safety backup 5afe000000000001 of 2026-11-15T04:00:12Z (POST /targets/web1/restore with backup 5afe000000000001)",
		"re-enroll only after a restore that worked"}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output without %q:\n%s", w, out)
		}
	}
	for _, leak := range []string{"/var/lib", "zstd", "checksum"} {
		if strings.Contains(out, leak) {
			t.Errorf("raw Proxmox text %q in the output: %s", leak, out)
		}
	}
	if status != "failed" || !s.openIssue(plan.GuestRestoreFailedID) {
		t.Fatalf("%s, guest:restore-failed open %v", status, s.openIssue(plan.GuestRestoreFailedID))
	}
	if re, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll); len(re) != 0 {
		t.Errorf("a re-enroll Plan after a failed restore: %v", re)
	}
	// The way back: restore the safety backup. The guest may be empty, so
	// no new safety backup is taken and none is deleted (D1 of #346).
	s.fr.fail = nil
	status, out = s.restore(t, "5afe000000000001", false)
	if status != "succeeded" || !strings.Contains(out, "restored backup 5afe000000000001") || strings.Contains(out, "The safety backup") || s.fr.safeties != 1 {
		t.Fatalf("%s: %s (safety backups %d)", status, out, s.fr.safeties)
	}
	// D-440: the Issue ends at the re-enroll Run, not at the restore.
	if !s.openIssue(plan.GuestRestoreFailedID) {
		t.Error("guest:restore-failed ended before the re-enroll")
	}
	// D-429 (amends D1 of #346): the restored pre-restore volume is the
	// kept one; only older safety backups go, never a newer one.
	if len(s.fr.dropped) != 1 || s.fr.dropped[0] != "bk:safety-1" {
		t.Errorf("safety backups dropped: %v, want keep bk:safety-1", s.fr.dropped)
	}
	if re, _ := s.e.st.PendingPlans(s.e.ctx, plan.OriginReenroll); len(re) != 1 {
		t.Errorf("re-enroll Plans: %v", re)
	}
	s.reenrollDone(t)
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Error("guest:restore-failed stays open after the re-enroll Run worked")
	}
	// The next restore of the healthy guest takes a new safety backup and
	// replaces the old one (exactly one per Target, point a).
	status, _ = s.restore(t, "3f2a9c0d1e4b5a67", false)
	if status != "succeeded" || s.fr.safeties != 2 || len(s.fr.dropped) != 2 || s.fr.dropped[1] != "bk:safety-2" {
		t.Errorf("next restore: %s, safeties %d, dropped %v", status, s.fr.safeties, s.fr.dropped)
	}
}

// A second failure in a row (D1 of #346): the guest may be empty after the
// first, so the second Run takes no backup of it, deletes none, and the
// Issue and the text keep naming the good safety backup from before the
// first failure. The skip is refused on a healthy guest.
func TestRestoreSecondFailureKeepsTheGoodBackup(t *testing.T) {
	s := newSafetyEnv(t)
	code, body := s.adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r", "skip_safety_backup": true})
	if code != 422 || !strings.Contains(string(body), "only for a broken guest") {
		t.Fatalf("skip on a healthy guest: %d %s", code, body)
	}
	s.fr.fail = errors.New("proxmox: no space left")
	if status, _ := s.restore(t, "3f2a9c0d1e4b5a67", false); status != "failed" {
		t.Fatal("the first restore did not fail")
	}
	if s.fr.safeties != 1 {
		t.Fatalf("safety backups after the first failure: %d", s.fr.safeties)
	}
	issueText := func() string {
		iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
		if err != nil {
			t.Fatal(err)
		}
		return string(iss.Changes)
	}
	first := issueText()
	if !strings.Contains(first, "restore the safety backup 5afe000000000001") {
		t.Fatalf("the Issue: %s", first)
	}
	// The second Plan says that the old safety backup stays.
	code, body = s.adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r"})
	if code != 201 || !strings.Contains(string(body), `"to":"kept: the last restore failed; the safety backup from before it stays"`) ||
		!strings.Contains(string(body), "No new safety backup: the last restore failed") {
		t.Fatalf("second Plan: %d %s", code, body)
	}
	status, out := s.restore(t, "3f2a9c0d1e4b5a67", false)
	if status != "failed" || s.fr.safeties != 1 {
		t.Fatalf("second restore: %s, safety backups %d (want still 1)", status, s.fr.safeties)
	}
	if !strings.Contains(out, "the restore failed again") || !strings.Contains(out, "restore the safety backup 5afe000000000001") {
		t.Errorf("second failure text: %s", out)
	}
	if got := issueText(); got != first {
		t.Errorf("the Issue changed: %s, was %s", got, first)
	}
	// The way back works: the good safety backup is restored, with no new
	// safety backup (the guest may be empty); D-429 keeps it and drops
	// only older ones.
	s.fr.fail = nil
	status, out = s.restore(t, "5afe000000000001", false)
	if status != "succeeded" || s.fr.safeties != 1 || len(s.fr.dropped) != 1 || s.fr.dropped[0] != "bk:safety-1" {
		t.Fatalf("restore of the good backup: %s %s, safeties %d, dropped %v", status, out, s.fr.safeties, s.fr.dropped)
	}
	s.reenrollDone(t) // D-440
	if s.openIssue(plan.GuestRestoreFailedID) {
		t.Error("the Issue stays after the re-enroll Run worked")
	}
}

// S3 of T-225: the guest of a Target is gone: a restore is a clear 409,
// never a 500.
func TestRestoreGuestGone(t *testing.T) {
	s := newSafetyEnv(t)
	if err := s.e.drv.Delete(s.e.ctx, driver.GuestID(s.ref)); err != nil {
		t.Fatal(err)
	}
	code, body := s.adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r"})
	if code != 409 || problemType(body) != "not-restorable" || !strings.Contains(string(body), "no restore after the guest is gone") {
		t.Fatalf("restore of a gone guest: %d %s", code, body)
	}
	_ = store.ErrNotFound
}

// M1 of #346: a restore that Proxmox did not begin says so; the guest runs
// again, no Issue opens, and so no later skip of the safety backup.
func TestRestoreNotStartedIsHonest(t *testing.T) {
	s := newSafetyEnv(t)
	s.fr.fail = fmt.Errorf("%w: the backup volume changed since it was checked", plan.ErrRestoreNotStarted)
	status, out := s.restore(t, "3f2a9c0d1e4b5a67", false)
	for _, w := range []string{"the restore did not start: the backup volume changed since it was checked.",
		"The guest's disks are as before; nothing was restored.", "The guest runs again.", "The agent credential is revoked."} {
		if !strings.Contains(out, w) {
			t.Errorf("output without %q: %s", w, out)
		}
	}
	if status != "failed" || strings.Contains(out, "may have no disk") || s.openIssue(plan.GuestRestoreFailedID) {
		t.Fatalf("%s %s, Issue %v", status, out, s.openIssue(plan.GuestRestoreFailedID))
	}
	if st, _ := s.e.drv.Observe(s.e.ctx, driver.GuestID(s.ref)); st.Power != driver.PowerRunning {
		t.Errorf("guest power %s", st.Power)
	}
	code, body := s.adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r", "skip_safety_backup": true})
	if code != 422 {
		t.Errorf("skip after a restore that did not start: %d %s", code, body)
	}
}

// The Run checks "broken" again: a Plan made with the skip for a guest
// without a disk refuses when the guest has its disk back at the Run.
func TestRestoreSkipCheckedAtRun(t *testing.T) {
	s := newSafetyEnv(t)
	id := driver.GuestID(s.ref)
	if err := s.e.drv.Tamper(id, func(g *driver.GuestSpec) { g.DiskGB = 0 }); err != nil {
		t.Fatal(err)
	}
	code, body := s.adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r", "skip_safety_backup": true})
	if code != 201 || !strings.Contains(string(body), "skipped: the guest is broken already") {
		t.Fatalf("skip on a guest without a disk: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	s.e.drv.Tamper(id, func(g *driver.GuestSpec) { g.DiskGB = 10 })
	if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	s.e.eng.Wait()
	r, _ := s.e.st.RunOfPlan(s.e.ctx, p.ID)
	steps, _ := s.e.st.Steps(s.e.ctx, r.ID)
	if r.Status != "failed" || !strings.HasPrefix(steps[0].Output, "nothing was changed: ") || !strings.Contains(steps[0].Output, "only for a broken guest") {
		t.Fatalf("%s: %+v", r.Status, steps)
	}
	if len(s.fr.restored) != 0 || s.fr.safeties != 0 {
		t.Errorf("restored %v, safety backups %d", s.fr.restored, s.fr.safeties)
	}
}

// D1 at the Run: a Plan made while the guest was healthy (safety backup
// "first"), approved after another restore failed: the Run takes no backup
// of the guest that may be empty now and keeps the Issue's good backup.
func TestRestoreOlderPlanAfterAFailure(t *testing.T) {
	s := newSafetyEnv(t)
	propose := func() int64 {
		code, body := s.adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r"})
		if code != 201 {
			t.Fatalf("propose: %d %s", code, body)
		}
		var p struct{ ID int64 }
		json.Unmarshal(body, &p)
		return p.ID
	}
	first, second := propose(), propose()
	s.fr.fail = errors.New("proxmox: no space left")
	for _, id := range []int64{first, second} {
		if code, body := s.adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
			t.Fatalf("approve %d: %d %s", id, code, body)
		}
		s.e.eng.Wait()
	}
	if s.fr.safeties != 1 {
		t.Fatalf("safety backups: %d, want 1 (none of the guest after the first failure)", s.fr.safeties)
	}
	r, _ := s.e.st.RunOfPlan(s.e.ctx, second)
	steps, _ := s.e.st.Steps(s.e.ctx, r.ID)
	if !strings.Contains(steps[0].Output, "restore the safety backup 5afe000000000001") {
		t.Errorf("second Run: %s", steps[0].Output)
	}
}
