package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openAt makes a database with the migrations up to version and returns
// its path. It uses the settings of Open (foreign keys on).
func openAt(t *testing.T, version int, setup ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plumb.db")
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	all, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all[:version] {
		if _, err := db.Exec(m.body); err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		t.Fatal(err)
	}
	for _, s := range setup {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return path
}

func migrationNumber(t *testing.T, suffix string) int {
	t.Helper()
	all, _ := migrations()
	for _, m := range all {
		if strings.HasSuffix(m.name, suffix) {
			return m.version
		}
	}
	t.Fatalf("no migration %s", suffix)
	return 0
}

// The plan origin migration (D-096) rebuilds the plan table. Plans, Runs,
// Issues and the references between them survive, and the new origins
// are allowed.
func TestMigratePlanOrigin(t *testing.T) {
	ctx := context.Background()
	const ts = "'2026-11-10T00:00:00.000000000Z'"
	path := openAt(t, migrationNumber(t, "_plan_origin.sql")-1,
		`INSERT INTO revision (id, author, reason, created_at) VALUES (1, 'system', 'seed', `+ts+`)`,
		`INSERT INTO issue (id, target, resource, status, changes, opened_at, updated_at) VALUES (1, 'web1', 'file:/etc/motd', 'resolving', '[]', `+ts+`, `+ts+`)`,
		`INSERT INTO plan (id, base_revision, origin, proposal, changes, risk, status, reason, author, approved_by, created_at, approved_at)
		 VALUES (1, 1, 'proposal', '{}', '[]', 'reversible', 'approved', 'one', 'user:admin', 'user:admin', `+ts+`, `+ts+`)`,
		`INSERT INTO revision (id, author, reason, plan_id, created_at) VALUES (2, 'user:admin', 'one', 1, `+ts+`)`,
		`INSERT INTO plan (id, base_revision, origin, issue_id, proposal, changes, risk, status, reason, author, created_at)
		 VALUES (2, 2, 'revert', 1, '{"targets":["web1"]}', '[]', 'reversible', 'pending', 'two', 'user:admin', `+ts+`)`,
		`INSERT INTO run (id, plan_id, revision_id, status, started_at) VALUES (1, 1, 2, 'succeeded', `+ts+`)`,
		`UPDATE issue SET plan_id = 2`,
		`INSERT INTO issue_comment (issue_id, author, body, created_at) VALUES (1, 'user:admin', 'hi', `+ts+`)`,
	)
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	p1, err := s.GetPlan(ctx, 1)
	if err != nil || p1.Origin != "proposal" || p1.Status != PlanApproved || p1.ApprovedBy != "user:admin" || p1.Reason != "one" {
		t.Fatalf("plan 1: %+v %v", p1, err)
	}
	p2, err := s.GetPlan(ctx, 2)
	if err != nil || p2.Origin != "revert" || p2.IssueID != 1 || p2.BaseRevision != 2 || string(p2.Proposal) != `{"targets":["web1"]}` {
		t.Fatalf("plan 2: %+v %v", p2, err)
	}
	if run, err := s.GetRun(ctx, 1); err != nil || run.PlanID != 1 {
		t.Errorf("run: %+v %v", run, err)
	}
	if rev, err := s.Revision(ctx, 2); err != nil || rev.PlanID != 1 {
		t.Errorf("revision: %+v %v", rev, err)
	}
	if iss, err := s.GetIssue(ctx, 1); err != nil || iss.PlanID != 2 {
		t.Errorf("issue: %+v %v", iss, err)
	}

	// The references still point at plan and are enforced.
	for _, table := range []string{"revision", "run", "issue"} {
		var schema string
		s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE name = ?`, table).Scan(&schema)
		if !strings.Contains(schema, "REFERENCES plan (id)") {
			t.Errorf("%s lost its reference to plan: %s", table, schema)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO run (plan_id, revision_id, status, started_at) VALUES (99, 2, 'running', `+ts+`)`); err == nil {
		t.Error("a run of a missing plan was accepted: foreign keys are off")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM plan WHERE id = 1`); err == nil {
		t.Error("deleting a referenced plan was accepted")
	}

	// New origins are allowed; unknown ones are not.
	for _, origin := range []string{"rollback", "import", "ignore_rule"} {
		p := Plan{BaseRevision: 2, Origin: origin, Risk: "read", Reason: origin, Author: "user:admin"}
		if origin == "rollback" {
			p.RollbackTo = 1
		}
		created, err := s.CreatePlan(ctx, p)
		if err != nil {
			t.Errorf("origin %s: %v", origin, err)
			continue
		}
		if got, _ := s.GetPlan(ctx, created.ID); got.RollbackTo != p.RollbackTo {
			t.Errorf("origin %s: rollback_to %d", origin, got.RollbackTo)
		}
	}
	// rollback_to belongs to origin rollback only, and must name a revision (D-131).
	for _, p := range []Plan{
		{BaseRevision: 2, Origin: "rollback", Risk: "read", Reason: "no goal", Author: "user:admin"},
		{BaseRevision: 2, Origin: "proposal", RollbackTo: 1, Risk: "read", Reason: "goal without rollback", Author: "user:admin"},
		{BaseRevision: 2, Origin: "rollback", RollbackTo: 99, Risk: "read", Reason: "unknown revision", Author: "user:admin"},
	} {
		if _, err := s.CreatePlan(ctx, p); err == nil {
			t.Errorf("%s was accepted", p.Reason)
		}
	}
	// Migrated rows have no rollback goal.
	if p1, _ := s.GetPlan(ctx, 1); p1.RollbackTo != 0 {
		t.Errorf("migrated plan has rollback_to %d", p1.RollbackTo)
	}
	if _, err := s.CreatePlan(ctx, Plan{BaseRevision: 2, Origin: "magic", Risk: "read", Reason: "x", Author: "user:admin"}); err == nil {
		t.Error("origin magic was accepted")
	}
}

// A migration that leaves a broken reference fails, and the database
// stays at the old version.
func TestMigrateRefusesBrokenReference(t *testing.T) {
	ctx := context.Background()
	path := openAt(t, 2)
	db, _ := sql.Open("sqlite", "file:"+path)
	conn, _ := db.Conn(ctx)
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO run (plan_id, revision_id, status, started_at) VALUES (7, 7, 'running', 'x')`); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	db.Close()
	if s, err := Open(ctx, path); err == nil {
		s.Close()
		t.Fatal("Open migrated a database with a broken reference")
	} else if !strings.Contains(err.Error(), "foreign key") {
		t.Errorf("error: %v", err)
	} else if !strings.Contains(err.Error(), `table "run"`) || !strings.Contains(err.Error(), "rowid") {
		t.Errorf("error lacks broken row: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Errorf("user_version = %d, %v; want 2", version, err)
	}
	var secrets int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='secret'`).Scan(&secrets); err != nil || secrets != 0 {
		t.Errorf("secret table count = %d, %v; want 0", secrets, err)
	}
}

// A slice 1 database (version 2) migrates through the secret store (0003)
// and the plan origins (0004) with the runner of D-115. The guards of
// 0003 work afterwards: the triggers survive foreign keys off.
func TestMigrateSlice1ThroughSecrets(t *testing.T) {
	ctx := context.Background()
	const ts = "'2026-11-10T00:00:00.000000000Z'"
	path := openAt(t, 2,
		`INSERT INTO revision (id, author, reason, created_at) VALUES (1, 'system', 'seed', `+ts+`)`,
		`INSERT INTO plan (id, base_revision, origin, proposal, changes, risk, status, reason, author, created_at)
		 VALUES (1, 1, 'proposal', '{}', '[]', 'read', 'pending', 'one', 'user:admin', `+ts+`)`)
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version int
	s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	all, _ := migrations()
	if version != len(all) || migrationNumber(t, "_secret.sql") != 3 || migrationNumber(t, "_plan_origin.sql") != 4 {
		t.Fatalf("user_version %d, want %d", version, len(all))
	}
	if p, err := s.GetPlan(ctx, 1); err != nil || p.Reason != "one" {
		t.Fatalf("plan: %+v %v", p, err)
	}
	m, err := s.PutSecret(ctx, "a", "user:admin", func(int64) ([]byte, []byte, error) {
		return make([]byte, 12), make([]byte, 17), nil
	})
	if err != nil || m.Version != 1 {
		t.Fatalf("put secret: %+v %v", m, err)
	}
	for _, q := range []string{`UPDATE secret_counter SET value = 0`, `DELETE FROM secret_counter`} {
		if _, err := s.db.ExecContext(ctx, q); err == nil {
			t.Errorf("%s was accepted", q)
		}
	}
	if err := s.SetKeyCheck(ctx, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM key_check`); err == nil {
		t.Error("the key check value was deleted")
	}
	var fk int
	s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk)
	if fk != 1 {
		t.Error("foreign keys are off after the migration")
	}
}

