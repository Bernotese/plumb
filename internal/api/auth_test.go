package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func TestPasswordHash(t *testing.T) {
	h := HashPassword("correct horse", fastPasswords)
	if !strings.HasPrefix(h, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("hash = %s", h)
	}
	if !VerifyPassword("correct horse", h) || VerifyPassword("correct hors", h) || VerifyPassword("x", "$bcrypt$x") {
		t.Error("verify")
	}
	if h1, h2 := HashPassword("a", fastPasswords), HashPassword("a", fastPasswords); h1 == h2 {
		t.Error("no salt")
	}
	// A stored hash with absurd parameters is refused, not computed.
	if VerifyPassword("a", "$argon2id$v=19$m=99999999,t=1,p=1$c2FsdA$aGFzaA") {
		t.Error("huge memory accepted")
	}
}

func TestBootstrapAndLogin(t *testing.T) {
	e := newEnv(t, false) // production cookie rules
	if len(e.password) < 20 {
		t.Fatalf("bootstrap password %q", e.password)
	}
	if pw, created, _ := BootstrapAdmin(e.ctx, e.st, fastPasswords); created || pw != "" {
		t.Error("a second start made another admin")
	}
	u, _ := e.st.UserByName(e.ctx, "admin")
	if !strings.HasPrefix(u.PasswordHash, "$argon2id$") || !u.MustChangePassword {
		t.Errorf("admin = %+v", u)
	}

	// Login CSRF: no or a foreign Origin.
	for _, origin := range []string{"none", "https://evil.test", "http://" + host} {
		c := &client{e: e, origin: origin}
		if code, body := c.do("POST", "/api/v1/session", map[string]string{"username": "admin", "password": e.password}); code != 403 || problemType(body) != "origin-mismatch" {
			t.Errorf("origin %s: %d %s", origin, code, body)
		}
	}

	rec := e.anon().raw("POST", "/api/v1/session", map[string]string{"username": "admin", "password": e.password})
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	var ck *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == e.srv.cookieName() {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" {
		t.Fatalf("cookie = %+v", ck)
	}
	c := &client{e: e, cookie: ck}
	// The first login must change the password; nothing else works.
	if code, body := c.do("GET", "/api/v1/targets", nil); code != 403 || problemType(body) != "password-change-required" {
		t.Errorf("before the change: %d %s", code, body)
	}
	if code, _ := c.do("GET", "/api/v1/session", nil); code != 200 {
		t.Errorf("GET /session during reset: %d", code)
	}
	other := e.login("admin", e.password) // a second session
	if code, body := c.do("PUT", "/api/v1/session/password", map[string]string{"current_password": e.password, "new_password": "short"}); code != 422 {
		t.Errorf("short password: %d %s", code, body)
	}
	if code, _ := c.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "wrong", "new_password": "a-good-long-password"}); code != 401 {
		t.Errorf("wrong current password: %d", code)
	}
	if code, body := c.do("PUT", "/api/v1/session/password", map[string]string{"current_password": e.password, "new_password": "a-good-long-password"}); code != 204 {
		t.Fatalf("change: %d %s", code, body)
	}
	if code, _ := c.do("GET", "/api/v1/targets", nil); code != 200 {
		t.Errorf("after the change: %d", code)
	}
	if code, _ := other.do("GET", "/api/v1/session", nil); code != 401 {
		t.Errorf("other session after a password change: %d", code)
	}
	// A state-changing call of a session needs the Origin.
	c.origin = "none"
	if code, body := c.do("POST", "/api/v1/plans", map[string]any{"reason": "x"}); code != 403 || problemType(body) != "origin-mismatch" {
		t.Errorf("session POST without Origin: %d %s", code, body)
	}
	c.origin = ""
	// Logout.
	if code, _ := c.do("DELETE", "/api/v1/session", nil); code != 204 {
		t.Errorf("logout: %d", code)
	}
	if code, _ := c.do("GET", "/api/v1/session", nil); code != 401 {
		t.Errorf("after logout: %d", code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t, true)
	for i := 0; i < 5; i++ {
		if code, _ := e.anon().do("POST", "/api/v1/session", map[string]string{"username": "admin", "password": "wrong"}); code != 401 {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	rec := e.anon().raw("POST", "/api/v1/session", map[string]string{"username": "admin", "password": e.password})
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" || problemType(rec.Body.Bytes()) != "rate-limited" {
		t.Errorf("after 5 failures, even the right password: %d %s", rec.Code, rec.Body)
	}
	// An unknown user gets the same answer as a wrong password.
	e2 := newEnv(t, true)
	_, a := e2.anon().do("POST", "/api/v1/session", map[string]string{"username": "admin", "password": "wrong"})
	_, b := e2.anon().do("POST", "/api/v1/session", map[string]string{"username": "nobody", "password": "wrong"})
	if string(a) != string(b) {
		t.Errorf("user enumeration: %s vs %s", a, b)
	}
}

func TestDevCookie(t *testing.T) {
	e := newEnv(t, true)
	c := e.login("admin", e.password)
	if c == nil || c.cookie.Secure || !c.cookie.HttpOnly || c.cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("dev cookie = %+v", c.cookie)
	}
}

