package api

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// testClock is a settable clock for the engine.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// restoredEnv seeds web1 and web2, then marks the database restored, as
// plumb restore does, and loads restored mode.
func restoredEnv(t *testing.T) (*env, *client, *plan.RestoreMode, *testClock) {
	t.Helper()
	mode := &plan.RestoreMode{}
	clk := &testClock{t: time.Now().UTC()}
	e := newEnv(t, true, func(c *plan.Config) { c.Restore = mode; c.Now = clk.now })
	adm := e.admin()
	e.seed(adm, "web1")
	e.seed(adm, "web2")
	if err := e.st.Restore(e.ctx, clk.now().Add(-time.Hour), json.RawMessage(`{"v":1}`), HashPassword("restored-password-1", fastPasswords)); err != nil {
		t.Fatal(err)
	}
	if err := mode.Load(e.ctx, e.st); err != nil || !mode.Active() {
		t.Fatalf("mode: %v", err)
	}
	return e, adm, mode, clk
}

// Restored mode stops every writer (D-303): approve, re-enroll, build,
// backup, schedules, Runs, the driver; Plans can still be made.
func TestRestoredModeStopsWriters(t *testing.T) {
	e, _, mode, _ := restoredEnv(t)
	rc := &recorder{t: t}
	// The restore ended the session of the seed; admin logs in with the
	// new password and must change it.
	adm := e.login("admin", "restored-password-1")
	if adm == nil {
		t.Fatal("admin login with the new password failed")
	}
	if code, body := adm.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "restored-password-1", "new_password": "another-good-password"}); code != 204 {
		t.Fatalf("change password: %d %s", code, body)
	}

	code, body := rc.do(adm, "GET", "/api/v1/restore", "/restore", nil)
	if code != 200 || !strings.Contains(string(body), `"restored":true`) || !strings.Contains(string(body), `"restored_mode":true`) || !strings.Contains(string(body), `"confirmed_at":null`) {
		t.Fatalf("restore state: %d %s", code, body)
	}
	// A Plan can be made; its approval waits.
	code, body = rc.do(adm, "POST", "/api/v1/plans", "/plans", map[string]any{"reason": "change", "put": []any{targetDoc("web1", "changed")}})
	if code != 201 {
		t.Fatalf("plan: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	for _, c := range []struct{ method, path, tmpl string }{
		{"POST", "/api/v1/plans/" + itoa(p.ID) + "/approve", "/plans/{id}/approve"},
		{"POST", "/api/v1/targets/web1/reenroll", "/targets/{name}/reenroll"},
		{"POST", "/api/v1/images/ubuntu/build", "/images/{name}/build"},
		{"POST", "/api/v1/backups", "/backups"},
	} {
		code, body := rc.do(adm, c.method, c.path, c.tmpl, nil)
		if code != 409 || problemType(body) != "restored-mode" {
			t.Errorf("%s %s: %d %s", c.method, c.path, code, body)
		}
	}
	// The engine itself refuses a Run and skips the schedules.
	if _, err := e.eng.Approve(e.ctx, p.ID, "system", nil); err == nil {
		t.Error("engine approve in restored mode")
	}
	if err := e.eng.CheckSchedules(e.ctx); err != nil {
		t.Errorf("schedules: %v", err)
	}
	// The driver guard refuses writes.
	guarded := driver.ReadOnlyWhen(e.drv, mode.Active)
	if err := guarded.Power(e.ctx, "1", driver.PowerStop); err != driver.ErrReadOnly {
		t.Errorf("guard: %v", err)
	}
	rc.check()
}

