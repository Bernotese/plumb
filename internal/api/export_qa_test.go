package api

import (
	"bytes"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/secret"
)

// Compare logical SQLite content, including ciphertext, counters and events.
// Only agent.last_seen_at is left out (see below).
func qaImportDB(t *testing.T, e *env, tables ...string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(e.dir, "plumb.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(tables) == 0 {
		rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			tables = append(tables, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	var out []string
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for i, col := range cols {
				// agent.last_seen_at is the link heartbeat: every poll of a
				// running agent writes it (link.handlePoll → TouchAgent),
				// independent of any API request. Not import content (T-045).
				if table == "agent" && col == "last_seen_at" {
					vals[i] = nil
				}
			}
			raw, err := json.Marshal(vals)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, table+":"+string(raw))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func TestQAImportRejectedDBUnchanged(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	if _, err := e.sec.Put(e.ctx, "live", "qa-live-canary", "user:admin"); err != nil {
		t.Fatal(err)
	}
	b, err := secret.SealBundle(map[string]string{"live": "replacement", "new": "new-canary"}, exportPass, secret.BundleKDF)
	if err != nil {
		t.Fatal(err)
	}
	head := "kind: Export\nformat: 1\nsecrets: []\n"
	bundle := func(b secret.Bundle) string { raw, _ := json.Marshal(b); return head + "---\n" + string(raw) }
	file := bundle(b)
	type refusal struct {
		content, pass string
		code          int
	}
	cases := map[string]refusal{
		"duplicate":    {head + "format: 1\n", "", 422},
		"deep nesting": {head + "x: " + strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001), "", 422},
		"10 MiB":       {strings.Repeat("x", 10<<20), "", 400},
	}
	for _, kind := range []string{"User", "Token", "Session"} {
		cases[kind] = refusal{head + "---\nkind: " + kind + "\nname: injected\nspec: {}\n", "", 422}
	}
	for _, field := range []string{"time", "memory", "threads"} {
		bad := b
		switch field {
		case "time":
			bad.KDF.Time = 0
		case "memory":
			bad.KDF.MemoryKiB = 0
		case "threads":
			bad.KDF.Threads = 0
		}
		cases["zero "+field] = refusal{bundle(bad), exportPass, 422}
	}
	bad := b
	bad.KDF.MemoryKiB = 4 << 20
	cases["4 GiB"] = refusal{bundle(bad), exportPass, 422}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			before := qaImportDB(t, e)
			code, body := e.importFile(adm, map[string]any{"content": c.content, "passphrase": c.pass})
			if code != c.code {
				t.Fatalf("status %d, want %d: %s", code, c.code, body)
			}
			if qaImportDB(t, e) != before {
				t.Fatal("refused import changed database content")
			}
		})
	}
	// Full slots prove that invalid KDF parameters are rejected before Argon2 admission.
	for i := 0; i < cap(e.srv.hashSlots); i++ {
		e.srv.hashSlots <- struct{}{}
	}
	defer func() {
		for len(e.srv.hashSlots) > 0 {
			<-e.srv.hashSlots
		}
	}()
	if code, _, _ := e.export(adm, exportPass); code != 429 {
		t.Fatalf("export with full slots: %d", code)
	}
	for _, c := range []struct {
		file string
		want int
	}{{file, 429}, {cases["4 GiB"].content, 422}, {cases["zero time"].content, 422}} {
		before := qaImportDB(t, e)
		code, _ := e.importFile(adm, map[string]any{"content": c.file, "passphrase": exportPass})
		if code != c.want || before != qaImportDB(t, e) {
			t.Fatalf("slots: status %d, want %d, or database changed", code, c.want)
		}
	}
}

func TestQAImportApprovalBoundaryAndExactRoundTrip(t *testing.T) {
	src := newEnv(t, true)
	adm := src.admin()
	if code, _, _ := src.export(adm, ""); code != 404 {
		t.Fatalf("empty export: %d", code)
	}
	src.seed(adm, "web1")
	if _, err := src.sec.Put(src.ctx, "canary", "qa-export-value-canary", "user:admin"); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	src.srv.now = func() time.Time { return fixed }
	rec := adm.raw("GET", "/api/v1/export", nil)
	if rec.Header().Get("Content-Disposition") != `attachment; filename="plumb-export-r1.yaml"` {
		t.Fatal("export attachment header")
	}
	file := rec.Body.String()
	master, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ReplaceAll(src.key.Recovery(), "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{string(master[:secret.KeySize]), base64.StdEncoding.EncodeToString(master[:secret.KeySize]), "qa-export-value-canary", src.key.Recovery(), string(src.key.CheckValue()), base64.StdEncoding.EncodeToString(src.key.CheckValue())} {
		if bytes.Contains([]byte(file), []byte(canary)) {
			t.Fatal("export leaked value or key material")
		}
	}
	dst := newEnv(t, true)
	dadm := dst.admin()
	dst.srv.now = src.srv.now
	dst.agent("web1")
	if _, err := dst.sec.Put(dst.ctx, "canary", "different", "user:admin"); err != nil {
		t.Fatal(err)
	}
	tables := []string{"doc", "revision", "revision_doc", "binding", "run", "run_step", "secret", "secret_counter", "user", "session", "api_token"}
	before := qaImportDB(t, dst, tables...)
	code, body := dst.importFile(dadm, map[string]any{"content": file})
	if code != 201 {
		t.Fatalf("import %d: %s", code, body)
	}
	if before != qaImportDB(t, dst, tables...) {
		t.Fatal("pending import changed state before approval")
	}
	if code, body = dadm.do("POST", "/api/v1/plans/"+dst.planID(body)+"/approve", nil); code != 202 {
		t.Fatalf("approve %d: %s", code, body)
	}
	dst.eng.Wait()
	_, _, again := dst.export(dadm, "")
	if file != again {
		t.Fatal("fresh-store round trip is not byte equal with a fixed export time")
	}
}
