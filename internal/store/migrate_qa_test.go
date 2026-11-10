package store

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

// Snapshot the schema, version and all contents of the populated tables.
func migrationSnapshotQA(t *testing.T, conn *sql.Conn) []string {
	t.Helper()
	out := []string{}
	for _, query := range []string{
		`PRAGMA user_version`,
		`SELECT group_concat(sql, char(10)) FROM (SELECT sql FROM sqlite_schema ORDER BY name)`,
		`SELECT json_group_array(json_array(id, base_revision, origin, issue_id, proposal, changes, risk, status, reason, author, approved_by, created_at, approved_at)) FROM plan`,
		`SELECT json_group_array(json_array(id, author, reason, plan_id, created_at)) FROM revision`,
		`SELECT json_group_array(json_array(id, plan_id, revision_id, status, started_at, finished_at)) FROM run`,
	} {
		var value string
		if err := conn.QueryRowContext(context.Background(), query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		out = append(out, value)
	}
	return out
}

func TestMigration0004FailureIsAtomicQA(t *testing.T) {
	for _, suffix := range []string{
		"; SELECT * FROM missing_table",
		"; INSERT INTO run(plan_id, revision_id, status, started_at) VALUES(999, 999, 'running', 'x')",
	} {
		t.Run(suffix, func(t *testing.T) {
			path := openAt(t, 3,
				`INSERT INTO revision(id, author, reason, created_at) VALUES(1, 'system', 'seed', 'x')`,
				`INSERT INTO plan(id, base_revision, origin, proposal, changes, risk, status, reason, author, created_at) VALUES(1, 1, 'proposal', '{}', '[]', 'read', 'pending', 'keep', 'system', 'x')`)
			db, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ctx := context.Background()
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
				t.Fatal(err)
			}
			before := migrationSnapshotQA(t, conn)
			all, err := migrations()
			if err != nil {
				t.Fatal(err)
			}
			m := all[3]
			m.body += suffix
			if err := migrateOne(ctx, conn, m); err == nil {
				t.Fatal("broken migration succeeded")
			}
			if after := migrationSnapshotQA(t, conn); !reflect.DeepEqual(before, after) {
				t.Fatalf("failed migration changed database\nbefore: %v\nafter: %v", before, after)
			}
		})
	}
}
