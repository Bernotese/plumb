package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

const exportPass = "correct horse battery"

// exportEnv is a controller with one target that uses the secret app-db.
func exportEnv(t *testing.T, value string) (*env, *client) {
	t.Helper()
	e := newEnv(t, true)
	adm := e.admin()
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": value})
	e.agent("web1")
	_, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "app",
		"put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), appTarget("web1", "app-db")}})
	if code, b := adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil); code != 202 {
		t.Fatalf("seed: %d %s", code, b)
	}
	e.eng.Wait()
	return e, adm
}

func (e *env) export(c *client, pass string) (int, string, string) {
	e.t.Helper()
	method, body := "GET", any(nil)
	if pass != "" {
		method, body = "POST", map[string]string{"passphrase": pass}
	}
	r := c.raw(method, "/api/v1/export", body)
	return r.Code, r.Header().Get("Cache-Control"), r.Body.String()
}

func (e *env) importFile(c *client, req map[string]any) (int, []byte) {
	if req["reason"] == nil {
		req["reason"] = "restore"
	}
	return c.do("POST", "/api/v1/import", req)
}

func TestExportImportRoundTrip(t *testing.T) {
	var logs bytes.Buffer
	envLog = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	defer func() { envLog = quiet }()
	src, adm := exportEnv(t, "export-canary-1")

	code, cache, plain := src.export(adm, "")
	if code != 200 || cache != "no-store" || !strings.Contains(plain, "secrets: [app-db]") || strings.Contains(plain, "export-canary-1") ||
		strings.Contains(plain, "SecretBundle") {
		t.Fatalf("GET /export: %d %s\n%s", code, cache, plain)
	}
	if code, _, _ := src.export(adm, "short"); code != 422 {
		t.Fatalf("short passphrase: %d", code)
	}
	code, cache, withBundle := src.export(adm, exportPass)
	if code != 200 || cache != "no-store" || !strings.Contains(withBundle, "kind: SecretBundle") || strings.Contains(withBundle, "export-canary-1") {
		t.Fatalf("POST /export: %d\n%s", code, withBundle)
	}
	// A token cannot export the secrets.
	if code, _, _ := src.export(src.token(TierApply, wire.RiskDisruptive), exportPass); code != 403 {
		t.Fatalf("token export: %d", code)
	}

	// A fresh controller: import with the bundle, approve, same documents.
	dst := newEnv(t, true)
	dadm := dst.admin()
	dst.agent("web1")
	code, body := dst.importFile(dadm, map[string]any{"content": withBundle, "passphrase": exportPass})
	if code != 201 || !strings.Contains(string(body), `"origin":"import"`) || !strings.Contains(string(body), `The import created secret \"app-db\".`) {
		t.Fatalf("import: %d %s", code, body)
	}
	if !strings.Contains(string(body), `"human_only":["secret-binding"]`) {
		t.Fatalf("an import that binds a secret is human-only: %s", body)
	}
	if code, b := dst.token(TierApply, wire.RiskDisruptive).do("POST", "/api/v1/plans/"+dst.planID(body)+"/approve", nil); code != 403 || problemType(b) != "human-required" {
		t.Fatalf("token approves import: %d %s", code, b)
	}
	if code, b := dadm.do("POST", "/api/v1/plans/"+dst.planID(body)+"/approve", nil); code != 202 {
		t.Fatalf("approve import: %d %s", code, b)
	}
	dst.eng.Wait()
	sh, _ := src.st.Head(src.ctx)
	dh, _ := dst.st.Head(dst.ctx)
	want, got := src.hashes(sh.ID), dst.hashes(dh.ID)
	if len(want) != len(got) {
		t.Fatalf("documents %v, want %v", got, want)
	}
	for k, h := range want {
		if got[k] != h {
			t.Errorf("%s: %s, want %s", k, got[k], h)
		}
	}
	vals, err := dst.sec.Resolve(dst.ctx, []string{"app-db"})
	if err != nil || vals["app-db"].Text != "export-canary-1" {
		t.Fatalf("secret after import: %v", err)
	}
	dst.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD=export-canary-1\n")
	// The export of the copy is the same file, except the time and the revision.
	_, _, again := dst.export(dadm, "")
	if strip(again) != strip(plain) {
		t.Errorf("export of the copy differs:\n%s\n---\n%s", again, plain)
	}

	// Import again: the same value is skipped, no new version.
	v1, _ := dst.sec.Versions(dst.ctx, []string{"app-db"})
	code, body = dst.importFile(dadm, map[string]any{"content": withBundle, "passphrase": exportPass, "mode": "merge"})
	v2, _ := dst.sec.Versions(dst.ctx, []string{"app-db"})
	if code != 201 || v1["app-db"] != v2["app-db"] || strings.Contains(string(body), "created secret") {
		t.Fatalf("second import: %d %s (%v %v)", code, body, v1, v2)
	}
	for _, e := range []*env{src, dst} {
		e.noValue(&logs, "export-canary-1")
		if strings.Contains(logs.String(), exportPass) {
			t.Error("the log holds the passphrase")
		}
		for _, b := range e.seen {
			if bytes.Contains(b, []byte(exportPass)) {
				t.Error("an answer holds the passphrase")
			}
		}
	}
}

