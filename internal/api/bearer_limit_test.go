package api

import (
	"fmt"
	"strings"
	"testing"
)

// A client that retries a revoked token is limited for that token id only;
// a valid token from the same client still works (D-157).
func TestBadBearerLimitScope(t *testing.T) {
	e := newEnv(t, true)
	good := e.token(TierRead, "")
	bad := e.token(TierRead, "")
	id, _, _ := strings.Cut(bad.bearer, ".")
	if _, revoked, err := e.st.RevokeToken(e.ctx, id); err != nil || !revoked {
		t.Fatalf("revoke: %v %v", revoked, err)
	}
	for i := 0; i < 20; i++ {
		if code, _ := bad.do("GET", "/api/v1/images", nil); code != 401 {
			t.Fatalf("revoked try %d: %d", i, code)
		}
	}
	rec := bad.raw("GET", "/api/v1/images", nil)
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" || problemType(rec.Body.Bytes()) != "rate-limited" {
		t.Errorf("revoked token after 20 failures: %d %s", rec.Code, rec.Body)
	}
	if code, body := good.do("GET", "/api/v1/images", nil); code != 200 {
		t.Errorf("valid token from the same client: %d %s", code, body)
	}

	// A blocked id answers 429 whether it exists or not: no oracle for ids.
	ghost := &client{e: e, bearer: "tok_0000000000000000.x"}
	for i := 0; i < 20; i++ {
		ghost.do("GET", "/api/v1/images", nil)
	}
	if code, _ := ghost.do("GET", "/api/v1/images", nil); code != 429 {
		t.Errorf("unknown id after 20 failures: %d", code)
	}
}

// Guessing with new ids is bounded per client: after 300 failures every
// bearer from the client gets 429, even a valid one (D-157).
func TestBadBearerHardCap(t *testing.T) {
	e := newEnv(t, true)
	good := e.token(TierRead, "")
	for i := 0; i < 300; i++ {
		c := &client{e: e, bearer: fmt.Sprintf("tok_%016x.guess", i)}
		if code, _ := c.do("GET", "/api/v1/images", nil); code != 401 {
			t.Fatalf("guess %d: %d", i, code)
		}
	}
	c := &client{e: e, bearer: "tok_ffffffffffffffff.guess"}
	if code, _ := c.do("GET", "/api/v1/images", nil); code != 429 {
		t.Errorf("guess after the cap: %d", code)
	}
	if code, _ := good.do("GET", "/api/v1/images", nil); code != 429 {
		t.Errorf("valid token after the cap: %d", code)
	}
	// Sessions are not limited by bad bearers.
	if code, _ := e.admin().do("GET", "/api/v1/session", nil); code != 200 {
		t.Errorf("session after the cap: %d", code)
	}
}
