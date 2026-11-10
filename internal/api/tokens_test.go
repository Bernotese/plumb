package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type createdToken struct {
	Token struct {
		ID        string  `json:"id"`
		Name      string  `json:"name"`
		Tier      string  `json:"tier"`
		MaxRisk   *string `json:"max_risk"`
		CreatedBy string  `json:"created_by"`
		RevokedAt *string `json:"revoked_at"`
	} `json:"token"`
	Bearer string `json:"bearer"`
}

// createToken makes a token through the API. Its bearer is in the 201
// body by design, so it is not added to the secret scan of noSecrets.
func (e *env) createToken(adm *client, body map[string]any) createdToken {
	e.t.Helper()
	rec := adm.raw("POST", "/api/v1/tokens", body)
	if rec.Code != 201 {
		e.t.Fatalf("create token: %d %s", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		e.t.Errorf("create token: Cache-Control %q, want no-store", cc)
	}
	var out createdToken
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	if rec.Header().Get("Location") != "/api/v1/tokens/"+out.Token.ID {
		e.t.Errorf("Location %q", rec.Header().Get("Location"))
	}
	return out
}

// Create → use → revoke → 401. The bearer is only in the POST answer.
func TestTokenLifecycle(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	ct := e.createToken(adm, map[string]any{"name": "CI deploy", "tier": "apply", "max_risk": "reversible"})
	if ct.Token.Tier != "apply" || ct.Token.MaxRisk == nil || *ct.Token.MaxRisk != "reversible" ||
		ct.Token.CreatedBy != "user:admin" || ct.Token.RevokedAt != nil || !strings.HasPrefix(ct.Bearer, ct.Token.ID+".") {
		t.Fatalf("created: %+v", ct)
	}
	_, secret, _ := strings.Cut(ct.Bearer, ".")
	hash := sha256Hex(secret)

	tok := &client{e: e, bearer: ct.Bearer}
	code, body := tok.do("GET", "/api/v1/session", nil)
	if code != 200 || !strings.Contains(string(body), `"principal":"token:`+ct.Token.ID+`"`) {
		t.Fatalf("use: %d %s", code, body)
	}

	code, list := adm.do("GET", "/api/v1/tokens", nil)
	if code != 200 || !strings.Contains(string(list), ct.Token.ID) || !strings.Contains(string(list), `"last_used_at":"`) {
		t.Fatalf("list: %d %s", code, list)
	}

	if code, body := adm.do("DELETE", "/api/v1/tokens/"+ct.Token.ID, nil); code != 204 || len(body) != 0 {
		t.Fatalf("revoke: %d %s", code, body)
	}
	if code, body := tok.do("GET", "/api/v1/session", nil); code != 401 {
		t.Errorf("revoked token: %d %s, want 401", code, body)
	}
	if code, _ := adm.do("DELETE", "/api/v1/tokens/"+ct.Token.ID, nil); code != 204 {
		t.Errorf("revoke twice: %d, want 204", code)
	}
	if code, body := adm.do("DELETE", "/api/v1/tokens/tok_nope", nil); code != 404 || problemType(body) != "not-found" {
		t.Errorf("revoke unknown: %d %s", code, body)
	}

	// Soft revoke: the row stays, with revoked_at.
	_, list = adm.do("GET", "/api/v1/tokens", nil)
	var page struct {
		Items []struct {
			ID        string
			RevokedAt *string `json:"revoked_at"`
		}
	}
	json.Unmarshal(list, &page)
	if len(page.Items) != 1 || page.Items[0].RevokedAt == nil {
		t.Errorf("after revoke: %s", list)
	}

	// No answer but the 201 holds the bearer; none holds the hash. The
	// event log holds neither.
	e.mu.Lock()
	for _, b := range e.seen {
		s := string(b)
		if strings.Contains(s, hash) || (strings.Contains(s, secret) && !strings.Contains(s, `"bearer":`)) {
			t.Errorf("a response leaks the token: %.120s", s)
		}
	}
	e.mu.Unlock()
	types := map[string]string{}
	sub := e.events.Subscribe(e.ctx, 0)
	for done := false; !done; {
		select {
		case ev := <-sub.C:
			if strings.Contains(string(ev.Data), secret) || strings.Contains(string(ev.Data), hash) {
				t.Errorf("event %s leaks the token: %s", ev.Type, ev.Data)
			}
			if strings.HasPrefix(ev.Type, "token.") {
				types[ev.Type] = string(ev.Data)
				if ev.Subject != "token:"+ct.Token.ID || ev.Actor != "user:admin" {
					t.Errorf("event %s: subject %s actor %s", ev.Type, ev.Subject, ev.Actor)
				}
			}
		case <-time.After(200 * time.Millisecond):
			done = true
		}
	}
	want := `{"id":"` + ct.Token.ID + `","name":"CI deploy","tier":"apply"}`
	if types["token.created"] != want || types["token.revoked"] != want || len(types) != 2 {
		t.Errorf("token events: %v", types) // a second revoke emits no event
	}
	e.noSecrets()
}

// D-046: only an apply token has a max_risk, and never destructive.
func TestTokenCreateValidation(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	for _, c := range []struct {
		body    map[string]any
		pointer string
	}{
		{map[string]any{"name": "x", "tier": "apply"}, "/max_risk"},
		{map[string]any{"name": "x", "tier": "apply", "max_risk": "destructive"}, "/max_risk"},
		{map[string]any{"name": "x", "tier": "plan", "max_risk": "reversible"}, "/max_risk"},
		{map[string]any{"name": "x", "tier": "read", "max_risk": "disruptive"}, "/max_risk"},
		{map[string]any{"name": "x", "tier": "admin"}, "/tier"},
		{map[string]any{"name": " ", "tier": "read"}, "/name"},
		{map[string]any{"name": strings.Repeat("é", 101), "tier": "read"}, "/name"},
	} {
		code, body := adm.do("POST", "/api/v1/tokens", c.body)
		var p problemBody
		json.Unmarshal(body, &p)
		if code != 422 || len(p.Errors) != 1 || p.Errors[0].Pointer != c.pointer {
			t.Errorf("%v: %d %s, want 422 at %s", c.body, code, body, c.pointer)
		}
	}
	if code, _ := adm.do("POST", "/api/v1/tokens", map[string]any{"name": "x", "tier": "read", "extra": 1}); code != 400 {
		t.Errorf("unknown field: %d, want 400", code)
	}
	// 100 characters is fine, even when they are more than 100 bytes.
	e.createToken(adm, map[string]any{"name": strings.Repeat("é", 100), "tier": "read"})
	if code, body := adm.do("GET", "/api/v1/tokens", nil); code != 200 || strings.Count(string(body), `"id":`) != 1 {
		t.Errorf("only the valid request makes a token: %s", body)
	}
}

// A session that must change its password cannot manage tokens.
func TestTokensNeedPasswordChange(t *testing.T) {
	e := newEnv(t, true)
	first := e.login("admin", e.password)
	if code, body := first.do("POST", "/api/v1/tokens", map[string]any{"name": "x", "tier": "read"}); code != 403 || problemType(body) != "password-change-required" {
		t.Errorf("must change: %d %s", code, body)
	}
}

func TestTokenListPages(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	var ids []string
	for _, n := range []string{"a", "b", "c"} {
		ids = append(ids, e.createToken(adm, map[string]any{"name": n, "tier": "read"}).Token.ID)
	}
	var got []string
	path := "/api/v1/tokens?limit=2"
	for i := 0; i < 3 && path != ""; i++ {
		code, body := adm.do("GET", path, nil)
		var p struct {
			Items []struct{ ID string }
			Next  *string
		}
		if code != 200 || json.Unmarshal(body, &p) != nil {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		for _, it := range p.Items {
			got = append(got, it.ID)
		}
		path = ""
		if p.Next != nil {
			path = "/api/v1/tokens?limit=2&cursor=" + *p.Next
		}
	}
	if strings.Join(got, ",") != ids[2]+","+ids[1]+","+ids[0] {
		t.Errorf("pages: %v, want newest first %v", got, ids)
	}
}

// D-070: an open event stream of a token ends at once when the token is
// revoked, not at the next keep-alive.
func TestRevokeEndsStreams(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.srv.cfg.KeepAlive = time.Hour // only the revoke can end the stream
	ct := e.createToken(adm, map[string]any{"name": "watcher", "tier": "read"})
	other := e.token(TierRead, "")
	s := e.stream(&client{e: e, bearer: ct.Bearer}, "", "")
	keep := e.stream(other, "", "")
	if code, _ := adm.do("DELETE", "/api/v1/tokens/"+ct.Token.ID, nil); code != 204 {
		t.Fatal(code)
	}
	ended := make(chan bool, 1)
	go func() {
		for s.sc.Scan() {
		}
		ended <- true
	}()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream of the revoked token is still open")
	}
	// The stream of another token stays open and gets the revoke event.
	if ev := keep.nextEvent(t); ev.typ != "token.revoked" {
		t.Errorf("other stream: %+v", ev)
	}
}