func TestCreateTokenRules(t *testing.T) {
	e := newEnv(t, true)
	for _, tc := range []struct {
		tier string
		risk wire.Risk
	}{
		{TierApply, wire.RiskDestructive}, {TierApply, ""}, {TierApply, wire.RiskRead}, {TierAdmin, ""},
		{TierRead, wire.RiskReversible}, {TierPlan, wire.RiskDisruptive}, {"root", ""},
	} {
		if _, _, err := CreateToken(e.ctx, e.st, "x", tc.tier, tc.risk, "user:admin"); !errors.Is(err, ErrBadToken) {
			t.Errorf("tier %s max_risk %q: %v", tc.tier, tc.risk, err)
		}
	}
	// The database refuses a destructive token too, whatever calls it.
	if _, err := e.st.CreateToken(e.ctx, store.Token{ID: "t", Name: "x", Hash: "h", Tier: TierApply, MaxRisk: "destructive", CreatedBy: "x"}); err == nil {
		t.Error("store accepted a destructive token")
	}
	bearer, tok, err := CreateToken(e.ctx, e.st, "ci", TierApply, wire.RiskDisruptive, "user:admin")
	if err != nil || !strings.HasPrefix(bearer, tok.ID+".") || tok.Hash != sha256Hex(strings.TrimPrefix(bearer, tok.ID+".")) {
		t.Fatalf("token: %s %+v %v", bearer, tok, err)
	}
	c := &client{e: e, bearer: bearer}
	code, body := c.do("GET", "/api/v1/session", nil)
	var s sessionView
	json.Unmarshal(body, &s)
	if code != 200 || s.Tier != TierApply || s.MaxRisk != "disruptive" || s.Principal != "token:"+tok.ID || s.ExpiresAt != nil {
		t.Errorf("token session: %d %s", code, body)
	}
	// Wrong secret, revoked token, garbage: 401.
	for _, b := range []string{tok.ID + ".wrong", "garbage", tok.ID} {
		if code, _ := (&client{e: e, bearer: b}).do("GET", "/api/v1/session", nil); code != 401 {
			t.Errorf("bearer %q: %d", b, code)
		}
	}
	e.st.RevokeToken(e.ctx, tok.ID)
	if code, _ := c.do("GET", "/api/v1/session", nil); code != 401 {
		t.Errorf("revoked token: %d", code)
	}
}

// The approval rule: no token approves above its max_risk, no token
// approves destructive, a plan token cannot approve at all.
func TestApproveRules(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	planTok := e.token(TierPlan, "")
	rev := e.token(TierApply, wire.RiskReversible)
	dis := e.token(TierApply, wire.RiskDisruptive)

	propose := func(c *client, body any) int64 {
		t.Helper()
		code, b := c.do("POST", "/api/v1/plans", body)
		if code != 201 {
			t.Fatalf("propose: %d %s", code, b)
		}
		var p struct {
			ID   int64
			Risk string
		}
		json.Unmarshal(b, &p)
		return p.ID
	}
	approve := func(c *client, id int64) (int, string) {
		code, b := c.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", map[string]string{"comment": "ok"})
		return code, problemType(b)
	}

	// A reversible change (file content).
	p1 := propose(planTok, map[string]any{"reason": "text", "put": []state.Doc{targetDoc("web1", "new")}})
	if code, typ := approve(planTok, p1); code != 403 || typ != "forbidden" {
		t.Errorf("plan token approves: %d %s", code, typ)
	}
	if code, typ := approve(rev, p1); code != 202 {
		t.Errorf("reversible token, reversible plan: %d %s", code, typ)
	}
	e.eng.Wait()

	// A disruptive change (cores).
	p2 := propose(planTok, map[string]any{"reason": "cores", "put": []state.Doc{doc(state.KindTarget, "web1",
		`{"type":"vm","image":"ubuntu","guest":{"cores":4,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"new"}}]}`)}})
	if code, typ := approve(rev, p2); code != 403 || typ != "risk-too-high" {
		t.Errorf("reversible token, disruptive plan: %d %s", code, typ)
	}
	if got, _ := e.st.GetPlan(e.ctx, p2); got.Status != store.PlanPending {
		t.Errorf("a refused approval changed the plan: %s", got.Status)
	}
	if code, typ := approve(dis, p2); code != 202 {
		t.Errorf("disruptive token, disruptive plan: %d %s", code, typ)
	}
	e.eng.Wait()

	// A destructive change (delete the target): only a human.
	p3 := propose(planTok, map[string]any{"reason": "remove", "delete": []map[string]string{{"kind": "Target", "name": "web1"}}})
	if got, _ := e.st.GetPlan(e.ctx, p3); got.Risk != "destructive" {
		t.Fatalf("plan risk %s", got.Risk)
	}
	for _, c := range []*client{rev, dis} {
		if code, typ := approve(c, p3); code != 403 || typ != "risk-too-high" {
			t.Errorf("token approves destructive: %d %s", code, typ)
		}
	}
	if code, typ := approve(adm, p3); code != 202 {
		t.Errorf("human approves destructive: %d %s", code, typ)
	}
	e.eng.Wait()
	if code, typ := approve(adm, p3); code != 409 || typ != "plan-not-pending" {
		t.Errorf("approve twice: %d %s", code, typ)
	}
	_ = time.Second
}
