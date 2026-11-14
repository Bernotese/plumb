package store

import (
	"context"
	"testing"
)

// 0021 rebuilds plan for the origins repair and adopt: old rows stay, the
// new origins are accepted, the other checks stay.
func TestMigrationPlanOriginRepair(t *testing.T) {
	v := migrationNumber(t, "_plan_origin_repair.sql")
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
	for _, o := range []string{"repair", "adopt", "pin_refresh"} {
		if _, err := s.CreatePlan(context.Background(), Plan{Origin: o, Proposal: []byte(`{}`), Changes: []byte(`[]`),
			Images: []byte(`[]`), Risk: "disruptive", Reason: "r", Author: "user:admin", HumanOnly: []string{"repair"}}); err != nil {
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
