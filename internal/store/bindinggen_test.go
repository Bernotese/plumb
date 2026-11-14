package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// T-209 (host finding F10): migration 0024 drops the pins and reports of
// deleted Targets and of older guests, tags the rest with the binding
// generation, and changes no other table.
func TestMigrateBindingGen(t *testing.T) {
	ctx := context.Background()
	const ts = "'2026-11-14T00:00:00.000000000Z'"
	rep := `'{"lines":[],"sshd":{"status":"ok","port":22},"host_keys":[]}'`
	path := openAt(t, migrationNumber(t, "_binding_gen.sql")-1,
		`INSERT INTO binding (kind, name, ref, digest, updated_at) VALUES ('Target', 'live', '9101', '', `+ts+`)`,
		`INSERT INTO binding (kind, name, ref, digest, updated_at) VALUES ('Target', 'moved', '9102', '', `+ts+`)`,
		`INSERT INTO binding (kind, name, ref, digest, updated_at) VALUES ('Image', 'ct', 'local:vztmpl/x', '', `+ts+`)`,
		// live: its own pin and report stay.
		`INSERT INTO guest_host_key VALUES ('live', '9101', 'SHA256:live', `+ts+`, 'agent')`,
		`INSERT INTO repair_report VALUES ('live', '9101', `+rep+`, `+ts+`)`,
		// deleted: the host case, the guest 9100 adopted again later.
		`INSERT INTO guest_host_key VALUES ('deleted', '9100', 'SHA256:old', `+ts+`, 'agent')`,
		`INSERT INTO repair_report VALUES ('deleted', '9100', `+rep+`, `+ts+`)`,
		// moved: rows of an older guest of a live name.
		`INSERT INTO guest_host_key VALUES ('moved', '9000', 'SHA256:older', `+ts+`, 'agent')`,
		`INSERT INTO repair_report VALUES ('moved', '9000', `+rep+`, `+ts+`)`,
		`INSERT INTO foreign_guest (target, ref, marked_at) VALUES ('live', '9101', `+ts+`)`,
		`INSERT INTO setting (key, value, changed_by, changed_at) VALUES ('backup.keep', '7', 'user:admin', `+ts+`)`,
	)
	counts := func(db *sql.DB) map[string]int {
		t.Helper()
		rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for rows.Next() {
			var n string
			rows.Scan(&n)
			names = append(names, n)
		}
		rows.Close()
		out := map[string]int{}
		for _, n := range names {
			var c int
			db.QueryRow(`SELECT count(*) FROM "` + n + `"`).Scan(&c)
			out[n] = c
		}
		return out
	}
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	before := counts(raw)
	raw.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after := counts(s.db)
	for name, n := range before {
		want := n
		switch name {
		case "guest_host_key", "repair_report":
			want = 1 // only the live one
		}
		if after[name] != want {
			t.Errorf("%s: %d rows → %d, want %d", name, n, after[name], want)
		}
	}
	live, _ := s.GetBinding(ctx, "Target", "live")
	moved, _ := s.GetBinding(ctx, "Target", "moved")
	img, _ := s.GetBinding(ctx, "Image", "ct")
	if live.Gen == 0 || moved.Gen == 0 || img.Gen == 0 || live.Gen == moved.Gen || live.Gen == img.Gen {
		t.Fatalf("gens %d %d %d", live.Gen, moved.Gen, img.Gen)
	}
	if pins, _ := s.HostKeyPins(ctx, "live", live.GuestKey()); len(pins) != 1 || pins[0] != "SHA256:live" {
		t.Fatalf("live pins %v", pins)
	}
	if r, err := s.RepairReport(ctx, "live"); err != nil || r.GuestID != live.GuestKey() {
		t.Fatalf("live report %+v %v", r, err)
	}
	// The next binding gets a generation above every existing one.
	if err := s.PutBinding(ctx, Binding{Kind: "Target", Name: "new", Ref: "9100"}); err != nil {
		t.Fatal(err)
	}
	nb, _ := s.GetBinding(ctx, "Target", "new")
	if nb.Gen <= max(live.Gen, moved.Gen, img.Gen) {
		t.Fatalf("new gen %d", nb.Gen)
	}
	// The host case: the guest 9100 shows its keys again, under a new name.
	if owners, _ := s.HostKeyOwners(ctx, []string{"SHA256:old"}, "new"); len(owners) != 0 {
		t.Fatalf("a deleted Target still owns the key: %v", owners)
	}
}

