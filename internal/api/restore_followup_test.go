package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// D-321: an admin gives a locked user a new one-time password. Humans
// only; the user must change it at the first login.
func TestResetUserPassword(t *testing.T) {
	mode := &plan.RestoreMode{}
	e := newEnv(t, true, func(c *plan.Config) { c.Restore = mode })
	e.admin()
	if _, err := e.st.CreateUser(e.ctx, store.User{Name: "bob", PasswordHash: HashPassword("bob-password-123", fastPasswords)}); err != nil {
		t.Fatal(err)
	}
	bobOld := e.login("bob", "bob-password-123")
	if bobOld == nil {
		t.Fatal("bob login before the restore")
	}
	if err := e.st.Restore(e.ctx, time.Now(), json.RawMessage(`{}`), HashPassword("restored-password-1", fastPasswords)); err != nil {
		t.Fatal(err)
	}
	if err := mode.Load(e.ctx, e.st); err != nil || !mode.Active() {
		t.Fatalf("mode: %v", err)
	}
	rc := &recorder{t: t}
	adm := e.login("admin", "restored-password-1")
	const path = "/users/{name}/password"
	// A session that must change its password first cannot reset others.
	if code, body := rc.do(adm, "POST", "/api/v1/users/bob/password", path, nil); code != 403 || problemType(body) != "password-change-required" {
		t.Errorf("reset before own change: %d %s", code, body)
	}
	adm.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "restored-password-1", "new_password": "another-good-password"})
	if code, _ := rc.do(e.token(TierApply, wire.RiskDisruptive), "POST", "/api/v1/users/bob/password", path, nil); code != 403 {
		t.Errorf("token reset: %d", code)
	}
	if code, body := rc.do(adm, "POST", "/api/v1/users/admin/password", path, nil); code != 409 || problemType(body) != "own-user" {
		t.Errorf("own reset: %d %s", code, body)
	}
	if code, _ := rc.do(adm, "POST", "/api/v1/users/nobody/password", path, nil); code != 404 {
		t.Errorf("unknown user: %d", code)
	}
	if e.login("bob", "bob-password-123") != nil {
		t.Fatal("locked bob logs in")
	}
	lockedUsers := func() []string {
		t.Helper()
		_, body := rc.do(adm, "GET", "/api/v1/restore", "/restore", nil)
		var v struct {
			LockedUsers []string `json:"locked_users"`
		}
		json.Unmarshal(body, &v)
		return v.LockedUsers
	}
	if got := lockedUsers(); strings.Join(got, ",") != "bob" {
		t.Errorf("locked_users before the reset: %v", got)
	}
	// In restored mode too.
	code, body := rc.do(adm, "POST", "/api/v1/users/bob/password", path, nil)
	var otp struct{ Username, Password string }
	json.Unmarshal(body, &otp)
	if code != 200 || otp.Username != "bob" || len(otp.Password) < 20 {
		t.Fatalf("reset: %d %s", code, body)
	}
	if e.login("bob", "bob-password-123") != nil {
		t.Error("the old password of bob works")
	}
	if got := lockedUsers(); got == nil || len(got) != 0 {
		t.Errorf("locked_users after the reset: %v", got)
	}
	bob := e.login("bob", otp.Password)
	if bob == nil {
		t.Fatal("bob cannot log in with the one-time password")
	}
	if _, body := bob.do("GET", "/api/v1/session", nil); !strings.Contains(string(body), `"must_change_password":true`) {
		t.Errorf("session: %s", body)
	}
	if code, body := bob.do("GET", "/api/v1/targets", nil); code != 403 || problemType(body) != "password-change-required" {
		t.Errorf("bob before the change: %d %s", code, body)
	}
	if code, _ := bob.do("PUT", "/api/v1/session/password", map[string]string{"current_password": otp.Password, "new_password": "bobs-own-new-password"}); code != 204 {
		t.Fatalf("bob change: %d", code)
	}
	if code, _ := bob.do("GET", "/api/v1/targets", nil); code != 200 {
		t.Errorf("bob after the change: %d", code)
	}
	// A second reset ends the sessions of bob.
	if rec := adm.raw("POST", "/api/v1/users/bob/password", nil); rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("second reset: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if code, _ := bob.do("GET", "/api/v1/session", nil); code != 401 {
		t.Errorf("bob session after a reset: %d", code)
	}
	evs, _ := e.st.EventsAfter(e.ctx, 0, 1000)
	n := 0
	for _, ev := range evs {
		if ev.Type == "user.password_reset" {
			n++
			if ev.Subject != "user:bob" || ev.Actor != "user:admin" || strings.Contains(string(ev.Data), otp.Password) {
				t.Errorf("event: %+v", ev)
			}
		}
	}
	if n != 2 {
		t.Errorf("%d user.password_reset events", n)
	}
	rc.check()
}

