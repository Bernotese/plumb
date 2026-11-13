package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// newUser makes a user through the API and logs it in with a changed
// password. It returns the session client.
func (e *env) newUser(adm *client, name, role string) *client {
	e.t.Helper()
	code, body := adm.do("POST", "/api/v1/users", map[string]string{"name": name, "role": role})
	if code != 201 {
		e.t.Fatalf("create %s: %d %s", name, code, body)
	}
	var otp oneTimePassword
	json.Unmarshal(body, &otp)
	if otp.Username != name || len(otp.Password) < 12 {
		e.t.Fatalf("one-time password: %s", body)
	}
	c := e.login(name, otp.Password)
	if c == nil {
		e.t.Fatalf("login %s", name)
	}
	if code, body := c.do("PUT", "/api/v1/session/password",
		map[string]string{"current_password": otp.Password, "new_password": name + "-long-password"}); code != 204 {
		e.t.Fatalf("change password %s: %d %s", name, code, body)
	}
	return c
}

func (e *env) userView(adm *client, name string) userView {
	e.t.Helper()
	code, body := adm.do("GET", "/api/v1/users?limit=200", nil)
	if code != 200 {
		e.t.Fatalf("list users: %d %s", code, body)
	}
	var p struct{ Items []userView }
	json.Unmarshal(body, &p)
	for _, u := range p.Items {
		if u.Name == name {
			return u
		}
	}
	e.t.Fatalf("no user %s in %s", name, body)
	return userView{}
}

// Roles on the operations (D-329, D-342): every endpoint of the tier
// matrix with a session of each role. Session-only operations need admin,
// except the own session.
func TestRoleMatrix(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	sessions := map[string]*client{TierAdmin: adm}
	for _, role := range []string{TierRead, TierPlan, TierApply} {
		sessions[role] = e.newUser(adm, "u"+role, role)
	}
	own := map[string]bool{"PUT /api/v1/session/password": true, "DELETE /api/v1/session": true}
	extra := []endpoint{
		{"GET", "/api/v1/users", nil, TierAdmin, true},
		{"POST", "/api/v1/users", map[string]string{"name": "", "role": "x"}, TierAdmin, true},  // allowed → 422
		{"PATCH", "/api/v1/users/nobody", map[string]string{"role": "read"}, TierAdmin, true}, // allowed → 404
	}
	for _, ep := range append(append([]endpoint{}, endpoints...), extra...) {
		key := ep.method + " " + strings.SplitN(ep.path, "?", 2)[0]
		if key == "DELETE /api/v1/session" {
			continue // logs the session out; TestOwnSessionForEveryRole covers it
		}
		minRole := ep.tier
		if own[key] {
			minRole = TierRead
		}
		for role, c := range sessions {
			code, body := c.do(ep.method, ep.path, ep.body)
			name := role + " session " + key
			if tierLevel[role] < tierLevel[minRole] {
				if code != 403 || problemType(body) != "forbidden" {
					t.Errorf("%s: %d %s, want 403 forbidden", name, code, body)
				}
				continue
			}
			if code == 401 || code == 403 || code >= 500 {
				t.Errorf("%s: %d %s, want access", name, code, body)
			}
		}
	}
	// No token reaches user management, whatever its tier (risks.md §1
	// worst case 2).
	for _, tok := range []*client{e.token(TierRead, ""), e.token(TierPlan, ""), e.token(TierApply, wire.RiskDisruptive)} {
		for _, ep := range extra {
			if code, body := tok.do(ep.method, ep.path, ep.body); code != 403 || problemType(body) != "human-required" {
				t.Errorf("token %s %s: %d %s", ep.method, ep.path, code, body)
			}
		}
	}
	e.noSecrets()
}