func genEnv(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, ctx
}

func pinFor(t *testing.T, s *Store, ctx context.Context, name, ref string, fps ...string) Binding {
	t.Helper()
	if err := s.PutBinding(ctx, Binding{Kind: "Target", Name: name, Ref: ref}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.GetBinding(ctx, "Target", name)
	if ok, err := s.PinFirstHostKeys(ctx, name, b.GuestKey(), fps, time.Now()); err != nil || !ok {
		t.Fatalf("pin %s: %v %v", name, ok, err)
	}
	if err := s.SetRepairReport(ctx, name, RepairReportRow{GuestID: b.GuestKey(), Report: json.RawMessage(`{}`), ReportedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return b
}

// A delete drops the pins and the report with the binding; a new binding
// of the same name and VMID (a reused VMID) starts clean, and so does the
// same guest under a new name.
func TestBindingDeleteDropsPins(t *testing.T) {
	s, ctx := genEnv(t)
	old := pinFor(t, s, ctx, "wiki", "9100", "SHA256:k")
	if err := s.DeleteBinding(ctx, "Target", "wiki"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepairReport(ctx, "wiki"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("report after delete: %v", err)
	}
	if pins, _ := s.HostKeyPins(ctx, "wiki", old.GuestKey()); len(pins) != 0 {
		t.Fatalf("pins after delete %v", pins)
	}
	// Reused name and VMID: a new generation, no pins.
	if err := s.PutBinding(ctx, Binding{Kind: "Target", Name: "wiki", Ref: "9100"}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.GetBinding(ctx, "Target", "wiki")
	if b.Gen == old.Gen {
		t.Fatal("a new binding reused the generation")
	}
	if pins, _ := s.HostKeyPins(ctx, "wiki", b.GuestKey()); len(pins) != 0 {
		t.Fatalf("inherited pins %v", pins)
	}
	// The same guest under another name: its keys are free.
	if owners, _ := s.HostKeyOwners(ctx, []string{"SHA256:k"}, "wiki2"); len(owners) != 0 {
		t.Fatalf("owners %v", owners)
	}
}

// A new ref under a live name (a re-created guest) drops the old pins in
// the same transaction; the same ref keeps them (a digest update).
func TestBindingNewRefDropsPins(t *testing.T) {
	s, ctx := genEnv(t)
	b := pinFor(t, s, ctx, "web1", "101", "SHA256:a")
	b.Digest = "d2"
	if err := s.PutBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	same, _ := s.GetBinding(ctx, "Target", "web1")
	if same.Gen != b.Gen {
		t.Fatal("a digest update changed the generation")
	}
	if pins, _ := s.HostKeyPins(ctx, "web1", same.GuestKey()); len(pins) != 1 {
		t.Fatalf("pins after a digest update %v", pins)
	}
	if err := s.PutBinding(ctx, Binding{Kind: "Target", Name: "web1", Ref: "102"}); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM guest_host_key WHERE target = 'web1'`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d pins of the old guest", n)
	}
	if _, err := s.RepairReport(ctx, "web1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("report of the old guest: %v", err)
	}
}

// A report or pin that raced the delete (guest key read before, written
// after) lands nowhere.
func TestStaleGuestKeyWritesNothing(t *testing.T) {
	s, ctx := genEnv(t)
	if err := s.PutBinding(ctx, Binding{Kind: "Target", Name: "web1", Ref: "101"}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.GetBinding(ctx, "Target", "web1")
	key := b.GuestKey()
	s.DeleteBinding(ctx, "Target", "web1")
	s.PutBinding(ctx, Binding{Kind: "Target", Name: "web1", Ref: "101"}) // same name and VMID again
	if ok, err := s.PinFirstHostKeys(ctx, "web1", key, []string{"SHA256:z"}, time.Now()); err != nil || ok {
		t.Fatalf("stale pin: %v %v", ok, err)
	}
	if err := s.AddHostKeyPin(ctx, "web1", key, "SHA256:z", "x", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale accepted pin: %v", err)
	}
	s.SetRepairReport(ctx, "web1", RepairReportRow{GuestID: key, Report: json.RawMessage(`{}`), ReportedAt: time.Now()})
	var n int
	s.db.QueryRow(`SELECT (SELECT count(*) FROM guest_host_key) + (SELECT count(*) FROM repair_report)`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d stale rows written", n)
	}
}

// host-key-shared still sees a real clone: two live Targets, one key.
func TestHostKeySharedLiveClone(t *testing.T) {
	s, ctx := genEnv(t)
	pinFor(t, s, ctx, "a", "101", "SHA256:same")
	if err := s.PutBinding(ctx, Binding{Kind: "Target", Name: "b", Ref: "102"}); err != nil {
		t.Fatal(err)
	}
	if owners, _ := s.HostKeyOwners(ctx, []string{"SHA256:same"}, "b"); owners["SHA256:same"] != "a" {
		t.Fatalf("clone not seen: %v", owners)
	}
	// A dead row (a guest key of no live binding, e.g. from an old
	// database) never counts.
	if _, err := s.db.Exec(`INSERT INTO guest_host_key VALUES ('gone', '9100@1', 'SHA256:dead', '2026-11-14T00:00:00Z', 'agent')`); err != nil {
		t.Fatal(err)
	}
	if owners, _ := s.HostKeyOwners(ctx, []string{"SHA256:dead"}, "b"); len(owners) != 0 {
		t.Fatalf("a dead pin counts: %v", owners)
	}
}

// T-209 sweep: the marks of a deleted Target go; another Target's stay.
// The value cap flag stays for both (D-397: the copy rule needs it).
func TestForgetTargetMarks(t *testing.T) {
	s, ctx := genEnv(t)
	const ts = "'2026-11-14T00:00:00.000000000Z'"
	for _, q := range []string{
		`INSERT INTO guest_value_flag (target, flagged_at, until) VALUES ('web1', ` + ts + `, '2099-01-01T00:00:00Z'), ('web2', ` + ts + `, '2099-01-01T00:00:00Z')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"web1", "web2"} {
		iss, _, err := s.UpsertIssue(ctx, target, "file:/etc/motd", json.RawMessage(`[]`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO issue_ignore (target, resource, changes, issue_id, created_at) VALUES (?, 'file:/etc/motd', '[]', ?, `+ts+`)`, target, iss.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO ignored_drift (target, resource, rule, attrs, last_seen) VALUES (?, 'file:/etc/motd', 'r', '[]', `+ts+`)`, target); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.ForgetTargetMarks(ctx, "web1"); err != nil || n != 2 {
		t.Fatalf("dropped %d marks, %v; want 2", n, err)
	}
	var flags int
	s.db.QueryRow(`SELECT count(*) FROM guest_value_flag`).Scan(&flags)
	if flags != 2 {
		t.Errorf("value cap flags: %d, want 2 (kept)", flags)
	}
	for _, table := range []string{"issue_ignore", "ignored_drift"} {
		var w1, w2 int
		s.db.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE target = 'web1'`).Scan(&w1)
		s.db.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE target = 'web2'`).Scan(&w2)
		if w1 != 0 || w2 != 1 {
			t.Errorf("%s: web1 %d, web2 %d", table, w1, w2)
		}
	}
}
