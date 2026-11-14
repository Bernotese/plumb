package store

import (
	"database/sql"
	"io/fs"
	"regexp"
	"sort"
	"testing"

	_ "modernc.org/sqlite"
)

// openMigrated applies all migrations to a new in-memory database.
func openMigrated(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // one connection, so the in-memory database is shared
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	names, err := fs.Glob(Migrations, "migrations/*.sql")
	if err != nil || len(names) == 0 {
		t.Fatalf("no migrations: %v", err)
	}
	sort.Strings(names)
	namePattern := regexp.MustCompile(`^migrations/\d{4}_[a-z0-9_]+\.sql$`)
	for _, name := range names {
		if !namePattern.MatchString(name) {
			t.Errorf("%s: name must be NNNN_name.sql", name)
		}
		body, err := Migrations.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	return db
}

func TestMigrationsCreateSliceTables(t *testing.T) {
	db := openMigrated(t)
	// The exact list of ARCHITECTURE §3, plus the secret store of
	// migration 0003 (D-087).
	want := []string{
		"agent", "api_token", "backup_run", "binding", "doc", "enroll_token", "event", "foreign_guest", "guest_host_key", "guest_value", "guest_value_flag", "ignored_drift", "image_schedule", "image_version", "issue", "issue_comment", "issue_ignore",
		"key_check", "plan", "plan_guest_data", "repair_key", "repair_report", "restore_state", "revision", "revision_doc", "run", "run_step", "secret", "secret_counter",
		"session", "setting", "user",
	}
	rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if len(got) != len(want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tables = %v, want %v", got, want)
		}
	}
	if _, err := db.Exec("PRAGMA foreign_key_check"); err != nil {
		t.Fatal(err)
	}
}