// strip removes the header lines that change between two exports.
func strip(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, "exported_at:") && !strings.HasPrefix(l, "revision:") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestImportModes(t *testing.T) {
	src, adm := exportEnv(t, "v")
	_, _, file := src.export(adm, "")
	// The destination has an extra Block; replace deletes it, merge keeps it.
	for _, mode := range []string{"replace", "merge"} {
		dst := newEnv(t, true)
		dadm := dst.admin()
		dst.seed(dadm, "web9")
		dadm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "v"})
		code, body := dst.importFile(dadm, map[string]any{"content": file, "mode": mode})
		if code != 201 {
			t.Fatalf("%s: %d %s", mode, code, body)
		}
		var p struct{ Proposal json.RawMessage }
		pl, _ := dst.st.GetPlan(dst.ctx, mustID(t, body))
		p.Proposal = pl.Proposal
		deletes := strings.Contains(string(p.Proposal), `"delete"`)
		if (mode == "replace") != deletes {
			t.Errorf("%s: proposal %s", mode, p.Proposal)
		}
	}
}

func mustID(t *testing.T, body []byte) int64 {
	var v struct{ ID int64 }
	if json.Unmarshal(body, &v) != nil || v.ID == 0 {
		t.Fatalf("no id: %s", body)
	}
	return v.ID
}

func TestImportRefuses(t *testing.T) {
	src, adm := exportEnv(t, "refuse-canary")
	_, _, file := src.export(adm, exportPass)
	dst := newEnv(t, true)
	dadm := dst.admin()
	head := "kind: Export\nformat: 1\nrevision: 1\nexported_at: \"2026-11-10T09:30:00Z\"\nversion: x\nsecrets: []\n"
	bomb := head + "---\nkind: Image\nname: a\nspec:\n  a: &a [x, x, x, x, x, x, x, x, x]\n  b: &b [*a, *a, *a, *a, *a, *a, *a, *a, *a]\n"
	i := strings.Index(file, "data: ")
	truncated := file[:i+20]
	changed := strings.Replace(file, file[i+6:i+12], "AAAAAA", 1)
	for name, c := range map[string]struct {
		req  map[string]any
		code int
	}{
		"9 MiB":           {map[string]any{"content": strings.Repeat("a", 9<<20)}, 400},
		"alias bomb":      {map[string]any{"content": bomb}, 422},
		"format 2":        {map[string]any{"content": strings.Replace(head, "format: 1", "format: 2", 1)}, 422},
		"truncated":       {map[string]any{"content": truncated, "passphrase": exportPass}, 422},
		"changed bundle":  {map[string]any{"content": changed, "passphrase": exportPass}, 422},
		"wrong pass":      {map[string]any{"content": file, "passphrase": exportPass + "x"}, 422},
		"no bundle":       {map[string]any{"content": head, "passphrase": exportPass}, 422},
		"overwrite ghost": {map[string]any{"content": file, "passphrase": exportPass, "overwrite": []string{"ghost"}}, 422},
		"missing secret":  {map[string]any{"content": file}, 422},
		"bad document":    {map[string]any{"content": head + "---\nkind: Target\nname: web1\nspec: {type: vm}\n"}, 422},
		"mode":            {map[string]any{"content": file, "mode": "upsert"}, 422},
	} {
		code, body := dst.importFile(dadm, c.req)
		if code != c.code {
			t.Errorf("%s: %d %s, want %d", name, code, body, c.code)
		}
		if name == "missing secret" && !strings.Contains(string(body), `"secrets":["app-db"]`) {
			t.Errorf("missing secret: %s", body)
		}
		if name == "bad document" && !strings.Contains(string(body), `"pointer":"/content"`) {
			t.Errorf("bad document: %s", body)
		}
	}
	if list, _ := dst.sec.List(dst.ctx); len(list) != 0 {
		t.Fatalf("a refused import wrote secrets: %+v", list)
	}
	if code, _ := dst.token(TierApply, wire.RiskDisruptive).do("POST", "/api/v1/import", map[string]any{"content": file, "reason": "x"}); code != 403 {
		t.Errorf("token import: %d", code)
	}
	dst.noValue(nil, "refuse-canary")
}

