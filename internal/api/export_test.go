package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
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
		beforeDB := qaImportDB(t, dst)
		code, body := dst.importFile(dadm, c.req)
		if qaImportDB(t, dst) != beforeDB {
			t.Errorf("%s: refused import changed database content", name)
		}
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

	beforeDB := qaImportDB(t, dst)
	code, body := dst.importFile(dadm, map[string]any{"content": file, "passphrase": exportPass, "mode": "merge"})
	if code != 409 || problemType(body) != "secret-conflict" || !strings.Contains(string(body), `"secrets":["diff"]`) {
		t.Fatalf("conflict: %d %s", code, body)
	}
	if qaImportDB(t, dst) != beforeDB {
		t.Fatal("secret conflict changed database content")
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

// storeState is every secret with its version and value.
func (e *env) storeState() map[string]string {
	e.t.Helper()
	list, err := e.sec.List(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	names := make([]string, len(list))
	for i, in := range list {
		names[i] = in.Name
	}
	vals, err := e.sec.Resolve(e.ctx, names)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]string{}
	for n, v := range vals {
		out[n] = itoa(v.Version) + ":" + v.Text
	}
	return out
}

// Review T-033 must: an import that Propose refuses writes no secret,
// neither a created nor an overwritten one (D-153).
func TestImportRefusedWritesNothing(t *testing.T) {
	e, adm := exportEnv(t, "file-value")
	_, _, file := e.export(adm, exportPass)
	// A smaller disk: refused at save, a disk cannot shrink (D-299).
	createOnly := strings.Replace(file, "disk: 10", "disk: 5", 1)
	if createOnly == file {
		t.Fatal("the test file has no disk: 10")
	}
	for _, c := range []struct {
		name  string
		setup func()
		req   map[string]any
	}{
		{"created", func() {
			e.sec.SetUsage(nil)
			if err := e.sec.Delete(e.ctx, "app-db"); err != nil {
				t.Fatal(err)
			}
		}, map[string]any{"content": createOnly, "passphrase": exportPass}},
		{"overwritten", func() {
			adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "live-value"})
		}, map[string]any{"content": createOnly, "passphrase": exportPass, "overwrite": []string{"app-db"}}},
	} {
		c.setup()
		before := e.storeState()
		last, _ := e.st.LastEventSeq(e.ctx)
		code, body := e.importFile(adm, c.req)
		if code != 422 || !strings.Contains(string(body), "cannot shrink") {
			t.Fatalf("%s: %d %s", c.name, code, body)
		}
		if after := e.storeState(); len(after) != len(before) || after["app-db"] != before["app-db"] {
			t.Fatalf("%s: the store changed: %v → %v", c.name, before, after)
		}
		evs, _ := e.st.EventsAfter(e.ctx, last, 100)
		for _, ev := range evs {
			if strings.HasPrefix(ev.Type, "secret.") {
				t.Fatalf("%s: a refused import emitted %s", c.name, ev.Type)
			}
		}
	}
}

// D-153 (b): a value that the import writes reaches no guest before the
// approval; the Plan records the written version.
func TestImportValueNotObserved(t *testing.T) {
	e, adm := exportEnv(t, "file-value")
	_, _, file := e.export(adm, exportPass)
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "live-value"})
	changed := strings.Replace(file, "cores: 2", "cores: 3", 1)
	a := e.agents["web1"]
	skip := len(a.Bodies())
	code, body := e.importFile(adm, map[string]any{"content": changed, "passphrase": exportPass, "overwrite": []string{"app-db"}})
	if code != 201 {
		t.Fatalf("import: %d %s", code, body)
	}
	if bodiesHold(a, skip, "file-value") {
		t.Fatal("the imported value reached the guest before the approval")
	}
	var p planShape
	json.Unmarshal(body, &p)
	if u, _, ok := p.resource("file:/etc/app.env"); !ok || !u {
		t.Fatalf("the resource with the imported value is observed: %s", body)
	}
	v, _ := e.sec.Versions(e.ctx, []string{"app-db"})
	if !strings.Contains(string(body), `{"name":"app-db","version":`+itoa(v["app-db"])+`}`) {
		t.Fatalf("planned version is not the written one (%d): %s", v["app-db"], body)
	}
	if c, b := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); c != 202 {
		t.Fatalf("approve: %d %s", c, b)
	}
	e.eng.Wait()
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD=file-value\n")
}

