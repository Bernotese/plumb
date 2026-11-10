package api

import (
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// endpoint is one operation of api/openapi.yaml with its access rule. The
// calls use ids and bodies that do nothing when they are allowed (404,
// 400), so the matrix can run every principal against every endpoint.
type endpoint struct {
	method, path string
	body         any
	tier         string
	human        bool
}

var endpoints = []endpoint{
	{"GET", "/api/v1/session", nil, TierRead, false},
	{"PUT", "/api/v1/session/password", map[string]string{}, TierAdmin, true},
	{"GET", "/api/v1/images", nil, TierRead, false},
	{"GET", "/api/v1/images/ubuntu", nil, TierRead, false},
	{"GET", "/api/v1/blocks", nil, TierRead, false},
	{"GET", "/api/v1/blocks/motd", nil, TierRead, false},
	{"GET", "/api/v1/rules", nil, TierRead, false},
	{"GET", "/api/v1/rules/nope", nil, TierRead, false},
	{"GET", "/api/v1/targets", nil, TierRead, false},
	{"GET", "/api/v1/targets/web1", nil, TierRead, false},
	{"POST", "/api/v1/targets/nope/reenroll", nil, TierPlan, false},
	{"GET", "/api/v1/plans", nil, TierRead, false},
	{"POST", "/api/v1/plans", []byte(`{"bad":1}`), TierPlan, false},
	{"GET", "/api/v1/plans/1", nil, TierRead, false},
	{"DELETE", "/api/v1/plans/999999", nil, TierPlan, false},
	{"POST", "/api/v1/plans/999999/approve", nil, TierApply, false},
	{"GET", "/api/v1/runs", nil, TierRead, false},
	{"GET", "/api/v1/runs/1", nil, TierRead, false},
	{"GET", "/api/v1/issues", nil, TierRead, false},
	{"GET", "/api/v1/issues/999999", nil, TierRead, false},
	{"POST", "/api/v1/issues/999999/resolve", map[string]string{"action": "revert"}, TierPlan, false},
	{"POST", "/api/v1/issues/999999/comments", map[string]string{"body": "x"}, TierPlan, false},
	{"GET", "/api/v1/secrets", nil, TierRead, false},
	{"PUT", "/api/v1/secrets/tier-test", map[string]string{}, TierAdmin, true}, // allowed → 422 (no value)
	{"DELETE", "/api/v1/secrets/nope", nil, TierAdmin, true},                   // allowed → 404
	{"GET", "/api/v1/revisions", nil, TierRead, false},
	{"GET", "/api/v1/revisions/999/diff", nil, TierRead, false},
	{"POST", "/api/v1/revisions/999/rollback", map[string]string{"reason": "x"}, TierPlan, false},
	{"GET", "/api/v1/events?since=-1", nil, TierRead, false}, // allowed → 400, so the stream never opens
	{"GET", "/api/v1/tokens", nil, TierAdmin, true},
	{"POST", "/api/v1/tokens", map[string]string{}, TierAdmin, true}, // allowed → 422, so no token is made
	{"DELETE", "/api/v1/tokens/tok_nope", nil, TierAdmin, true},
	{"DELETE", "/api/v1/session", nil, TierAdmin, true}, // last: it logs the session out
}

func TestTierMatrix(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	callers := []struct {
		name string
		c    *client
		tier string
	}{
		{"anonymous", e.anon(), ""},
		{"read", e.token(TierRead, ""), TierRead},
		{"plan", e.token(TierPlan, ""), TierPlan},
		{"apply", e.token(TierApply, wire.RiskDisruptive), TierApply},
		{"session", adm, TierAdmin},
	}
	for _, ep := range endpoints {
		for _, cl := range callers {
			code, body := cl.c.do(ep.method, ep.path, ep.body)
			name := cl.name + " " + ep.method + " " + ep.path
			switch {
			case cl.tier == "":
				if code != 401 {
					t.Errorf("%s: %d, want 401", name, code)
				}
			case ep.human && cl.name != "session":
				if code != 403 || problemType(body) != "human-required" {
					t.Errorf("%s: %d %s, want 403 human-required", name, code, body)
				}
			case tierLevel[cl.tier] < tierLevel[ep.tier]:
				if code != 403 || problemType(body) != "forbidden" {
					t.Errorf("%s: %d %s, want 403 forbidden", name, code, body)
				}
			default:
				if code == 401 || code == 403 || code >= 500 {
					t.Errorf("%s: %d %s, want access", name, code, body)
				}
			}
			if ct := body; len(ct) > 0 && code >= 400 && !strings.Contains(string(body), `"urn:plumb:problem:`) {
				t.Errorf("%s: error without problem+json: %s", name, body)
			}
		}
	}
	e.noSecrets()
}

// noSecrets scans every response body of the test for a secret: token
// secrets and hashes, password hashes and session ids (D-046, §7).
func (e *env) noSecrets() {
	e.t.Helper()
	var secrets []string
	for _, id := range []string{"admin"} {
		u, _ := e.st.UserByName(e.ctx, id)
		secrets = append(secrets, u.PasswordHash, e.password)
	}
	rows, err := e.st.TokenHashes(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	secrets = append(secrets, rows...)
	secrets = append(secrets, e.bearers...)
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, body := range e.seen {
		for _, s := range secrets {
			if s != "" && strings.Contains(string(body), s) {
				e.t.Errorf("a response holds a secret: %.80s…", body)
			}
		}
		if strings.Contains(string(body), "$argon2id$") {
			e.t.Errorf("a response holds a password hash: %.80s…", body)
		}
	}
}

// Every endpoint that lists or reads documents works on the seed.
func TestReadEndpoints(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	for _, path := range []string{"/api/v1/images/ubuntu", "/api/v1/blocks/motd", "/api/v1/targets/web1", "/api/v1/plans/1", "/api/v1/runs/1"} {
		if code, body := adm.do("GET", path, nil); code != 200 {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
	code, body := adm.do("GET", "/api/v1/targets/web1", nil)
	if code != 200 || !strings.Contains(string(body), `"phase":"ready"`) || !strings.Contains(string(body), `"agent":"online"`) {
		t.Errorf("target status: %s", body)
	}
	if code, body := adm.do("GET", "/api/v1/images/nope", nil); code != 404 || problemType(body) != "not-found" {
		t.Errorf("missing image: %d %s", code, body)
	}
	if code, _ := adm.do("GET", "/api/v1/nothing", nil); code != 404 {
		t.Errorf("unknown API path: %d", code)
	}
}