// renameAndDelete renames the guest of web1 in Proxmox, runs a guest
// poll, then deletes web1.
func renameAndDelete(t *testing.T, e *env, adm *client) driver.GuestID {
	t.Helper()
	e.eng.CheckGuests(e.ctx) // a first good check
	b1, err := e.st.GetBinding(e.ctx, "Target", "web1")
	if err != nil {
		t.Fatal(err)
	}
	id := driver.GuestID(b1.Ref)
	if err := e.drv.Tamper(id, func(s *driver.GuestSpec) { s.Name = "web1-renamed" }); err != nil {
		t.Fatal(err)
	}
	e.eng.CheckGuests(e.ctx)
	if _, err := e.st.OpenIssue(e.ctx, "web1", plan.GuestMissingID); err == nil {
		t.Error("guest:missing for a rename")
	}
	if _, err := e.st.AgentByTarget(e.ctx, "web1"); err != nil {
		t.Errorf("the agent of web1 was revoked: %v", err)
	}
	e.runPlanAnyStatus(adm, map[string]any{"reason": "remove web1", "delete": []map[string]string{{"kind": "Target", "name": "web1"}}})
	return id
}

// D-322: once restored mode is over and no guest:missing is open, the
// delete of a Target whose guest a person renamed is the same on a
// restored and on a never restored controller.
func TestRestoreRenameAfterConfirm(t *testing.T) {
	type result struct{ guestGone, bindingGone bool }
	run := func(restored bool) result {
		var e *env
		var adm *client
		if restored {
			e, _, _, _ = restoredEnv(t)
			adm = e.login("admin", "restored-password-1")
			adm.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "restored-password-1", "new_password": "another-good-password"})
			if code, body := adm.do("POST", "/api/v1/restore/confirm", map[string]any{"old_controller_off": true}); code != 200 {
				t.Fatalf("confirm: %d %s", code, body)
			}
		} else {
			e = newEnv(t, true)
			adm = e.admin()
			e.seed(adm, "web1")
			e.seed(adm, "web2")
		}
		id := renameAndDelete(t, e, adm)
		_, err := e.drv.Observe(e.ctx, id)
		_, berr := e.st.GetBinding(e.ctx, "Target", "web1")
		return result{guestGone: err != nil, bindingGone: berr != nil}
	}
	never, restored := run(false), run(true)
	if never != restored {
		t.Errorf("never restored %+v, restored %+v", never, restored)
	}
	if !restored.guestGone || !restored.bindingGone {
		t.Errorf("delete of a renamed guest: %+v", restored)
	}
}