// commitSame commits the head documents again: the head moves, nothing
// else changes.
func (e *env) commitSame() {
	head, err := e.st.Head(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	rows, err := e.st.RevisionDocs(e.ctx, head.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.st.CommitRevision(e.ctx, store.NewRevision{Author: "system", Reason: "test: move the head", Docs: rows}); err != nil {
		e.t.Fatal(err)
	}
}

// refusedImport runs an import that must fail with code and type and
// checks that no secret and no secret event was written (D-153, D-158).
func (e *env) refusedImport(name string, adm *client, req map[string]any, code int, typ string) {
	e.t.Helper()
	before := e.storeState()
	last, _ := e.st.LastEventSeq(e.ctx)
	c, body := e.importFile(adm, req)
	if c != code || problemType(body) != typ {
		e.t.Fatalf("%s: %d %s, want %d %s", name, c, body, code, typ)
	}
	if after := e.storeState(); len(after) != len(before) || after["app-db"] != before["app-db"] {
		e.t.Fatalf("%s: the store changed: %v → %v", name, before, after)
	}
	evs, _ := e.st.EventsAfter(e.ctx, last, 100)
	for _, ev := range evs {
		if strings.HasPrefix(ev.Type, "secret.") {
			e.t.Fatalf("%s: a refused import emitted %s", name, ev.Type)
		}
	}
}

// D-158 test 1: the head moves during the observe, or a target is
// offline: 409, nothing written, for a created and a replaced secret.
func TestImportLateRefusals(t *testing.T) {
	e, adm := exportEnv(t, "file-value")
	_, _, file := e.export(adm, exportPass)
	touch := strings.Replace(file, "cores: 2", "cores: 3", 1)
	a := e.agents["web1"]
	for _, mode := range []string{"created", "replaced"} {
		req := map[string]any{"content": touch, "passphrase": exportPass}
		if mode == "created" {
			e.sec.SetUsage(nil)
			e.sec.Delete(e.ctx, "app-db")
		} else {
			adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "live-value"})
			req["overwrite"] = []string{"app-db"}
		}
		a.OnCommand(func(c wire.CommandType) {
			if c == wire.CommandObserve {
				e.commitSame()
			}
		})
		e.refusedImport(mode+": head moved", adm, req, 409, "plan-stale")
		a.OnCommand(nil)
	}

	// Offline: a target whose agent never came (its Run timed out).
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "live-value"})
	_, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "web2", "put": []state.Doc{appTarget("web2", "app-db")}})
	adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil)
	e.eng.Wait() // fails after the enroll timeout; the guest exists
	_, _, file = e.export(adm, exportPass)
	i := strings.Index(file, "name: web2")
	offline := file[:i] + strings.Replace(file[i:], "cores: 2", "cores: 3", 1)
	e.refusedImport("replaced: offline", adm, map[string]any{"content": offline, "passphrase": exportPass, "overwrite": []string{"app-db"}}, 409, "target-offline")
	e.sec.SetUsage(nil)
	e.sec.Delete(e.ctx, "app-db")
	e.refusedImport("created: offline", adm, map[string]any{"content": offline, "passphrase": exportPass}, 409, "target-offline")
}

// D-158 test 2: a PUT of a bundle name during the observe is never
// replaced silently: 409 secret-conflict, nothing written.
func TestImportPutDuringObserve(t *testing.T) {
	e, adm := exportEnv(t, "file-value")
	_, _, file := e.export(adm, exportPass)
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "live-value"})
	adm.do("PUT", "/api/v1/secrets/extra", map[string]string{"value": "x"}) // not in the file
	other := e.login("admin", e.password)
	e.agents["web1"].OnCommand(func(c wire.CommandType) {
		if c == wire.CommandObserve {
			other.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "third-value"})
		}
	})
	code, body := e.importFile(adm, map[string]any{"content": strings.Replace(file, "cores: 2", "cores: 3", 1),
		"passphrase": exportPass, "overwrite": []string{"app-db"}})
	e.agents["web1"].OnCommand(nil)
	if code != 409 || problemType(body) != "secret-conflict" {
		t.Fatalf("import: %d %s", code, body)
	}
	vals, _ := e.sec.Resolve(e.ctx, []string{"app-db"})
	if vals["app-db"].Text != "third-value" {
		t.Fatalf("the PUT during the observe was replaced: %q", vals["app-db"].Text)
	}
}

// D-158 test 3: no value of a written secret is in a command body before
// the approval: a new binding on an existing target.
func TestImportNewBindingNotObserved(t *testing.T) {
	src, sadm := exportEnv(t, "src-value")
	_, _, file := src.export(sadm, exportPass)
	dst := newEnv(t, true)
	dadm := dst.admin()
	dst.seed(dadm, "web1")
	a := dst.agents["web1"]
	skip := len(a.Bodies())
	code, body := dst.importFile(dadm, map[string]any{"content": file, "passphrase": exportPass})
	if code != 201 || !strings.Contains(string(body), `"human_only":["secret-binding"]`) {
		t.Fatalf("import: %d %s", code, body)
	}
	if bodiesHold(a, skip, "src-value") {
		t.Fatal("the value of a new binding reached the guest before the approval")
	}
	var p planShape
	json.Unmarshal(body, &p)
	if u, _, ok := p.resource("file:/etc/app.env"); !ok || !u {
		t.Fatalf("plan: %s", body)
	}
}

// D-158 test 4: a replaced secret that another target uses: that target
// is not in the Plan, gets no command, and its pending Plan is stale.
func TestImportReplacedOtherTarget(t *testing.T) {
	e, adm := exportEnv(t, "file-value")
	e.agent("web2")
	_, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "web2", "put": []state.Doc{appTarget("web2", "app-db")}})
	adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil)
	e.eng.Wait()
	e.waitGuest("web2", "file:/etc/app.env", "DB_PASSWORD=file-value\n")
	_, _, file := e.export(adm, exportPass)
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "live-value"})
	_, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "rotate web2", "targets": []string{"web2"}})
	pending := mustID(t, body)
	web2 := e.agents["web2"]
	skip := len(web2.Bodies())
	i := strings.Index(file, "name: web1")
	touch := file[:i] + strings.Replace(file[i:], "cores: 2", "cores: 3", 1)
	code, body := e.importFile(adm, map[string]any{"content": touch, "passphrase": exportPass, "overwrite": []string{"app-db"}, "mode": "merge"})
	if code != 201 || strings.Contains(string(body), `"target":"web2"`) {
		t.Fatalf("import: %d %s", code, body)
	}
	if n := len(web2.Bodies()); n != skip {
		t.Fatalf("web2 got %d commands", n-skip)
	}
	if p, _ := e.st.GetPlan(e.ctx, pending); p.Status != "stale" {
		t.Fatalf("the pending Plan of web2 is %s", p.Status)
	}
	e.waitGuest("web2", "file:/etc/app.env", "DB_PASSWORD=file-value\n")
}
