package api

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Bernotese/plumb/internal/wire"
)

type settingsBody struct {
	Backup struct {
		Time   string `json:"time"`
		Keep   int    `json:"keep"`
		Source struct{ Time, Keep string }
	} `json:"backup"`
	Proxmox *json.RawMessage `json:"proxmox"`
}

func readSettings(t *testing.T, body []byte) settingsBody {
	t.Helper()
	var v settingsBody
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("settings: %s", body)
	}
	return v
}

// The proxmox part goes only to a human session; a token gets null and
// cannot change a setting (D-361, review n2 of #276). Every answer and
// the event match the contract.
func TestSettings(t *testing.T) {
	k := loadContract(t)
	e := newEnv(t, true)
	viewCalls := 0
	e.srv.cfg.Proxmox = func(context.Context) *ProxmoxView {
		viewCalls++
		v := &ProxmoxView{ACL: "ok", ACLLines: []ACLLineView{{Path: "/pool/plumb", Role: "PlumbGuests", OK: true}}}
		node, user, port := "pve", "plumb@pve", 8006
		v.Node, v.TokenUser, v.API.Port = &node, &user, &port
		return v
	}
	adm := e.admin()
	check := func(c *client, method string, body any) (int, []byte) {
		t.Helper()
		rec := c.raw(method, "/api/v1/settings", body)
		ct, _, _ := strings.Cut(rec.Header().Get("Content-Type"), ";")
		k.check(t, recorded{method, "/settings", rec.Code, ct, rec.Body.Bytes()})
		return rec.Code, rec.Body.Bytes()
	}

	code, body := check(adm, "GET", nil)
	v := readSettings(t, body)
	if code != 200 || v.Backup.Time != "03:00" || v.Backup.Keep != 7 || v.Backup.Source.Time != "default" || v.Proxmox == nil {
		t.Fatalf("admin GET: %d %s", code, body)
	}
	reader := e.newUser(adm, "reader", TierRead)
	if code, body := check(reader, "GET", nil); code != 200 || readSettings(t, body).Proxmox == nil {
		t.Errorf("read user GET: %d %s", code, body)
	}

	// Tokens of every tier: backup part only, proxmox null, PATCH 403.
	calls := viewCalls
	for _, tier := range []string{TierRead, TierPlan, TierApply} {
		var risk wire.Risk
		if tier == TierApply {
			risk = wire.RiskDisruptive
		}
		tok := e.token(tier, risk)
		code, body := check(tok, "GET", nil)
		if code != 200 || !bytes.Contains(body, []byte(`"proxmox":null`)) || bytes.Contains(body, []byte("pve")) {
			t.Errorf("%s token GET: %d %s", tier, code, body)
		}
		code, body = check(tok, "PATCH", map[string]any{"backup": map[string]any{"keep": 3}})
		if code != 403 || problemType(body) != "human-required" {
			t.Errorf("%s token PATCH: %d %s", tier, code, body)
		}
	}
	if viewCalls != calls {
		t.Errorf("a token GET read the Proxmox view %d times", viewCalls-calls)
	}
	// A person with role apply cannot change settings either.
	applier := e.newUser(adm, "applier", TierApply)
	if code, body := check(applier, "PATCH", map[string]any{"backup": map[string]any{"keep": 3}}); code != 403 {
		t.Errorf("apply user PATCH: %d %s", code, body)
	}

	// Admin changes both; the schedule follows at once.
	code, body = check(adm, "PATCH", map[string]any{"backup": map[string]any{"time": "02:30", "keep": 10}})
	v = readSettings(t, body)
	if code != 200 || v.Backup.Time != "02:30" || v.Backup.Keep != 10 || v.Backup.Source.Time != "setting" || v.Backup.Source.Keep != "setting" {
		t.Fatalf("PATCH: %d %s", code, body)
	}
	if tm, keep := e.backups.Schedule(); tm != "02:30" || keep != 10 {
		t.Errorf("schedule = %s %d", tm, keep)
	}
	// GET /backups shows the same schedule.
	if _, body := adm.do("GET", "/api/v1/backups", nil); !bytes.Contains(body, []byte(`"time":"02:30"`)) {
		t.Errorf("backups: %s", body)
	}
	// null goes back to the default for that field only.
	code, body = check(adm, "PATCH", []byte(`{"backup":{"time":null}}`))
	v = readSettings(t, body)
	if code != 200 || v.Backup.Time != "03:00" || v.Backup.Source.Time != "default" || v.Backup.Keep != 10 {
		t.Errorf("PATCH null: %d %s", code, body)
	}

	// 422 names the field, never the value (D-325); nothing changes.
	for bad, value := range map[string]string{
		`{"backup":{"time":"24:00"}}`: "24:00", `{"backup":{"time":"+1:00"}}`: "+1:00", `{"backup":{"time":7}}`: "",
		`{"backup":{"keep":0}}`: "", `{"backup":{"keep":101}}`: "101", `{"backup":{"keep":7.5}}`: "7.5", `{"backup":{"keep":"9x"}}`: "9x",
	} {
		code, body := check(adm, "PATCH", []byte(bad))
		if code != 422 || problemType(body) != "validation" {
			t.Errorf("%s: %d %s", bad, code, body)
		}
		if value != "" && bytes.Contains(body, []byte(value)) {
			t.Errorf("%s: the answer echoes the value: %s", bad, body)
		}
	}
	for _, bad := range []string{`{}`, `{"backup":{}}`, `{"backup":{"x":1}}`, `{"proxmox":{}}`} {
		if code, body := check(adm, "PATCH", []byte(bad)); code != 400 {
			t.Errorf("%s: %d %s", bad, code, body)
		}
	}
	if tm, keep := e.backups.Schedule(); tm != "03:00" || keep != 10 {
		t.Errorf("after bad PATCHes: %s %d", tm, keep)
	}

	// settings.changed: one per real change, old and new values, valid.
	evSchema := k.schema(t, "/components/schemas/Event")
	events, err := e.st.EventsAfter(e.ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ev := range events {
		if ev.Type != "settings.changed" {
			continue
		}
		raw, _ := json.Marshal(eventView{Seq: ev.Seq, Type: ev.Type, Time: ev.Time.UTC(), Subject: ev.Subject, Actor: ev.Actor, Data: ev.Data})
		inst, _ := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err := evSchema.Validate(inst); err != nil {
			t.Errorf("event: %s\n%v", raw, err)
		}
		if ev.Subject != "settings:backup" || ev.Actor != "user:admin" {
			t.Errorf("event: %s", raw)
		}
		d, _ := json.Marshal(ev.Data)
		got = append(got, string(d))
	}
	want := []string{
		`{"new":{"keep":10,"time":"02:30"},"old":{"keep":7,"time":"03:00"}}`,
		`{"new":{"time":"03:00"},"old":{"time":"02:30"}}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Without a Proxmox view (serve --dev) a person gets a view with null
// values and acl unknown, which matches the contract.
func TestSettingsWithoutProxmox(t *testing.T) {
	k := loadContract(t)
	e := newEnv(t, true)
	adm := e.admin()
	rec := adm.raw("GET", "/api/v1/settings", nil)
	k.check(t, recorded{"GET", "/settings", rec.Code, "application/json", rec.Body.Bytes()})
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(`"acl":"unknown"`)) || !bytes.Contains(rec.Body.Bytes(), []byte(`"acl_lines":[]`)) {
		t.Errorf("dev view: %d %s", rec.Code, rec.Body)
	}
}
