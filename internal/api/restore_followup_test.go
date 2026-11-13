package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
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