func TestOwnSessionForEveryRole(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	c := e.newUser(adm, "viewer", TierRead)
	code, body := c.do("GET", "/api/v1/session", nil)
	if code != 200 || !strings.Contains(string(body), `"tier":"read"`) {
		t.Fatalf("session view: %d %s", code, body)
	}
	if code, body := c.do("DELETE", "/api/v1/session", nil); code != 204 {
		t.Fatalf("logout: %d %s", code, body)
	}
	if code, _ := c.do("GET", "/api/v1/session", nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
}

// Approve and ignore once go by role inside the operation (review M2 of
// #241): apply up to disruptive, also human-only Plans; destructive only
// admin.
func TestApproveAndIgnoreByRole(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	ops := e.newUser(adm, "ops", TierApply)
	planner := e.newUser(adm, "planner", TierPlan)
	approve := func(c *client, id int64) (int, string) {
		code, b := c.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil)
		return code, problemType(b)
	}

	// A human-only Plan (rule change) of low risk: role apply approves it.
	p, _ := e.plan(planner, map[string]any{"reason": "rule", "put": []state.Doc{
		doc(state.KindRule, "quiet", `{"action":"ignore","target":"*","resource":"file:/etc/motd","attrs":["content"]}`)}}, 201)
	if len(p.HumanOnly) == 0 {
		t.Fatalf("rule plan not human-only: %+v", p)
	}
	if code, typ := approve(planner, p.ID); code != 403 || typ != "forbidden" {
		t.Errorf("plan role approves: %d %s", code, typ)
	}
	if code, typ := approve(ops, p.ID); code != 202 {
		t.Errorf("apply role, human-only plan: %d %s", code, typ)
	}
	e.eng.Wait()
	// Take the rule away again (also human-only) so drift opens Issues.
	p, _ = e.plan(adm, map[string]any{"reason": "unrule", "delete": []map[string]string{{"kind": "Rule", "name": "quiet"}}}, 201)
	if code, typ := approve(ops, p.ID); code != 202 {
		t.Errorf("apply role, rule delete: %d %s", code, typ)
	}
	e.eng.Wait()

	// Ignore once: role plan cannot, role apply can (reversible drift).
	e.motd("changed by hand")
	deadline := time.Now().Add(5 * time.Second)
	for e.openIssueID() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no issue")
		}
		time.Sleep(10 * time.Millisecond)
	}
	id := e.openIssueID()
	if code, b := planner.do("POST", "/api/v1/issues/"+itoa(id)+"/resolve", resolveBody("ignore_once")); code != 403 || problemType(b) != "forbidden" {
		t.Errorf("plan role ignores: %d %s", code, b)
	}
	if code, b := ops.do("POST", "/api/v1/issues/"+itoa(id)+"/resolve", resolveBody("ignore_once")); code != 200 {
		t.Errorf("apply role ignores: %d %s", code, b)
	}

	// Destructive (delete the Target): apply role refused, admin approves.
	p, _ = e.plan(planner, map[string]any{"reason": "remove", "delete": []map[string]string{{"kind": "Target", "name": "web1"}}}, 201)
	if p.Risk != "destructive" {
		t.Fatalf("risk %s", p.Risk)
	}
	if code, typ := approve(ops, p.ID); code != 403 || typ != "risk-too-high" {
		t.Errorf("apply role approves destructive: %d %s", code, typ)
	}
	if code, typ := approve(adm, p.ID); code != 202 {
		t.Errorf("admin approves destructive: %d %s", code, typ)
	}
	e.eng.Wait()
}