// Tokens and users after a restore (D-303).
func TestRestoredTokensAndUsers(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	tok := e.token(TierRead, "")
	if code, _ := tok.do("GET", "/api/v1/targets", nil); code != 200 {
		t.Fatal("token before")
	}
	e.st.CreateUser(e.ctx, store.User{Name: "bob", PasswordHash: HashPassword("bob-password-123", fastPasswords)})
	if err := e.st.Restore(e.ctx, time.Now(), json.RawMessage(`{}`), HashPassword("restored-password-1", fastPasswords)); err != nil {
		t.Fatal(err)
	}
	rc := &recorder{t: t}
	if code, _ := tok.do("GET", "/api/v1/targets", nil); code != 401 {
		t.Errorf("disabled token: %d", code)
	}
	if code, _ := adm.do("GET", "/api/v1/targets", nil); code != 401 {
		t.Errorf("old session: %d", code)
	}
	if e.login("admin", e.password) != nil {
		t.Error("the old admin password still works")
	}
	if e.login("bob", "bob-password-123") != nil {
		t.Error("a locked user can log in")
	}
	adm = e.login("admin", "restored-password-1")
	adm.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "restored-password-1", "new_password": "another-good-password"})
	_, body := rc.do(adm, "GET", "/api/v1/tokens", "/tokens", nil)
	if !strings.Contains(string(body), `"disabled_at":"`) {
		t.Errorf("token list: %s", body)
	}
	var list struct {
		Items []struct{ ID string } `json:"items"`
	}
	json.Unmarshal(body, &list)
	id := list.Items[0].ID
	// Only a human session enables a token.
	other := e.token(TierRead, "") // made after the restore: enabled
	if code, body := rc.do(other, "POST", "/api/v1/tokens/"+id+"/enable", "/tokens/{id}/enable", nil); code != 403 {
		t.Errorf("token enables: %d %s", code, body)
	}
	if code, body := rc.do(adm, "POST", "/api/v1/tokens/"+id+"/enable", "/tokens/{id}/enable", nil); code != 200 || !strings.Contains(string(body), `"disabled_at":null`) {
		t.Fatalf("enable: %d %s", code, body)
	}
	if code, _ := tok.do("GET", "/api/v1/targets", nil); code != 200 {
		t.Errorf("enabled token: %d", code)
	}
	if code, body := rc.do(adm, "POST", "/api/v1/tokens/"+id+"/enable", "/tokens/{id}/enable", nil); code != 200 {
		t.Errorf("enable again: %d %s", code, body)
	}
	rc.do(adm, "DELETE", "/api/v1/tokens/"+id, "/tokens/{id}", nil)
	if code, body := rc.do(adm, "POST", "/api/v1/tokens/"+id+"/enable", "/tokens/{id}/enable", nil); code != 409 || problemType(body) != "token-revoked" {
		t.Errorf("enable revoked: %d %s", code, body)
	}
	if code, _ := rc.do(adm, "POST", "/api/v1/tokens/nope/enable", "/tokens/{id}/enable", nil); code != 404 {
		t.Errorf("enable unknown: %d", code)
	}
	rc.check()
}

