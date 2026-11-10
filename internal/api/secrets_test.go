package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/wire"
)

const testValue = "s3cr3t-Value-that-must-never-leak"

func TestSecretsFlow(t *testing.T) {
	e := newEnv(t, true)
	var logs bytes.Buffer
	e.srv.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	adm := e.admin()

	code, body := adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": testValue})
	if code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	var v secretView
	json.Unmarshal(body, &v)
	if v.Name != "app-db" || v.Version < 1 || v.UpdatedBy != "user:admin" || v.UsedBy == nil {
		t.Fatalf("put view: %s", body)
	}
	code, body = adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": testValue + "2"})
	var v2 secretView
	json.Unmarshal(body, &v2)
	if code != 200 || v2.Version <= v.Version {
		t.Fatalf("replace: %d %s", code, body)
	}
	adm.do("PUT", "/api/v1/secrets/b", map[string]string{"value": "x"})
	adm.do("PUT", "/api/v1/secrets/c", map[string]string{"value": "y"})

	// The list has names only, pages work.
	readTok := e.token(TierRead, "")
	code, body = readTok.do("GET", "/api/v1/secrets?limit=2", nil)
	var pg struct {
		Items []secretView
		Next  *string
	}
	json.Unmarshal(body, &pg)
	if code != 200 || len(pg.Items) != 2 || pg.Items[0].Name != "app-db" || pg.Next == nil {
		t.Fatalf("list: %d %s", code, body)
	}
	code, body = readTok.do("GET", "/api/v1/secrets?limit=2&cursor="+*pg.Next, nil)
	json.Unmarshal(body, &pg)
	if code != 200 || len(pg.Items) != 1 || pg.Items[0].Name != "c" || pg.Next != nil {
		t.Fatalf("list page 2: %d %s", code, body)
	}

	// Values are stored as sent.
	got, err := e.sec.Resolve(e.ctx, []string{"app-db"})
	if err != nil || got["app-db"].Text != testValue+"2" {
		t.Fatalf("resolve: %v", err)
	}

	// Tokens of every tier cannot write; a session that must change its
	// password cannot either.
	for _, c := range []*client{readTok, e.token(TierApply, wire.RiskDisruptive)} {
		if code, body := c.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "evil"}); code != 403 || problemType(body) != "human-required" {
			t.Errorf("token put: %d %s", code, body)
		}
	}
	// Wrong origin (CSRF).
	if code, _ := (&client{e: e, cookie: adm.cookie, origin: "https://evil.test"}).do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "evil"}); code != 403 {
		t.Errorf("cross-origin put: %d", code)
	}

	// Delete.
	if code, body := adm.do("DELETE", "/api/v1/secrets/b", nil); code != 204 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if code, body := adm.do("DELETE", "/api/v1/secrets/b", nil); code != 404 || problemType(body) != "not-found" {
		t.Fatalf("delete again: %d %s", code, body)
	}
	if code, _ := adm.do("DELETE", "/api/v1/secrets/NOT_VALID", nil); code != 404 {
		t.Fatalf("delete bad name: %d", code)
	}

	// Events carry the name only.
	types := map[string]int{}
	sub := e.events.Subscribe(e.ctx, 0)
	timeout := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case ev := <-sub.C:
			types[ev.Type]++
			if strings.HasPrefix(ev.Type, "secret.") {
				if ev.Subject != "secret:app-db" && ev.Subject != "secret:b" && ev.Subject != "secret:c" {
					t.Errorf("subject %s", ev.Subject)
				}
				var data map[string]any
				json.Unmarshal(ev.Data, &data)
				if len(data) != 1 || data["name"] == nil {
					t.Errorf("event data %s", ev.Data)
				}
			}
			if bytes.Contains(ev.Data, []byte(testValue)) {
				t.Errorf("event holds the value: %s", ev.Data)
			}
		case <-timeout:
			break loop
		}
	}
	if types["secret.updated"] != 4 || types["secret.deleted"] != 1 {
		t.Errorf("events: %v", types)
	}

	for _, b := range e.seen {
		if bytes.Contains(b, []byte(testValue)) || bytes.Contains(b, []byte("evil")) {
			t.Errorf("a response holds a value: %s", b)
		}
	}
	if strings.Contains(logs.String(), testValue) {
		t.Errorf("the log holds the value: %s", logs.String())
	}
	e.noSecrets()
}

