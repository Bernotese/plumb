package store

import (
	"errors"
	"testing"

	"modernc.org/sqlite"
	"modernc.org/sqlite/lib"
)

// Revisions share unchanged documents, but deleting either side must not
// leave a revision with a dangling document reference (ARCHITECTURE §3).
func TestRevisionDocumentSharing(t *testing.T) {
	db := openMigrated(t)
	for _, query := range []string{
		`INSERT INTO doc (hash, kind, name, body) VALUES ('shared', 'Block', 'motd', '{}')`,
		`INSERT INTO revision (id, author, reason, created_at) VALUES (1, 'system', 'seed', '2026-11-10T00:00:00Z')`,
		`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (1, 'Block', 'motd', 'shared')`,
		`INSERT INTO revision (id, author, reason, created_at) VALUES (2, 'system', 'next', '2026-11-10T00:00:01Z')`,
		`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (2, 'Block', 'motd', 'shared')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM doc`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stored %d documents, want one shared document", count)
	}
	for _, tt := range []struct {
		query string
		want  int
	}{
		// Migration 0002 makes revisions immutable; its triggers fire first.
		{`DELETE FROM doc WHERE hash = 'shared'`, sqlite3.SQLITE_CONSTRAINT_TRIGGER},
		{`DELETE FROM revision WHERE id = 1`, sqlite3.SQLITE_CONSTRAINT_TRIGGER},
		{`UPDATE doc SET body = '{"x":1}' WHERE hash = 'shared'`, sqlite3.SQLITE_CONSTRAINT_TRIGGER},
		{`UPDATE revision_doc SET hash = 'other' WHERE revision_id = 2`, sqlite3.SQLITE_CONSTRAINT_TRIGGER},
		{`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (1, 'Block', 'other', 'shared')`, sqlite3.SQLITE_CONSTRAINT_TRIGGER},
		{`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (2, 'Block', 'motd', 'shared')`, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY},
		{`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (2, 'Block', 'x', 'missing')`, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY},
	} {
		_, err := db.Exec(tt.query)
		var constraint *sqlite.Error
		if !errors.As(err, &constraint) || constraint.Code() != tt.want {
			t.Errorf("%s: got %v, want SQLite error code %d", tt.query, err, tt.want)
		}
	}
}

// Removing the newest event must not let a future event reuse its SSE id.
func TestEventSequenceIsNotReused(t *testing.T) {
	db := openMigrated(t)
	const insert = `INSERT INTO event (time, type, subject, actor) VALUES ('2026-11-10T00:00:00Z', 'test', 'target:web1', 'system')`
	first, err := db.Exec(insert)
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := first.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM event WHERE seq = ?`, firstID); err != nil {
		t.Fatal(err)
	}
	second, err := db.Exec(insert)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := second.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if secondID <= firstID {
		t.Fatalf("event id %d reused or preceded deleted id %d", secondID, firstID)
	}
}
