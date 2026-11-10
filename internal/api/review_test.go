package api

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// loginFrom logs in from one client address.
func (e *env) loginFrom(ip, user, password string) int {
	c := &client{e: e}
	req := httptest.NewRequest("POST", "/api/v1/session", strings.NewReader(`{"username":"`+user+`","password":"`+password+`"}`))
	req.Host = host
	req.RemoteAddr = ip + ":5000"
	req.Header.Set("Origin", "http://"+host)
	rec := httptest.NewRecorder()
	c.e.h.ServeHTTP(rec, req)
	return rec.Code
}

// Review T-011 M1: a client that guesses cannot lock the real user out.
func TestLoginLockoutIsPerClient(t *testing.T) {
	e := newEnv(t, true)
	for i := 0; i < 5; i++ {
		if code := e.loginFrom("10.1.1.1", "admin", "wrong"); code != 401 {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if code := e.loginFrom("10.1.1.1", "admin", e.password); code != 429 {
		t.Errorf("the guessing client: %d, want 429", code)
	}
	if code := e.loginFrom("10.2.2.2", "admin", e.password); code != 200 {
		t.Errorf("the real user from another client: %d, want 200", code)
	}
	// One client cannot try many names quickly either (20/min per client).
	for i := 0; i < 20; i++ {
		e.loginFrom("10.3.3.3", "user"+strconv.Itoa(i), "x")
	}
	if code := e.loginFrom("10.3.3.3", "someone", "x"); code != 429 {
		t.Errorf("many names from one client: %d, want 429", code)
	}
	// Unknown names leave no per-user key (review m4).
	if e.srv.loginUser.blocked("user:user1") != 0 || len(e.srv.loginUser.keys) != 1 {
		t.Errorf("user keys: %v", e.srv.loginUser.keys)
	}
}

// Review T-011 M2: argon2id runs in a few slots; when they are busy, a
// login gets 429 at once instead of more memory.
func TestHashSlots(t *testing.T) {
	e := newEnv(t, true)
	for i := 0; i < hashSlotCount; i++ {
		e.srv.hashSlots <- struct{}{}
	}
	start := time.Now()
	rec := e.anon().raw("POST", "/api/v1/session", map[string]string{"username": "admin", "password": e.password})
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" || time.Since(start) > time.Second {
		t.Errorf("busy slots: %d %s", rec.Code, rec.Body)
	}
	for i := 0; i < hashSlotCount; i++ {
		<-e.srv.hashSlots
	}
	if code := e.loginFrom("10.9.9.9", "admin", e.password); code != 200 {
		t.Errorf("free slots: %d", code)
	}
}

// Review T-011 m1: an open stream ends with its session.
func TestStreamEndsWithSession(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	ts := httptest.NewServer(e.h)
	t.Cleanup(ts.Close)
	s := e.streamCookie(ts.URL, adm)
	if code, _ := adm.do("DELETE", "/api/v1/session", nil); code != 204 {
		t.Fatal(code)
	}
	ended := make(chan struct{})
	go func() {
		for s.sc.Scan() {
		}
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Error("the stream outlived the session")
	}

	// A revoked token ends its stream too.
	tok := e.token(TierRead, "")
	s2 := e.stream(tok, "", "")
	id, _, _ := strings.Cut(tok.bearer, ".")
	e.st.RevokeToken(e.ctx, id)
	ended2 := make(chan struct{})
	go func() {
		for s2.sc.Scan() {
		}
		close(ended2)
	}()
	select {
	case <-ended2:
	case <-time.After(3 * time.Second):
		t.Error("the stream outlived the token")
	}
}

// Review T-011 m2: streams are capped per principal.
func TestStreamCap(t *testing.T) {
	e := newEnv(t, true)
	tok := e.token(TierRead, "")
	for i := 0; i < maxStreamsPerCaller; i++ {
		e.stream(tok, "", "")
	}
	ts := httptest.NewServer(e.h)
	t.Cleanup(ts.Close)
	req, _ := httpGet(e, ts.URL+"/api/v1/events", tok.bearer)
	if req.StatusCode != 429 {
		t.Errorf("stream %d: %d, want 429", maxStreamsPerCaller+1, req.StatusCode)
	}
	req.Body.Close()
	// Another principal still gets one.
	e.stream(e.token(TierRead, ""), "", "")
}

// Review T-011 m4: the limiter keeps at most maxLimiterKeys keys.
func TestLimiterCap(t *testing.T) {
	l := newFailLimiter(time.Now, 5, time.Hour)
	for i := 0; i < maxLimiterKeys+50; i++ {
		l.fail("k" + strconv.Itoa(i))
	}
	if len(l.keys) > maxLimiterKeys {
		t.Errorf("keys = %d", len(l.keys))
	}
}

// Review T-011 n1: outside --dev the cookie has the __Host- prefix.
func TestHostCookie(t *testing.T) {
	e := newEnv(t, false)
	c := e.login("admin", e.password)
	if c == nil || c.cookie.Name != "__Host-plumb_session" || !c.cookie.Secure || c.cookie.Path != "/" || c.cookie.Domain != "" {
		t.Errorf("cookie = %+v", c.cookie)
	}
}