// User life cycle (D-330, risks.md §1): self-raise, own user, last admin,
// sessions and tokens after a role change or disable, name reuse.
func TestUserLifeCycle(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()

	// Validation and name reuse.
	for _, body := range []map[string]string{{"name": "Bad", "role": "read"}, {"name": "ok", "role": "root"}, {"name": "-x", "role": "read"}} {
		if code, b := adm.do("POST", "/api/v1/users", body); code != 422 {
			t.Errorf("create %v: %d %s", body, code, b)
		}
	}
	alice := e.newUser(adm, "alice", TierApply)
	if code, b := adm.do("POST", "/api/v1/users", map[string]string{"name": "alice", "role": "read"}); code != 409 || problemType(b) != "user-exists" {
		t.Errorf("duplicate: %d %s", code, b)
	}
	if v := e.userView(adm, "alice"); v.Role != TierApply || v.CreatedBy != "user:admin" || v.MustChangePassword || v.LastLoginAt == nil {
		t.Errorf("alice view: %+v", v)
	}

	// Self-raise: alice is not admin, so she cannot reach user management.
	if code, b := alice.do("PATCH", "/api/v1/users/alice", map[string]string{"role": "admin"}); code != 403 || problemType(b) != "forbidden" {
		t.Errorf("self-raise: %d %s", code, b)
	}
	if code, b := alice.do("POST", "/api/v1/tokens", map[string]string{"name": "x", "tier": "apply", "max_risk": "disruptive"}); code != 403 {
		t.Errorf("non-admin makes a token: %d %s", code, b)
	}
	// Own user: 409 also for an admin.
	if code, b := adm.do("PATCH", "/api/v1/users/admin", map[string]string{"role": "read"}); code != 409 || problemType(b) != "own-user" {
		t.Errorf("own user: %d %s", code, b)
	}

	// A second admin with a token; the last-admin rule.
	boss := e.newUser(adm, "boss", TierAdmin)
	b, _, err := CreateToken(e.ctx, e.st, "boss-token", TierPlan, "", "user:boss")
	if err != nil {
		t.Fatal(err)
	}
	_, sec, _ := strings.Cut(b, ".")
	e.bearers = append(e.bearers, sec)
	bossTok := &client{e: e, bearer: b}
	if code, _ := bossTok.do("GET", "/api/v1/targets", nil); code != 200 {
		t.Fatalf("boss token: %d", code)
	}
	if code, body := boss.do("PATCH", "/api/v1/users/admin", map[string]bool{"disabled": true}); code != 200 {
		t.Fatalf("boss disables admin: %d %s", code, body)
	}
	if code, _ := adm.do("GET", "/api/v1/targets", nil); code != 401 {
		t.Errorf("session of a disabled user: %d", code)
	}
	if e.login("admin", e.password) != nil {
		t.Error("a disabled user logs in")
	}
	// boss is now the last enabled admin; nobody can demote boss, and boss
	// cannot demote itself (own-user).
	adm2 := e.newUser(boss, "carol", TierApply)
	_ = adm2
	if code, body := boss.do("PATCH", "/api/v1/users/boss", map[string]string{"role": "apply"}); code != 409 || problemType(body) != "own-user" {
		t.Errorf("boss demotes itself: %d %s", code, body)
	}
	if code, body := boss.do("PATCH", "/api/v1/users/admin", map[string]bool{"disabled": false}); code != 200 {
		t.Fatalf("enable admin: %d %s", code, body)
	}
	adm = e.login("admin", e.password)
	if adm == nil {
		t.Fatal("admin login after enable")
	}
	// Demote boss: its session ends and its token is disabled.
	if code, body := adm.do("PATCH", "/api/v1/users/boss", map[string]string{"role": "plan"}); code != 200 || !strings.Contains(string(body), `"role":"plan"`) {
		t.Fatalf("demote boss: %d %s", code, body)
	}
	if code, _ := boss.do("GET", "/api/v1/targets", nil); code != 401 {
		t.Errorf("session after demote: %d", code)
	}
	if code, _ := bossTok.do("GET", "/api/v1/targets", nil); code != 401 {
		t.Errorf("token of a demoted admin: %d", code)
	}
	// admin is the last admin now.
	if code, body := e.login("boss", "boss-long-password").do("PATCH", "/api/v1/users/admin", map[string]string{"role": "read"}); code != 403 {
		t.Errorf("demoted boss still manages users: %d %s", code, body)
	}
	if code, body := adm.do("PATCH", "/api/v1/users/nobody", map[string]string{"role": "read"}); code != 404 {
		t.Errorf("unknown user: %d %s", code, body)
	}
	if code, body := adm.do("PATCH", "/api/v1/users/boss", map[string]any{}); code != 400 {
		t.Errorf("empty change: %d %s", code, body)
	}

	// Events: user.created and user.changed, never a password.
	evs, err := e.st.EventsAfter(e.ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var created, changed int
	for _, ev := range evs {
		switch ev.Type {
		case "user.created":
			created++
		case "user.changed":
			changed++
		}
		if strings.Contains(string(ev.Data), "password") {
			t.Errorf("event %s holds a password field: %s", ev.Type, ev.Data)
		}
	}
	if created != 3 || changed != 3 {
		t.Errorf("events: created=%d changed=%d", created, changed)
	}
	e.noSecrets()
}

// The login limit per (name, client) grows and ends; another client is
// not blocked (risks.md §1 worst case 5).
func TestLoginBackoff(t *testing.T) {
	e := newEnv(t, true)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	e.srv.loginPair = newBackoffLimiter(func() time.Time { return now }, 5, time.Minute, 15*time.Minute, time.Hour)
	c := e.anon()
	for i := 0; i < 5; i++ {
		if code, _ := c.do("POST", "/api/v1/session", map[string]string{"username": "admin", "password": "wrong-wrong"}); code != 401 {
			t.Fatalf("try %d: %d", i, code)
		}
	}
	rec := c.raw("POST", "/api/v1/session", map[string]string{"username": "admin", "password": e.password})
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("blocked login: %d %s", rec.Code, rec.Header().Get("Retry-After"))
	}
	now = now.Add(time.Minute)
	if e.login("admin", e.password) == nil {
		t.Fatal("login after the wait")
	}
}
