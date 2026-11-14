package api

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// The contract supplies access expectations; endpoints supplies only safe calls.
// In particular x-plumb-role, rather than an own-session allowlist, sets roles.
func TestContractRoleMatrixQA(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	callers := []*client{e.newUser(adm, "reader", TierRead), e.newUser(adm, "planner", TierPlan), e.newUser(adm, "operator", TierApply), adm,
		e.token(TierRead, ""), e.token(TierPlan, ""), e.token(TierApply, wire.RiskDisruptive)}
	roles := []string{TierRead, TierPlan, TierApply, TierAdmin, TierRead, TierPlan, TierApply}
	k := loadContract(t)
	paths := k.doc["paths"].(map[string]any)
	names := make([]string, 0, len(paths))
	for path := range paths {
		names = append(names, path)
	}
	// Logout runs last, so every role can call the other operations first.
	sort.Slice(names, func(i, j int) bool {
		if names[i] == "/session" || names[j] == "/session" {
			return names[j] == "/session"
		}
		return names[i] < names[j]
	})
	for _, path := range names {
		for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"} {
			op, ok := paths[path].(map[string]any)[method].(map[string]any)
			if !ok {
				continue
			}
			ep := endpoint{method: strings.ToUpper(method), path: "/api/v1" + path}
			if _, planned := op["x-plumb-planned"]; planned {
				ep.path = regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(ep.path, "999999")
			} else if method == "post" && path == "/session" {
				ep.body = map[string]string{} // Public login, invalid input avoids a new session.
			} else {
				pattern := regexp.MustCompile("^" + regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, `[^/]+`) + "$")
				found := false
				for _, fixture := range endpoints {
					concrete, _, _ := strings.Cut(strings.TrimPrefix(fixture.path, "/api/v1"), "?")
					if fixture.method == ep.method && pattern.MatchString(concrete) {
						ep, found = fixture, true
						break
					}
				}
				if !found {
					t.Errorf("no safe call for %s %s", method, path)
					continue
				}
			}
			security, ok := op["security"].([]any)
			if !ok {
				security = k.doc["security"].([]any)
			}
			tokenRole, sessionRole := "", TierAdmin
			for _, entry := range security {
				if tiers, ok := entry.(map[string]any)["token"].([]any); ok && len(tiers) == 1 {
					tokenRole = tiers[0].(string)
					sessionRole = tokenRole
				}
			}
			if role, ok := op["x-plumb-role"].(string); ok {
				sessionRole = role
			}
			if _, ok := tierLevel[sessionRole]; !ok {
				t.Fatalf("unknown contract role %q", sessionRole)
			}
			for i, c := range callers {
				t.Run(fmt.Sprintf("%s%s/%s/token=%t", ep.method, path, roles[i], c.bearer != ""), func(t *testing.T) {
					code, body := c.do(ep.method, ep.path, ep.body)
					if _, planned := op["x-plumb-planned"]; planned {
						if code != 404 {
							t.Errorf("planned operation: %d %s", code, body)
						}
						return
					}
					if len(security) == 0 {
						if code != 400 {
							t.Errorf("public login: %d %s", code, body)
						}
						return
					}
					want, typ := 0, ""
					switch {
					case c.bearer != "" && tokenRole == "":
						want, typ = 403, "human-required"
					case c.bearer != "" && tierLevel[roles[i]] < tierLevel[tokenRole], c.bearer == "" && tierLevel[roles[i]] < tierLevel[sessionRole]:
						want, typ = 403, "forbidden"
					}
					if want != 0 {
						if code != want || problemType(body) != typ {
							t.Errorf("got %d %s, want %d %s", code, body, want, typ)
						}
					} else if code == 401 || code == 403 || code >= 500 {
						t.Errorf("expected access: %d %s", code, body)
					}
				})
			}
		}
	}
	e.noSecrets()
}

// L2 of #250: distributed guesses can exhaust the user-wide 100/min cap,
// but blocked requests must not prolong it, and another user stays usable.
func TestLoginUserLimitEndsQA(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.newUser(adm, "other", TierRead)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	e.srv.loginUser.now = clock
	e.srv.loginIP.now = clock
	e.srv.loginPair.now = clock
	for i := range 100 {
		if code := e.loginFrom(fmt.Sprintf("203.0.113.%d", i+1), "admin", "wrong"); code != 401 {
			t.Fatalf("failure %d: %d", i, code)
		}
	}
	if code := e.loginFrom("192.0.2.1", "admin", e.password); code != 429 {
		t.Fatalf("user cap: %d", code)
	}
	if code := e.loginFrom("192.0.2.1", "other", "other-long-password"); code != 200 {
		t.Fatalf("other user: %d", code)
	}
	now = now.Add(time.Minute - time.Nanosecond)
	if code := e.loginFrom("192.0.2.1", "admin", e.password); code != 429 {
		t.Fatalf("before expiry: %d", code)
	}
	now = now.Add(time.Nanosecond)
	if code := e.loginFrom("192.0.2.1", "admin", e.password); code != 200 {
		t.Fatalf("at expiry: %d", code)
	}
}
