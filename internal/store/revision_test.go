package store

import "testing"

// Revisions share unchanged documents, but deleting either side must not
// leave a revision with a dangling document reference (ARCHITECTURE §3).
func TestRevisionDocumentSharing(t *testing.T) {
	db := openMigrated(t)
	for _, query := range []string{
		`INSERT INTO doc (hash, kind, name, body) VALUES ('shared', 'Block', 'motd', '{}')`,
		`INSERT INTO revision (id, author, reason, created_at) VALUES (1, 'system', 'seed', '2026-11-10T00:00:00Z')`,
		`INSERT INTO revision (id, author, reason, created_at) VALUES (2, 'system', 'next', '2026-11-10T00:00:01Z')`,
		`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (1, 'Block', 'motd', 'shared')`,
		`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (2, 'Block', 'motd', 'shared')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM revision_doc JOIN doc USING (hash)`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("shared document appears in %d revisions, want 2", count)
	}
	for _, query := range []string{
		`DELETE FROM doc WHERE hash = 'shared'`,
		`DELETE FROM revision WHERE id = 1`,
		`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (1, 'Block', 'motd', 'shared')`,
		`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (999, 'Block', 'motd', 'shared')`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Errorf("expected constraint failure: %s", query)
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
