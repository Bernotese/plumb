package store

import (
	"context"
	"testing"
)

// 0026 marks the resolved Issues of Targets that the head no longer holds
// (deleted before the upgrade, low of #324); a Target in the head and an
// open Issue keep 0.
func TestMigrationIssueTargetDeleted(t *testing.T) {
	v := migrationNumber(t, "_issue_target_deleted.sql")
	now := "2026-11-14T10:00:00Z"
	issue := func(id int, target, status string) string {
		res := "NULL, NULL, NULL"
		if status == "resolved" {
			res = "'gone', 'system', '" + now + "'"
		}
		return `INSERT INTO issue (id, target, resource, status, changes, resolution, resolved_by, resolved_at, opened_at, updated_at)
			VALUES (` + string(rune('0'+id)) + `, '` + target + `', 'guest:addresses', '` + status + `', '[]', ` + res + `, '` + now + `', '` + now + `')`
	}
	path := openAt(t, v-1,
		`INSERT INTO doc (hash, kind, name, body) VALUES ('h1', 'Target', 'live', '{}'), ('h2', 'Target', 'gone', '{}')`,
		`INSERT INTO revision (id, author, reason, created_at) VALUES (1, 'system', 'seed', '`+now+`')`,
		`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (1, 'Target', 'live', 'h1'), (1, 'Target', 'gone', 'h2')`,
		`INSERT INTO revision (id, author, reason, created_at) VALUES (2, 'user:admin', 'delete', '`+now+`')`,
		`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (2, 'Target', 'live', 'h1')`,
		issue(1, "live", "resolved"), issue(2, "gone", "resolved"), issue(3, "gone", "open"))
	ctx := context.Background()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := map[int]int{1: 0, 2: 1, 3: 0}
	for id, w := range want {
		var got int
		if err := s.db.QueryRowContext(ctx, `SELECT target_deleted FROM issue WHERE id = ?`, id).Scan(&got); err != nil || got != w {
			t.Errorf("issue %d: target_deleted %d (%v), want %d", id, got, err, w)
		}
	}
}