func TestSecretBadBodies(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	const leak = "LEAKME"
	cases := []struct {
		name string
		body []byte
		code int
	}{
		{"syntax error inside the value", []byte(`{"value": ` + leak + `}`), 400},
		{"syntax error, string", []byte(`{"value": "` + leak), 400},
		{"unknown field", []byte(`{"value": "x", "` + leak + `": 1}`), 400},
		{"two objects", []byte(`{"value": "x"} {"value": "` + leak + `"}`), 400},
		{"number", []byte(`{"value": 1}`), 400},
		{"no value", []byte(`{}`), 422},
		{"empty", []byte(`{"value": ""}`), 422},
		{"invalid utf-8", []byte("{\"value\": \"" + leak + "\xff\"}"), 422},
		{"lone high surrogate", []byte(`{"value": "\ud800"}`), 422},
		{"lone low surrogate", []byte(`{"value": "\udc00"}`), 422},
		{"literal replacement plus lone surrogate", []byte("{\"value\": \"�\\ud800\"}"), 422},
		{"too long", []byte(`{"value": "` + strings.Repeat("x", secret.MaxValueBytes+1) + `"}`), 422},
		{"too large body", []byte(`{"value": "` + strings.Repeat("x", 7*secret.MaxValueBytes) + `"}`), 413},
	}
	for _, c := range cases {
		code, body := adm.do("PUT", "/api/v1/secrets/x", c.body)
		if code != c.code {
			t.Errorf("%s: %d %s, want %d", c.name, code, body, c.code)
		}
		if bytes.Contains(body, []byte(leak)) || bytes.Contains(body, []byte("LEAK")) || bytes.Contains(body, []byte("'L'")) {
			t.Errorf("%s: the answer echoes the body: %s", c.name, body)
		}
	}
	// Real U+FFFD and surrogate pairs are kept.
	for _, v := range []string{"�", `\ud800`, "a�b", "😀"} {
		if code, body := adm.do("PUT", "/api/v1/secrets/ok", map[string]string{"value": v}); code != 200 {
			t.Errorf("%q: %d %s", v, code, body)
		}
		got, _ := e.sec.Resolve(e.ctx, []string{"ok"})
		if got["ok"].Text != v {
			t.Errorf("%q stored as %q", v, got["ok"].Text)
		}
	}
	if code, body := adm.do("PUT", "/api/v1/secrets/ok", []byte(`{"value": "\ud83d\ude00"}`)); code != 200 {
		t.Errorf("escaped pair: %d %s", code, body)
	} else if got, _ := e.sec.Resolve(e.ctx, []string{"ok"}); got["ok"].Text != "😀" {
		t.Errorf("escaped pair stored as %q", got["ok"].Text)
	}
	// QA-2: an escaped U+FFFD is a real character and is stored as sent.
	if code, body := adm.do("PUT", "/api/v1/secrets/ok", []byte(`{"value": "\ufffd\uFFFD"}`)); code != 200 {
		t.Errorf("escaped U+FFFD: %d %s", code, body)
	} else if got, _ := e.sec.Resolve(e.ctx, []string{"ok"}); got["ok"].Text != "��" {
		t.Errorf("escaped U+FFFD stored as %q", got["ok"].Text)
	}
	if code, _ := adm.do("PUT", "/api/v1/secrets/Bad_Name", map[string]string{"value": "x"}); code != 422 {
		t.Errorf("bad name: %d", code)
	}
	// Nothing of the bad bodies was stored.
	list, _ := e.sec.List(e.ctx)
	if len(list) != 1 || list[0].Name != "ok" {
		t.Errorf("stored: %+v", list)
	}
}

func TestSecretInUse(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "x"})
	e.sec.SetUsage(func(context.Context) (map[string]secret.Usage, error) {
		return map[string]secret.Usage{"app-db": {Targets: []string{"web1"}, Plans: []int64{16}}}, nil
	})
	code, body := adm.do("DELETE", "/api/v1/secrets/app-db", nil)
	var p struct {
		Type    string
		Targets []string
		Plans   []int64
	}
	json.Unmarshal(body, &p)
	if code != 409 || problemType(body) != "secret-in-use" || len(p.Targets) != 1 || p.Plans[0] != 16 {
		t.Fatalf("in use: %d %s", code, body)
	}
	_, body = adm.do("GET", "/api/v1/secrets", nil)
	if !strings.Contains(string(body), `"used_by":["web1"]`) {
		t.Fatalf("used_by: %s", body)
	}
}

// A session that still must change its password cannot touch secrets.
func TestSecretNeedsPasswordChange(t *testing.T) {
	e := newEnv(t, true)
	first := e.login("admin", e.password)
	if code, body := first.do("PUT", "/api/v1/secrets/a", map[string]string{"value": "x"}); code != 403 || problemType(body) != "password-change-required" {
		t.Fatalf("put: %d %s", code, body)
	}
}

func TestSecretFailureDoesNotLeak(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	var logs bytes.Buffer
	e.srv.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	before, err := e.st.LastEventSeq(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.sec.SetUsage(func(context.Context) (map[string]secret.Usage, error) {
		return nil, errors.New("qa usage lookup unavailable")
	})
	code, body := adm.do("PUT", "/api/v1/secrets/failure", map[string]string{"value": testValue})
	if code != 500 || problemType(body) != "internal" {
		t.Fatalf("put: %d %s", code, body)
	}
	if !strings.Contains(logs.String(), "qa usage lookup unavailable") {
		t.Fatal("error path did not log")
	}
	for _, response := range e.seen {
		if bytes.Contains(response, []byte(testValue)) {
			t.Fatal("value in response")
		}
	}
	if strings.Contains(logs.String(), testValue) {
		t.Fatal("value in error log")
	}
	events, err := e.st.EventsAfter(e.ctx, before, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("failed put emitted %d events", len(events))
	}
	e.sec.SetUsage(nil)
	list, err := e.sec.List(e.ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("failed put persisted metadata: %v", err)
	}
}