// D-095: equal is skipped, another value is a conflict unless overwrite
// names it, a new name is created.
func TestImportSecretConflicts(t *testing.T) {
	src, adm := exportEnv(t, "same")
	adm.do("PUT", "/api/v1/secrets/diff", map[string]string{"value": "file-value"})
	adm.do("PUT", "/api/v1/secrets/newone", map[string]string{"value": "brand-new"})
	_, _, file := src.export(adm, exportPass)

	dst, dadm := exportEnv(t, "same")
	dadm.do("PUT", "/api/v1/secrets/diff", map[string]string{"value": "live-value"})
	// A pending Plan that uses diff.
	_, body := dadm.do("POST", "/api/v1/plans", map[string]any{"reason": "use diff", "put": []state.Doc{appTarget("web1", "diff")}})
	pending := dst.planID(body)
	before, _ := dst.sec.Versions(dst.ctx, []string{"app-db", "diff"})

	code, body := dst.importFile(dadm, map[string]any{"content": file, "passphrase": exportPass, "mode": "merge"})
	if code != 409 || problemType(body) != "secret-conflict" || !strings.Contains(string(body), `"secrets":["diff"]`) {
		t.Fatalf("conflict: %d %s", code, body)
	}
	if v, _ := dst.sec.Versions(dst.ctx, []string{"newone"}); len(v) != 0 {
		t.Fatal("a conflict wrote a secret")
	}
	code, body = dst.importFile(dadm, map[string]any{"content": file, "passphrase": exportPass, "mode": "merge", "overwrite": []string{"diff"}})
	if code != 201 || !strings.Contains(string(body), `replaced the value of secret \"diff\"`) || !strings.Contains(string(body), `created secret \"newone\"`) {
		t.Fatalf("overwrite: %d %s", code, body)
	}
	after, _ := dst.sec.Versions(dst.ctx, []string{"app-db", "diff", "newone"})
	if after["app-db"] != before["app-db"] || after["diff"] == before["diff"] || after["newone"] == 0 {
		t.Fatalf("versions %v → %v", before, after)
	}
	vals, _ := dst.sec.Resolve(dst.ctx, []string{"diff"})
	if vals["diff"].Text != "file-value" {
		t.Fatal("diff was not overwritten")
	}
	if p, _ := dst.st.GetPlan(dst.ctx, mustID(t, []byte(`{"id":`+pending+`}`))); p.Status != "stale" {
		t.Fatalf("the pending Plan that used diff is %s", p.Status)
	}
	// The warnings stay when the Plan is dropped.
	id := dst.planID(body)
	dadm.do("DELETE", "/api/v1/plans/"+id, nil)
	if _, b := dadm.do("GET", "/api/v1/plans/"+id, nil); !strings.Contains(string(b), `created secret \"newone\"`) {
		t.Errorf("warnings after drop: %s", b)
	}
	dst.noValue(nil, "file-value", "live-value", "brand-new")
}

// An import that brings a Rule is human-only (D-099).
func TestImportRuleHumanOnly(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	_, _, file := e.export(adm, "")
	rule := "---\nkind: Rule\nname: ignore-motd\nspec: {action: ignore, target: web1, resource: \"file:/etc/motd\", attrs: [content]}\n"
	code, body := e.importFile(adm, map[string]any{"content": file + rule, "mode": "merge"})
	if code != 201 || !strings.Contains(string(body), `"human_only":["rule-change"]`) {
		t.Fatalf("rule import: %d %s", code, body)
	}
	if c, b := e.token(TierApply, wire.RiskDisruptive).do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil); c != 403 || problemType(b) != "human-required" {
		t.Fatalf("token approves a rule import: %d %s", c, b)
	}
}