// Migration 0005 (T-032) rebuilds the issue table: Issues, comments and
// the Plans that point at them survive; resolution "ignore" and
// plan.human_only are new.
func TestMigrateIssueIgnore(t *testing.T) {
	ctx := context.Background()
	const ts = "'2026-11-10T00:00:00.000000000Z'"
	path := openAt(t, migrationNumber(t, "_issue_ignore.sql")-1,
		`INSERT INTO issue (id, target, resource, status, changes, opened_at, updated_at) VALUES (3, 'web1', 'file:/open', 'open', '[]', `+ts+`, `+ts+`)`,
		`INSERT INTO revision (id, author, reason, created_at) VALUES (1, 'system', 'seed', `+ts+`)`,
		`INSERT INTO issue (id, target, resource, status, changes, opened_at, updated_at) VALUES (1, 'web1', 'file:/etc/motd', 'resolving', '[]', `+ts+`, `+ts+`)`,
		`INSERT INTO issue (id, target, resource, status, changes, resolution, opened_at, updated_at, resolved_by, resolved_at)
		 VALUES (2, 'web1', 'file:/etc/a', 'resolved', '[]', 'gone', `+ts+`, `+ts+`, 'system', `+ts+`)`,
		`INSERT INTO plan (id, base_revision, origin, issue_id, proposal, changes, risk, status, reason, author, created_at)
		 VALUES (1, 1, 'revert', 1, '{}', '[]', 'reversible', 'pending', 'r', 'user:admin', `+ts+`)`,
		`UPDATE issue SET plan_id = 1 WHERE id = 1`,
		`INSERT INTO issue_comment (issue_id, author, body, created_at) VALUES (1, 'user:admin', 'hi', `+ts+`)`,
	)
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if iss, err := s.GetIssue(ctx, 3); err != nil || iss.Status != IssueOpen || string(iss.Changes) != "[]" {
		t.Fatalf("open issue after migration: %+v %v", iss, err)
	}
	if iss, err := s.GetIssue(ctx, 1); err != nil || iss.Status != IssueResolving || iss.PlanID != 1 {
		t.Fatalf("issue 1: %+v %v", iss, err)
	}
	if iss, err := s.GetIssue(ctx, 2); err != nil || iss.Resolution != "gone" {
		t.Fatalf("issue 2: %+v %v", iss, err)
	}
	if c, err := s.Comments(ctx, 1); err != nil || len(c) != 1 {
		t.Fatalf("comments: %+v %v", c, err)
	}
	if p, err := s.GetPlan(ctx, 1); err != nil || p.IssueID != 1 || len(p.HumanOnly) != 0 {
		t.Fatalf("plan: %+v %v", p, err)
	}
	for _, table := range []string{"plan", "issue_comment"} {
		var schema string
		s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE name = ?`, table).Scan(&schema)
		if !strings.Contains(schema, "REFERENCES issue (id)") {
			t.Errorf("%s lost its reference to issue: %s", table, schema)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_comment (issue_id, author, body, created_at) VALUES (99, 'x', 'y', `+ts+`)`); err == nil {
		t.Error("a comment of a missing issue was accepted")
	}
	// One open Issue per target and resource still holds.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO issue (target, resource, status, changes, opened_at, updated_at) VALUES ('web1', 'file:/etc/motd', 'open', '[]', `+ts+`, `+ts+`)`); err == nil {
		t.Error("the unique open index is gone")
	}
	// New: resolution ignore, with its memory.
	if _, err := s.db.ExecContext(ctx, `UPDATE issue SET status = 'open', plan_id = NULL WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IgnoreIssue(ctx, 1, "user:admin", json.RawMessage(`[]`), json.RawMessage(`[]`)); err != nil {
		t.Fatalf("ignore: %v", err)
	}
	if _, err := s.IgnoredOnce(ctx, "web1", "file:/etc/motd"); err != nil {
		t.Errorf("memory: %v", err)
	}
	p, err := s.CreatePlan(ctx, Plan{BaseRevision: 1, Origin: "ignore_rule", HumanOnly: []string{"rule-change"}, Risk: "read", Reason: "r", Author: "user:admin"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetPlan(ctx, p.ID); len(got.HumanOnly) != 1 || got.HumanOnly[0] != "rule-change" {
		t.Errorf("human_only: %+v", got.HumanOnly)
	}
}

// Migration 0006 (T-055, D-188) removes the Image bindings and keeps the
// Target bindings.
func TestMigrateNoImageBinding(t *testing.T) {
	ctx := context.Background()
	const ts = "'2026-11-11T00:00:00.000000000Z'"
	path := openAt(t, migrationNumber(t, "_no_image_binding.sql")-1,
		`INSERT INTO binding (kind, name, ref, digest, updated_at) VALUES ('Image', 'debian', '9000', '', `+ts+`)`,
		`INSERT INTO binding (kind, name, ref, digest, updated_at) VALUES ('Image', 'ubuntu', '9001', '', `+ts+`)`,
		`INSERT INTO binding (kind, name, ref, digest, updated_at) VALUES ('Target', 'web1', '9002', 'abc', `+ts+`)`,
	)
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if b, err := s.Bindings(ctx, "Image"); err != nil || len(b) != 0 {
		t.Errorf("Image bindings after migration: %+v %v", b, err)
	}
	// 0007 empties the digest (T-056); the row itself stays.
	if b, err := s.GetBinding(ctx, "Target", "web1"); err != nil || b.Ref != "9002" || b.Digest != "" {
		t.Errorf("Target binding after migration: %+v %v", b, err)
	}
}

// Migration 0007 (T-056, D-195) empties every digest, so the first poll
// round compares every guest, adds binding.waiting (default empty) and
// run.waits_for_restart.
func TestMigrateWaiting(t *testing.T) {
	ctx := context.Background()
	const ts = "'2026-11-11T00:00:00.000000000Z'"
	path := openAt(t, migrationNumber(t, "_waiting.sql")-1,
		`INSERT INTO binding (kind, name, ref, digest, updated_at) VALUES ('Target', 'web1', '100', 'abc', `+ts+`)`,
		`INSERT INTO binding (kind, name, ref, digest, updated_at) VALUES ('Target', 'web2', '101', 'def', `+ts+`)`,
	)
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bs, err := s.Bindings(ctx, "Target")
	if err != nil || len(bs) != 2 {
		t.Fatalf("bindings: %+v %v", bs, err)
	}
	for _, b := range bs {
		if b.Digest != "" || b.Waiting != nil {
			t.Errorf("%s after 0007: digest %q, waiting %+v", b.Name, b.Digest, b.Waiting)
		}
	}
	// Round trip of the list.
	since := time.Date(2026, 11, 11, 9, 0, 0, 0, time.FixedZone("x", 3600))
	b := bs[0]
	b.Waiting = []Waiting{{Attr: "memory", From: 1024, To: 2048, Since: since}}
	if err := s.PutBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBinding(ctx, "Target", "web1")
	if err != nil || len(got.Waiting) != 1 || got.Waiting[0].To != 2048 || !got.Waiting[0].Since.Equal(since) {
		t.Errorf("waiting round trip: %+v %v", got, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE binding SET waiting = '{}' WHERE name = 'web1'`); err == nil {
		t.Error("waiting accepts a JSON object")
	}
}

