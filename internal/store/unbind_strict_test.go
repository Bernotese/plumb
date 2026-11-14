package store

import (
	"context"
	"reflect"
	"testing"
)

// 0025 (T-227, ROADMAP row 29) rebuilds unbind_pending as STRICT and
// keeps every row: a parked and a forgotten one.
func TestMigrationUnbindPendingStrict(t *testing.T) {
	v := migrationNumber(t, "_unbind_pending_strict.sql")
	path := openAt(t, v-1,
		`INSERT INTO unbind_pending (target, agent_id, guest_ref, node, since, key_ids, forgotten_at)
		 VALUES ('wiki', 'agent-a', '9100', 'pve', '2026-11-14T10:00:00Z', 'k1,k2', NULL)`,
		`INSERT INTO unbind_pending (target, agent_id, guest_ref, node, since, key_ids, forgotten_at)
		 VALUES ('db', 'agent-b', '9101', 'pve', '2026-11-14T11:00:00Z', 'k1', '2026-11-14T12:00:00Z')`)
	ctx := context.Background()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.db.QueryContext(ctx, `SELECT target, agent_id, guest_ref, node, since, key_ids, COALESCE(forgotten_at, '') FROM unbind_pending ORDER BY target`)
	if err != nil {
		t.Fatal(err)
	}
	var got [][7]string
	for rows.Next() {
		var r [7]string
		if err := rows.Scan(&r[0], &r[1], &r[2], &r[3], &r[4], &r[5], &r[6]); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	want := [][7]string{
		{"db", "agent-b", "9101", "pve", "2026-11-14T11:00:00Z", "k1", "2026-11-14T12:00:00Z"},
		{"wiki", "agent-a", "9100", "pve", "2026-11-14T10:00:00Z", "k1,k2", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	// The store still reads them as before.
	if ok, err := s.IsParked(ctx, "wiki", "agent-a"); err != nil || !ok {
		t.Fatalf("parked: %v %v", ok, err)
	}
	if ok, err := s.IsParked(ctx, "db", "agent-b"); err != nil || ok {
		t.Fatalf("a forgotten row counts as parked: %v %v", ok, err)
	}
	// STRICT: a value of the wrong type is refused.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO unbind_pending (target, agent_id, guest_ref, node, since) VALUES ('x', 'a', X'00', 'pve', 'now')`); err == nil {
		t.Fatal("a BLOB in a TEXT column was accepted")
	}
}

// Every table is STRICT (T-227): a new table must be too.
func TestEveryTableStrict(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, openAt(t, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.db.QueryContext(ctx, `SELECT name, strict FROM pragma_table_list WHERE schema = 'main' AND type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name string
		var strict int
		if err := rows.Scan(&name, &strict); err != nil {
			t.Fatal(err)
		}
		n++
		if strict != 1 {
			t.Errorf("table %s is not STRICT", name)
		}
	}
	if n < 20 {
		t.Fatalf("only %d tables read", n)
	}
}
