package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
)

// D-441: while the password must change, a session may call only the
// /session operations. No other operation may carry duringReset.
func TestOnlySessionRoutesRunDuringReset(t *testing.T) {
	e := newEnv(t, true)
	if len(e.srv.routes) < 50 {
		t.Fatalf("the access table has only %d routes", len(e.srv.routes))
	}
	var during []string
	for pattern, a := range e.srv.routes {
		if !a.duringReset {
			continue
		}
		during = append(during, pattern)
		_, path, _ := strings.Cut(pattern, " ")
		if path != "/api/v1/session" && path != "/api/v1/session/password" {
			t.Errorf("%s runs while the password must change", pattern)
		}
	}
	if len(during) != 3 {
		t.Errorf("duringReset routes: %v (want GET/DELETE /session and PUT /session/password)", during)
	}
	for _, pattern := range []string{"POST /api/v1/restore/confirm", "POST /api/v1/tokens/{id}/enable"} {
		a, ok := e.srv.routes[pattern]
		if !ok || a.duringReset || !a.sessionOnly || a.tier != TierAdmin {
			t.Errorf("%s: %+v %v", pattern, a, ok)
		}
	}
}

// D-441: the shown-once password after a restore cannot end restored mode
// or bring back a disabled token; after the change both work.
func TestMustChangeCannotConfirmRestoreOrEnableToken(t *testing.T) {
	mode := &plan.RestoreMode{}
	e := newEnv(t, true, func(c *plan.Config) { c.Restore = mode })
	adm0 := e.admin()
	code, body := adm0.do("POST", "/api/v1/tokens", map[string]string{"name": "kept", "tier": "plan"})
	var tok struct {
		Token struct {
			ID string `json:"id"`
		} `json:"token"`
	}
	if json.Unmarshal(body, &tok); code != 201 || tok.Token.ID == "" {
		t.Fatalf("token: %d %s", code, body)
	}
	if err := e.st.Restore(e.ctx, time.Now(), json.RawMessage(`{}`), HashPassword("restored-password-1", fastPasswords)); err != nil {
		t.Fatal(err)
	}
	if err := mode.Load(e.ctx, e.st); err != nil || !mode.Active() {
		t.Fatalf("mode: %v", err)
	}
	adm := e.login("admin", "restored-password-1")
	if adm == nil {
		t.Fatal("the shown-once password does not log in")
	}
	confirm := map[string]any{"old_controller_off": true, "reason": "the old one is gone"}
	enable := "/api/v1/tokens/" + tok.Token.ID + "/enable"
	for _, c := range []struct{ method, path string }{{"POST", "/api/v1/restore/confirm"}, {"POST", enable}} {
		var in any
		if c.path == "/api/v1/restore/confirm" {
			in = confirm
		}
		if code, body := adm.do(c.method, c.path, in); code != 403 || problemType(body) != "password-change-required" {
			t.Errorf("%s %s before the change: %d %s", c.method, c.path, code, body)
		}
	}
	if !mode.Active() {
		t.Fatal("restored mode ended before the password change")
	}
	if code, body := adm.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "restored-password-1", "new_password": "a-new-good-password"}); code != 204 {
		t.Fatalf("change: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/restore/confirm", confirm); code != 200 {
		t.Errorf("confirm after the change: %d %s", code, body)
	}
	if mode.Active() {
		t.Error("restored mode still on after the confirm")
	}
	if code, body := adm.do("POST", enable, nil); code != 200 {
		t.Errorf("enable after the change: %d %s", code, body)
	}
}
