package store

import (
	"context"
	"testing"
)

// 0028 rebuilds plan for the origin restore (T-222): old rows stay, the
// new origins are accepted, the other checks stay.
func TestMigrationPlanOriginRestore(t *testing.T) {
	v := migrationNumber(t, "_plan_origin_restore.sql")
	path := openAt(t, v-1,
		`INSERT INTO plan (id, origin, proposal, changes, risk, status, reason, author, created_at, human_only, images)
		 VALUES (7, 'build', '{}', '[]', 'reversible', 'approved', 'old', 'system', '2026-11-01T00:00:00Z', '["image-recipe"]', '[]')`,
		// Rows that refer to plan 7 (review L2 of #335).
		`INSERT INTO revision (id, author, reason, plan_id, created_at) VALUES (1, 'user:admin', 'r', 7, '2026-11-01T00:00:00Z')`,
		`INSERT INTO run (id, plan_id, revision_id, status, started_at) VALUES (1, 7, 1, 'succeeded', '2026-11-01T00:00:00Z')`,
		`INSERT INTO issue (id, target, resource, status, changes, opened_at, updated_at, plan_id) VALUES (1, 'web1', 'file:/etc/motd', 'resolving', '[]', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z', 7)`)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.GetPlan(context.Background(), 7)
	if err != nil || p.Origin != "build" || p.Reason != "old" || len(p.HumanOnly) != 1 || p.HumanOnly[0] != "image-recipe" {
		t.Fatalf("old plan: %+v %v", p, err)
	}
	for _, q := range []string{`SELECT plan_id FROM revision WHERE id = 1`, `SELECT plan_id FROM run WHERE id = 1`, `SELECT plan_id FROM issue WHERE id = 1`} {
		var id int64
		if err := s.db.QueryRow(q).Scan(&id); err != nil || id != 7 {
			t.Fatalf("%s: %d %v", q, id, err)
		}
	}
	if rows, err := s.db.Query(`PRAGMA foreign_key_check`); err != nil || rows.Next() {
		t.Fatalf("foreign keys broken after 0028: %v", err)
	} else {
		rows.Close()
	}
	for _, o := range []string{"restore", "repair", "adopt"} {
		if _, err := s.CreatePlan(context.Background(), Plan{Origin: o, Proposal: []byte(`{}`), Changes: []byte(`[]`),
			Images: []byte(`[]`), Risk: "disruptive", Reason: "r", Author: "user:admin", HumanOnly: []string{"guest-restore"}}); err != nil {
			t.Fatalf("%s plan: %v", o, err)
		}
	}
	for _, bad := range []string{
		`INSERT INTO plan (origin, proposal, changes, risk, status, reason, author, created_at) VALUES ('other', '{}', '[]', 'read', 'pending', '', 'x', 'now')`,
		`INSERT INTO plan (origin, proposal, changes, risk, status, reason, author, created_at) VALUES ('rollback', '{}', '[]', 'read', 'pending', '', 'x', 'now')`,
		`INSERT INTO plan (origin, proposal, changes, risk, status, reason, author, created_at, images) VALUES ('proposal', '{}', '[]', 'read', 'pending', '', 'x', 'now', '{}')`,
	} {
		if _, err := s.db.Exec(bad); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
}