// The schema itself must refuse the states that the security model forbids.
func TestMigrationConstraints(t *testing.T) {
	const now = "2026-11-10T00:00:00Z"
	tests := []struct {
		name string
		sql  string
		ok   bool
	}{
		{"apply token with max_risk", `INSERT INTO api_token (id, name, hash, tier, max_risk, created_by, created_at) VALUES ('a', 'n', 'h', 'apply', 'disruptive', 'user:admin', '` + now + `')`, true},
		{"read token", `INSERT INTO api_token (id, name, hash, tier, created_by, created_at) VALUES ('b', 'n', 'h', 'read', 'user:admin', '` + now + `')`, true},
		{"apply token without max_risk", `INSERT INTO api_token (id, name, hash, tier, created_by, created_at) VALUES ('c', 'n', 'h', 'apply', 'user:admin', '` + now + `')`, false},
		{"token with destructive max_risk", `INSERT INTO api_token (id, name, hash, tier, max_risk, created_by, created_at) VALUES ('d', 'n', 'h', 'apply', 'destructive', 'user:admin', '` + now + `')`, false},
		{"read token with max_risk", `INSERT INTO api_token (id, name, hash, tier, max_risk, created_by, created_at) VALUES ('e', 'n', 'h', 'read', 'reversible', 'user:admin', '` + now + `')`, false},
		{"admin token", `INSERT INTO api_token (id, name, hash, tier, created_by, created_at) VALUES ('g', 'n', 'h', 'admin', 'user:admin', '` + now + `')`, false},
		{"admin token with max_risk", `INSERT INTO api_token (id, name, hash, tier, max_risk, created_by, created_at) VALUES ('h', 'n', 'h', 'admin', 'disruptive', 'user:admin', '` + now + `')`, false},
		{"token with unknown tier", `INSERT INTO api_token (id, name, hash, tier, created_by, created_at) VALUES ('f', 'n', 'h', 'root', 'user:admin', '` + now + `')`, false},

		{"first open issue", `INSERT INTO issue (target, resource, status, changes, opened_at, updated_at) VALUES ('web1', 'file:/etc/motd', 'open', '[]', '` + now + `', '` + now + `')`, true},
		{"second open issue for the same resource", `INSERT INTO issue (target, resource, status, changes, opened_at, updated_at) VALUES ('web1', 'file:/etc/motd', 'open', '[]', '` + now + `', '` + now + `')`, false},
		{"resolved issue for the same resource", `INSERT INTO issue (target, resource, status, changes, resolution, opened_at, updated_at, resolved_by, resolved_at) VALUES ('web1', 'file:/etc/motd', 'resolved', '[]', 'revert', '` + now + `', '` + now + `', 'user:admin', '` + now + `')`, true},
		{"resolved issue without resolution", `INSERT INTO issue (target, resource, status, changes, opened_at, updated_at) VALUES ('web2', 'file:/etc/motd', 'resolved', '[]', '` + now + `', '` + now + `')`, false},
		{"open issue with resolution", `INSERT INTO issue (target, resource, status, changes, resolution, opened_at, updated_at, resolved_by, resolved_at) VALUES ('web3', 'file:/etc/motd', 'open', '[]', 'gone', '` + now + `', '` + now + `', 'system', '` + now + `')`, false},
		{"issue with unknown resolution", `INSERT INTO issue (target, resource, status, changes, resolution, opened_at, updated_at, resolved_by, resolved_at) VALUES ('web4', 'file:/etc/motd', 'resolved', '[]', 'ignore_rule', '` + now + `', '` + now + `', 'user:admin', '` + now + `')`, false},
		{"comment", `INSERT INTO issue_comment (issue_id, author, body, created_at) VALUES (1, 'token:ci', 'looks like a manual edit', '` + now + `')`, true},
		{"comment on an issue that does not exist", `INSERT INTO issue_comment (issue_id, author, body, created_at) VALUES (999, 'token:ci', 'x', '` + now + `')`, false},

		{"plan with unknown risk", `INSERT INTO plan (origin, proposal, changes, risk, status, reason, author, created_at) VALUES ('proposal', '{}', '[]', 'harmless', 'pending', 'r', 'user:admin', '` + now + `')`, false},
		{"plan with a state of the run", `INSERT INTO plan (origin, proposal, changes, risk, status, reason, author, created_at) VALUES ('proposal', '{}', '[]', 'reversible', 'applied', 'r', 'user:admin', '` + now + `')`, false},
		{"plan", `INSERT INTO plan (origin, proposal, changes, risk, status, reason, author, created_at) VALUES ('proposal', '{}', '[]', 'reversible', 'pending', 'r', 'user:admin', '` + now + `')`, true},
		{"revision of a plan that does not exist", `INSERT INTO revision (author, reason, plan_id, created_at) VALUES ('user:admin', 'r', 999, '` + now + `')`, false},
		{"revision", `INSERT INTO revision (author, reason, plan_id, created_at) VALUES ('user:admin', 'r', 1, '` + now + `')`, true},
		{"revision_doc without doc", `INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (1, 'Target', 'web1', 'missing')`, false},

		{"enroll token", `INSERT INTO enroll_token (target, hash, expires_at, created_at) VALUES ('web1', 'h1', '` + now + `', '` + now + `')`, true},
		{"second enroll token for the same target", `INSERT INTO enroll_token (target, hash, expires_at, created_at) VALUES ('web1', 'h2', '` + now + `', '` + now + `')`, false},
		{"agent", `INSERT INTO agent (id, target, credential_hash, enrolled_at) VALUES ('a1', 'web1', 'h', '` + now + `')`, true},
		{"second agent for the same target", `INSERT INTO agent (id, target, credential_hash, enrolled_at) VALUES ('a2', 'web1', 'h', '` + now + `')`, false},
		{"strict type check", `INSERT INTO user (name, password_hash, must_change_password, created_at) VALUES ('admin', 'x', 'yes', '` + now + `')`, false},
	}
	db := openMigrated(t)
	for _, tt := range tests {
		_, err := db.Exec(tt.sql)
		if tt.ok && err != nil {
			t.Errorf("%s: want ok, got %v", tt.name, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("%s: want an error, got none", tt.name)
		}
	}
}