// The confirm ends restored mode; guest:missing and agent:silent come
// after it (D-303).
func TestRestoreConfirmAndIssues(t *testing.T) {
	e, _, mode, clk := restoredEnv(t)
	rc := &recorder{t: t}
	adm := e.login("admin", "restored-password-1")
	adm.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "restored-password-1", "new_password": "another-good-password"})

	// web2's guest was deleted after the backup.
	b2, err := e.st.GetBinding(e.ctx, "Target", "web2")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.drv.Delete(e.ctx, driver.GuestID(b2.Ref)); err != nil {
		t.Fatal(err)
	}
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Logf("check guests: %v", err)
	}
	iss, err := e.st.OpenIssue(e.ctx, "web2", plan.GuestMissingID)
	if err != nil {
		t.Fatalf("no guest:missing: %v", err)
	}
	_, body := rc.do(adm, "GET", "/api/v1/issues/"+itoa(iss.ID), "/issues/{id}", nil)
	if !strings.Contains(string(body), `"actions":["ignore_once","ignore_rule"]`) || !strings.Contains(string(body), `"risk":"destructive"`) {
		t.Errorf("guest:missing: %s", body)
	}
	if code, body := rc.do(adm, "POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", "/issues/{id}/resolve", resolveBody("revert")); code != 409 {
		t.Errorf("revert guest:missing: %d %s", code, body)
	}
	if _, err := e.st.AgentByTarget(e.ctx, "web2"); err == nil {
		t.Error("the agent of the missing guest was not revoked")
	}

	// Confirm: a token gets 403, a bad body 400, the session 200.
	tok := e.token(TierApply, wire.RiskReversible)
	if code, _ := rc.do(tok, "POST", "/api/v1/restore/confirm", "/restore/confirm", map[string]any{"old_controller_off": true}); code != 403 {
		t.Errorf("token confirm: %d", code)
	}
	if code, _ := rc.do(adm, "POST", "/api/v1/restore/confirm", "/restore/confirm", map[string]any{"old_controller_off": false}); code != 400 {
		t.Errorf("false confirm: %d", code)
	}
	code, body := rc.do(adm, "POST", "/api/v1/restore/confirm", "/restore/confirm", map[string]any{"old_controller_off": true, "reason": "old LXC destroyed"})
	if code != 200 || !strings.Contains(string(body), `"restored_mode":false`) || !strings.Contains(string(body), `"confirmed_by":"user:admin"`) {
		t.Fatalf("confirm: %d %s", code, body)
	}
	if mode.Active() {
		t.Fatal("still in restored mode")
	}
	if code, body := rc.do(adm, "POST", "/api/v1/restore/confirm", "/restore/confirm", map[string]any{"old_controller_off": true}); code != 409 || problemType(body) != "nothing-to-do" {
		t.Errorf("second confirm: %d %s", code, body)
	}
	// Writers run again.
	if code, body := rc.do(adm, "POST", "/api/v1/backups", "/backups", nil); code != 202 {
		t.Errorf("backup after confirm: %d %s", code, body)
	}
	e.backups.Wait()

	// agent:silent: web1 runs, its agent sent no hello since the confirm.
	clk.add(plan.SilentAfter + time.Minute)
	e.eng.CheckGuests(e.ctx)
	silent, err := e.st.OpenIssue(e.ctx, "web1", plan.AgentSilentID)
	if err != nil {
		t.Fatalf("no agent:silent: %v", err)
	}
	_, body = rc.do(adm, "GET", "/api/v1/issues/"+itoa(silent.ID), "/issues/{id}", nil)
	if !strings.Contains(string(body), `"actions":["ignore_once","ignore_rule"]`) {
		t.Errorf("agent:silent: %s", body)
	}
	// A hello ends it.
	if err := e.eng.HandleReport(e.ctx, helloDelivery("web1")); err != nil {
		t.Fatal(err)
	}
	if iss, err := e.st.GetIssue(e.ctx, silent.ID); err != nil || iss.Status != store.IssueResolved {
		t.Errorf("after a hello: %+v %v", iss, err)
	}
	rc.check()
}

// helloDelivery is a hello of the agent of target, as link delivers it.
func helloDelivery(target string) link.Delivery {
	return link.Delivery{Target: target, Report: wire.Report{Type: wire.ReportHello,
		Hello: &wire.Hello{Version: "test", Hostname: target, OS: "debian 13"}}}
}

// A Run of the backup that was running fails with "interrupted by a
// restore", without a command (D-303).
func TestRestoreRecoverRuns(t *testing.T) {
	e, _, _, _ := restoredEnv(t)
	head, _ := e.st.Head(e.ctx)
	docs, _ := e.st.RevisionDocs(e.ctx, head.ID)
	p, err := e.st.CreatePlan(e.ctx, store.Plan{BaseRevision: head.ID, Origin: "proposal", Risk: "read", Author: "user:admin"})
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := e.st.ApprovePlan(e.ctx, p.ID, "user:admin", store.NewRevision{Author: "user:admin", Docs: docs})
	if err != nil {
		t.Fatal(err)
	}
	e.st.AddStep(e.ctx, store.Step{RunID: run.ID, Target: "web1", Resource: "file:/etc/motd", Status: "pending"})
	if err := e.eng.Recover(e.ctx); err != nil {
		t.Fatal(err)
	}
	steps, _ := e.st.Steps(e.ctx, run.ID)
	if len(steps) != 1 || steps[0].Status != "failed" || steps[0].Output != "interrupted by a restore" {
		t.Errorf("steps: %+v", steps)
	}
	if r, _ := e.st.GetRun(e.ctx, run.ID); r.Status != "failed" {
		t.Errorf("run: %+v", r)
	}
}