// D-254: 0011 rebuilds image_version so base_sha256 takes a SHA-512 pin
// too. Rows, the index and the triggers survive.
func TestMigrateBasePin(t *testing.T) {
	ctx := context.Background()
	const ts = "'2026-11-12T00:00:00.000000000Z'"
	h64 := strings.Repeat("a", 64)
	// Every row that the code wrote before 0011 is lower-case hex: the
	// store inserts only hex digests (BuildDigest, the pinned checksum).
	// The rows below pass the new CHECK, so the copy keeps them.
	path := openAt(t, migrationNumber(t, "_base_pin.sql")-1,
		`INSERT INTO revision (id, author, reason, created_at) VALUES (1, 'system', 'seed', `+ts+`)`,
		`INSERT INTO plan (id, base_revision, origin, proposal, changes, risk, status, reason, author, approved_by, created_at, approved_at)
		 VALUES (1, 1, 'build', '{}', '[]', 'reversible', 'approved', 'b', 'user:admin', 'user:admin', `+ts+`, `+ts+`)`,
		`INSERT INTO run (id, plan_id, revision_id, status, started_at) VALUES (1, 1, 1, 'succeeded', `+ts+`)`,
		`INSERT INTO image_version (image, version, status, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb, started_at, finished_at)
		 VALUES ('web', 1, 'ready', 'change', 1, 1, '`+h64+`', '`+h64+`', '`+h64+`', 10, `+ts+`, `+ts+`)`,
		`INSERT INTO image_version (image, version, status, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb, started_at)
		 VALUES ('web', 2, 'building', 'manual', 1, 1, '`+h64+`', '`+h64+`', '`+h64+`', 10, `+ts+`)`,
	)
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for v, want := range map[int]string{1: "ready", 2: "building"} {
		got, err := s.GetImageVersion(ctx, "web", v)
		if err != nil || got.Status != want || got.BaseSHA256 != h64 || got.DiskGB != 10 {
			t.Errorf("v%d after 0011: %+v %v", v, got, err)
		}
	}
	// A SHA-512 pin goes in now; 64 and 128 only.
	pin := strings.Repeat("c", 128)
	_, err = s.AddImageVersion(ctx, ImageVersion{Image: "ct", Trigger: "change", RunID: 1, RevisionID: 1, RecipeSHA256: h64, BaseSHA256: pin, DiskGB: 4},
		func(int) string { return h64 })
	if err != nil {
		t.Fatalf("SHA-512 pin: %v", err)
	}
	for i, pin := range []string{h64, strings.Repeat("ab09", 32), strings.Repeat("aB09", 32), "", strings.Repeat("c", 63), strings.Repeat("c", 65), strings.Repeat("c", 100), strings.Repeat("c", 127), strings.Repeat("c", 129), strings.Repeat("g", 64), strings.Repeat("g", 128), strings.Repeat("C", 128), strings.Repeat("a", 63) + " ", strings.Repeat("a", 127) + "\n"} {
		_, err := s.db.ExecContext(ctx, `INSERT INTO image_version (image, version, status, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb, started_at)
            VALUES (?, 1, 'building', 'manual', 1, 1, ?, ?, ?, 1, `+ts+`)`, fmt.Sprintf("pin-%d", i), h64, pin, h64)
		if wantOK := i < 2; (err == nil) != wantOK {
			t.Errorf("base pin case %d (length %d): err=%v, want accepted=%t", i, len(pin), err, wantOK)

		}
	}

	// Index and triggers: one building per Image, no delete, fixed columns.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO image_version (image, version, status, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb, started_at)
		VALUES ('web', 3, 'building', 'manual', 1, 1, ?, ?, ?, 1, `+ts+`)`, h64, h64, h64); err == nil {
		t.Error("two building versions of one Image")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM image_version WHERE image = 'web'`); err == nil {
		t.Error("delete allowed")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE image_version SET disk_gb = 11 WHERE image = 'web' AND version = 1`); err == nil {
		t.Error("fixed column changed")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE image_version SET status = 'building' WHERE image = 'web' AND version = 1`); err == nil {
		t.Error("ready → building allowed")
	}
}