// D-322: ignore_once on guest:missing of a foreign guest keeps that guest
// in the unknown list, also after the confirm; no Run writes to it.
func TestRestoreIgnoreOnceForeign(t *testing.T) {
	e, _, _, clk := restoredEnv(t)
	rc := &recorder{t: t}
	b1, _ := e.st.GetBinding(e.ctx, "Target", "web1")
	b2, _ := e.st.GetBinding(e.ctx, "Target", "web2")
	if err := e.drv.Delete(e.ctx, driver.GuestID(b2.Ref)); err != nil {
		t.Fatal(err)
	}
	b2.Ref = b1.Ref // the VMID of web2 now holds a guest of another name
	if err := e.st.PutBinding(e.ctx, b2); err != nil {
		t.Fatal(err)
	}
	e.eng.CheckGuests(e.ctx)
	iss, err := e.st.OpenIssue(e.ctx, "web2", plan.GuestMissingID)
	if err != nil {
		t.Fatalf("no guest:missing: %v", err)
	}
	adm := e.login("admin", "restored-password-1")
	adm.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "restored-password-1", "new_password": "another-good-password"})
	unknown := func(when string) {
		t.Helper()
		clk.add(2 * time.Minute) // past the cache of the list
		_, body := rc.do(adm, "GET", "/api/v1/restore", "/restore", nil)
		if !strings.Contains(string(body), `"guest_id":"`+b1.Ref+`"`) {
			t.Errorf("%s: the foreign guest is not in unknown_guests: %s", when, body)
		}
	}
	unknown("open Issue")
	if code, body := rc.do(adm, "POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", "/issues/{id}/resolve", resolveBody(issue.ActionIgnoreOnce)); code != 200 {
		t.Fatalf("ignore once: %d %s", code, body)
	}
	e.eng.CheckGuests(e.ctx)
	unknown("ignored, restored mode")
	if code, body := adm.do("POST", "/api/v1/restore/confirm", map[string]any{"old_controller_off": true}); code != 200 {
		t.Fatalf("confirm: %d %s", code, body)
	}
	for i := range 2 {
		e.eng.CheckGuests(e.ctx)
		unknown("ignored, after the confirm, poll " + itoa(int64(i+1)))
	}
	if _, err := e.st.OpenIssue(e.ctx, "web2", plan.GuestMissingID); err == nil {
		t.Error("the ignored guest:missing opened again")
	}
	if _, err := e.st.IgnoredOnce(e.ctx, "web2", plan.GuestMissingID); err != nil {
		t.Errorf("the ignore ended: %v", err)
	}
	e.runPlanAnyStatus(adm, map[string]any{"reason": "remove web2", "delete": []map[string]string{{"kind": "Target", "name": "web2"}}})
	if _, err := e.drv.Observe(e.ctx, driver.GuestID(b1.Ref)); err != nil {
		t.Fatalf("the delete of web2 removed the foreign guest: %v", err)
	}
	rc.check()
}

// Review H1 of #224: restored mode ends before one good poll of a
// binding whose VMID holds a guest of another name. The confirm checks
// every guest first: an Observe error refuses it, a name mismatch opens
// guest:missing, so no delete reaches the foreign guest afterwards.
func TestRestoreConfirmChecksGuests(t *testing.T) {
	e, _, mode, _ := restoredEnv(t)
	rc := &recorder{t: t}
	b1, _ := e.st.GetBinding(e.ctx, "Target", "web1")
	b2, _ := e.st.GetBinding(e.ctx, "Target", "web2")
	b1.Ref = b2.Ref // the VMID of web1 holds the guest named web2
	if err := e.st.PutBinding(e.ctx, b1); err != nil {
		t.Fatal(err)
	}
	adm := e.login("admin", "restored-password-1")
	adm.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "restored-password-1", "new_password": "another-good-password"})
	e.drv.Fail(fake.OpObserve, errors.New("proxmox: 500"))
	if code, body := rc.do(adm, "POST", "/api/v1/restore/confirm", "/restore/confirm", map[string]any{"old_controller_off": true}); code != 409 || problemType(body) != "restore-check-failed" {
		t.Fatalf("confirm while Observe fails: %d %s", code, body)
	}
	if !mode.Active() {
		t.Fatal("a failed check ended restored mode")
	}
	if code, body := rc.do(adm, "POST", "/api/v1/restore/confirm", "/restore/confirm", map[string]any{"old_controller_off": true}); code != 200 {
		t.Fatalf("confirm: %d %s", code, body)
	}
	if _, err := e.st.OpenIssue(e.ctx, "web1", plan.GuestMissingID); err != nil {
		t.Fatalf("no guest:missing after the confirm check: %v", err)
	}
	e.eng.CheckGuests(e.ctx)
	e.runPlanAnyStatus(adm, map[string]any{"reason": "remove web1", "delete": []map[string]string{{"kind": "Target", "name": "web1"}}})
	if _, err := e.drv.Observe(e.ctx, driver.GuestID(b2.Ref)); err != nil {
		t.Fatalf("the delete of web1 removed the guest named web2: %v", err)
	}
	rc.check()
}

