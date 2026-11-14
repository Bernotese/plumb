package store

import (
	"context"
	"testing"
)

// 0019 rebuilds plan for origin pin_refresh: old rows stay, the new origin
// is accepted, the other checks stay.
func TestMigrationPinRefresh(t *testing.T) {
	v := migrationNumber(t, "_pin_refresh.sql")
	path := openAt(t, v-1,
		`INSERT INTO plan (id, origin, proposal, changes, risk, status, reason, author, created_at, human_only, images)
		 VALUES (7, 'build', '{}', '[]', 'reversible', 'approved', 'old', 'system', '2026-11-01T00:00:00Z', '["image-recipe"]', '[]')`)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.GetPlan(context.Background(), 7)
	if err != nil || p.Origin != "build" || p.Reason != "old" || len(p.HumanOnly) != 1 || p.HumanOnly[0] != "image-recipe" {
		t.Fatalf("old plan: %+v %v", p, err)
	}
	if _, err := s.CreatePlan(context.Background(), Plan{Origin: "pin_refresh", Proposal: []byte(`{}`), Changes: []byte(`[]`),
		Images: []byte(`[]`), Risk: "reversible", Reason: "r", Author: "system"}); err != nil {
		t.Fatalf("pin_refresh plan: %v", err)
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
