package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/store"
)

func devGet(h http.Handler, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// D-080: the route exists only on a dev server with a token.
func TestDevLoginOnlyInDev(t *testing.T) {
	for _, tc := range []struct {
		name  string
		dev   bool
		token string
	}{{"production", false, "tok"}, {"dev without token", true, ""}} {
		e := newEnv(t, tc.dev)
		e.st.CreateUser(e.ctx, store.User{Name: DevUser, PasswordHash: "!"})
		e.srv.cfg.DevLoginToken = tc.token
		h := e.srv.Handler()
		if rec := devGet(h, "/dev/login?token=tok&next=/"); rec.Code != 404 || rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("%s: %d %v", tc.name, rec.Code, rec.Header())
		}
	}
}

func TestDevLogin(t *testing.T) {
	e := newEnv(t, true)
	if _, err := e.st.CreateUser(e.ctx, store.User{Name: DevUser, PasswordHash: "!"}); err != nil {
		t.Fatal(err)
	}
	const token = "a-256-bit-token-for-the-test-xxxxxxxxxxxxx"
	e.srv.cfg.DevLoginToken = token
	h := e.srv.Handler()

	for _, bad := range []string{"", "wrong", token + "x", token[:10]} {
		if rec := devGet(h, "/dev/login?token="+url.QueryEscape(bad)+"&next=/"); rec.Code != 404 || rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("token %q: %d", bad, rec.Code)
		}
	}

	rec := devGet(h, "/dev/login?token="+token+"&next="+url.QueryEscape("/issues/7?x=1"))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/issues/7?x=1" {
		t.Fatalf("login: %d %v", rec.Code, rec.Header())
	}
	var ck *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == e.srv.cookieName() {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie: %+v", ck)
	}
	c := &client{e: e, cookie: ck}
	code, body := c.do("GET", "/api/v1/session", nil)
	if code != 200 || !contains(string(body), `"principal":"user:dev"`) || !contains(string(body), `"must_change_password":false`) {
		t.Errorf("session: %d %s", code, body)
	}
	// The dev user has no usable password.
	if code, _ := e.anon().do("POST", "/api/v1/session", map[string]string{"username": DevUser, "password": "!"}); code != 401 {
		t.Errorf("password login of dev: %d", code)
	}

	// next must be a local path (open redirect).
	for next, want := range map[string]string{
		"/":                     "/",
		"/targets/web1":         "/targets/web1",
		"https://evil.test/":    "/",
		"//evil.test/":          "/",
		"/\\evil.test":          "/",
		"\\\\evil.test":         "/",
		"/a//b":                 "/",
		"evil.test":             "/",
		"javascript:alert(1)":   "/",
		"/x\r\nSet-Cookie: a=b": "/",
		"":                      "/",
	} {
		rec := devGet(h, "/dev/login?token="+token+"&next="+url.QueryEscape(next))
		if got := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || got != want {
			t.Errorf("next %q: %d Location %q, want %q", next, rec.Code, got, want)
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