// Review round 2 of #224: the foreign mark is no Issue state. An
// ignore_rule closes guest:missing, then one Plan deletes the Rule and the
// Target: the binding goes, the agent is revoked, the guest stays.
func TestRestoreForeignMarkSurvivesRuleDelete(t *testing.T) {
	e, _, _, _ := restoredEnv(t)
	rc := &recorder{t: t}
	b1, _ := e.st.GetBinding(e.ctx, "Target", "web1")
	b2, _ := e.st.GetBinding(e.ctx, "Target", "web2")
	if err := e.drv.Delete(e.ctx, driver.GuestID(b1.Ref)); err != nil {
		t.Fatal(err)
	}
	b1.Ref = b2.Ref // the VMID of web1 holds the guest named web2
	if err := e.st.PutBinding(e.ctx, b1); err != nil {
		t.Fatal(err)
	}
	adm := e.login("admin", "restored-password-1")
	adm.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "restored-password-1", "new_password": "another-good-password"})
	if code, body := adm.do("POST", "/api/v1/restore/confirm", map[string]any{"old_controller_off": true}); code != 200 {
		t.Fatalf("confirm: %d %s", code, body)
	}
	iss, err := e.st.OpenIssue(e.ctx, "web1", plan.GuestMissingID)
	if err != nil {
		t.Fatalf("no guest:missing: %v", err)
	}
	code, body := rc.do(adm, "POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", "/issues/{id}/resolve", resolveBody(issue.ActionIgnoreRule))
	var pl struct{ ID int64 }
	json.Unmarshal(body, &pl)
	if code != 201 {
		t.Fatalf("ignore rule: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(pl.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve rule: %d %s", code, body)
	}
	e.eng.Wait()
	if _, err := e.st.OpenIssue(e.ctx, "web1", plan.GuestMissingID); err == nil {
		t.Fatal("guest:missing still open after the rule")
	}
	e.runPlanAnyStatus(adm, map[string]any{"reason": "clean up", "delete": []map[string]string{
		{"kind": "Rule", "name": issue.RuleName(iss.ID)}, {"kind": "Target", "name": "web1"}}})
	if _, err := e.drv.Observe(e.ctx, driver.GuestID(b2.Ref)); err != nil {
		t.Fatalf("the delete of web1 removed the guest named web2: %v", err)
	}
	if _, err := e.st.GetBinding(e.ctx, "Target", "web1"); err == nil {
		t.Error("the binding of web1 stays after its delete")
	}
	if _, err := e.st.AgentByTarget(e.ctx, "web1"); err == nil {
		t.Error("the agent of web1 was not revoked")
	}
	// The guest is an unknown guest now; Plumb never touches it.
	e.eng.CheckGuests(e.ctx)
	if ok, _ := e.st.IsForeign(e.ctx, "web1", b2.Ref); ok {
		t.Error("the foreign mark outlives the binding")
	}
	rc.check()
}