// A database with a non-hex base_sha256 (only possible by hand, 0009
// checked the length only) stops 0011 as a whole: the migration is one
// transaction, so the old table and its row stay.
func TestMigrateBasePinRefusesNonHex(t *testing.T) {
	ctx := context.Background()
	const ts = "'2026-11-12T00:00:00.000000000Z'"
	h64 := strings.Repeat("a", 64)
	path := openAt(t, migrationNumber(t, "_base_pin.sql")-1,
		`INSERT INTO revision (id, author, reason, created_at) VALUES (1, 'system', 'seed', `+ts+`)`,
		`INSERT INTO plan (id, base_revision, origin, proposal, changes, risk, status, reason, author, approved_by, created_at, approved_at)
		 VALUES (1, 1, 'build', '{}', '[]', 'reversible', 'approved', 'b', 'user:admin', 'user:admin', `+ts+`, `+ts+`)`,
		`INSERT INTO run (id, plan_id, revision_id, status, started_at) VALUES (1, 1, 1, 'succeeded', `+ts+`)`,
		`INSERT INTO image_version (image, version, status, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb, started_at, finished_at)
		 VALUES ('web', 1, 'ready', 'change', 1, 1, '`+h64+`', '`+strings.Repeat("Z", 64)+`', '`+h64+`', 10, `+ts+`, `+ts+`)`,
	)
	if s, err := Open(ctx, path); err == nil {
		s.Close()
		t.Fatal("0011 accepted a non-hex base_sha256")
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, rows int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != migrationNumber(t, "_base_pin.sql")-1 {
		t.Errorf("user_version %d %v", version, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM image_version").Scan(&rows); err != nil || rows != 1 {
		t.Errorf("rows %d %v", rows, err)
	}
}