// The backup run that wrote the file was running in the snapshot: after
// the restore it is ok, not "interrupted" (T-126, from T-118).
func TestRestoreBackupRunOK(t *testing.T) {
	e := newEnv(t, true)
	run, err := e.st.StartBackupRun(e.ctx, "system", store.BackupSchedule, "plumb-20261113-0300.pbk")
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if err := e.st.Restore(e.ctx, created, json.RawMessage(`{}`), HashPassword("restored-password-1", fastPasswords)); err != nil {
		t.Fatal(err)
	}
	if err := e.st.FailRunningBackups(e.ctx, "interrupted"); err != nil { // the start of serve
		t.Fatal(err)
	}
	last, err := e.st.LastBackupRun(e.ctx, "")
	if err != nil || last.ID != run.ID || last.Result != store.BackupOK || !last.FinishedAt.Equal(created) || !strings.Contains(last.Message, "restored from this backup") {
		t.Errorf("last backup run: %+v %v", last, err)
	}
}

// T-134 F8: a delete of the Target resolves its guest:missing as gone
// with the reason, also for a foreign guest; the foreign guest stays.
func TestRestoreTargetDeleteResolvesGuestMissing(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "gone", true: "foreign"}[foreign], func(t *testing.T) {
			e, _, _, _ := restoredEnv(t)
			b1, _ := e.st.GetBinding(e.ctx, "Target", "web1")
			b2, _ := e.st.GetBinding(e.ctx, "Target", "web2")
			if err := e.drv.Delete(e.ctx, driver.GuestID(b2.Ref)); err != nil {
				t.Fatal(err)
			}
			if foreign {
				b2.Ref = b1.Ref // the VMID of web2 holds the guest named web1
				if err := e.st.PutBinding(e.ctx, b2); err != nil {
					t.Fatal(err)
				}
			}
			adm := e.login("admin", "restored-password-1")
			adm.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "restored-password-1", "new_password": "another-good-password"})
			if code, body := adm.do("POST", "/api/v1/restore/confirm", map[string]any{"old_controller_off": true}); code != 200 {
				t.Fatalf("confirm: %d %s", code, body)
			}
			iss, err := e.st.OpenIssue(e.ctx, "web2", plan.GuestMissingID)
			if err != nil {
				t.Fatalf("no guest:missing: %v", err)
			}
			e.runPlanAnyStatus(adm, map[string]any{"reason": "remove web2", "delete": []map[string]string{{"kind": "Target", "name": "web2"}}})
			if _, err := e.st.GetBinding(e.ctx, "Target", "web2"); err == nil {
				t.Fatal("the binding of web2 stays after its delete")
			}
			got, err := e.st.GetIssue(e.ctx, iss.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != store.IssueResolved || got.Resolution != "gone" || got.ResolvedBy != "system" {
				t.Errorf("guest:missing after the delete: %s %s by %s", got.Status, got.Resolution, got.ResolvedBy)
			}
			cs, err := e.st.Comments(e.ctx, iss.ID)
			if err != nil || len(cs) != 1 || cs[0].Author != "system" || cs[0].Body != "Resolved: the Target was deleted." {
				t.Errorf("comments: %+v %v", cs, err)
			}
			code, body := adm.do("GET", "/api/v1/issues/"+itoa(iss.ID), nil)
			if code != 200 || !strings.Contains(string(body), "the Target was deleted") {
				t.Errorf("GET issue: %d %s", code, body)
			}
			if foreign {
				if _, err := e.drv.Observe(e.ctx, driver.GuestID(b1.Ref)); err != nil {
					t.Fatalf("the delete of web2 removed the foreign guest: %v", err)
				}
			}
			e.eng.CheckGuests(e.ctx)
			if _, err := e.st.OpenIssue(e.ctx, "web2", plan.GuestMissingID); err == nil {
				t.Error("guest:missing opened again after the delete")
			}
			if _, err := e.st.OpenIssue(e.ctx, "web1", plan.GuestMissingID); err == nil {
				t.Error("guest:missing of web1 opened")
			}
		})
	}
}
