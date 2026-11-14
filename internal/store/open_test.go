package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plumb.db")
	copyMigratedTemplate(t, path) // T-227 row 30; Open still checks the version
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func row(kind, name, body string) DocRow {
	sum := sha256.Sum256([]byte(body))
	return DocRow{Kind: kind, Name: name, Hash: hex.EncodeToString(sum[:]), Body: []byte(body)}
}

func TestOpenSettings(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()
	// Every connection of the pool has the settings, not only the first.
	s.db.SetMaxIdleConns(0)
	for i := 0; i < 3; i++ {
		var fk, timeout int
		var mode string
		if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
			t.Fatalf("foreign_keys = %d, %v", fk, err)
		}
		if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
			t.Fatalf("journal_mode = %q, %v", mode, err)
		}
		if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout <= 0 {
			t.Fatalf("busy_timeout = %d, %v", timeout, err)
		}
	}
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	all, _ := migrations()
	if version != len(all) || version < 2 {
		t.Fatalf("user_version = %d, want %d", version, len(all))
	}
	s.Close()

	// A second open runs no migration again (it would fail on CREATE TABLE).
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	// A database from a newer program is refused.
	if _, err := s2.db.ExecContext(ctx, "PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	s2.Close()
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "999") {
		t.Fatalf("newer schema: %v", err)
	}
}

func TestCommitRevision(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	s.Now = func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.FixedZone("x", 3600)) }

	if _, err := s.Head(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("head of empty store: %v", err)
	}
	a := row("Block", "a", `{"kind":"Block","name":"a","spec":{}}`)
	b := row("Block", "b", `{"kind":"Block","name":"b","spec":{}}`)
	b2 := row("Block", "b", `{"kind":"Block","name":"b","spec":{"x":1}}`)
	c := row("Image", "c", `{"kind":"Image","name":"c","spec":{}}`)

	r1, err := s.CommitRevision(ctx, NewRevision{Author: "system", Reason: "seed", Docs: []DocRow{a, b}})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := s.CommitRevision(ctx, NewRevision{Author: "user:admin", Reason: "two", Docs: []DocRow{a, b2, c}})
	if err != nil {
		t.Fatal(err)
	}
	if r1.ID != 1 || r2.ID != 2 || r1.CreatedAt.Location() != time.UTC || r1.CreatedAt.Hour() != 11 {
		t.Fatalf("r1 = %+v, r2 = %+v", r1, r2)
	}
	head, err := s.Head(ctx)
	if err != nil || head.ID != 2 || head.Author != "user:admin" || head.PlanID != 0 {
		t.Fatalf("head = %+v, %v", head, err)
	}
	var docs int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM doc`).Scan(&docs)
	if docs != 4 { // a shared; b, b2, c
		t.Errorf("doc rows = %d, want 4", docs)
	}
	got, err := s.RevisionDocs(ctx, 2)
	if err != nil || len(got) != 3 || got[0].Name != "a" || got[1].Hash != b2.Hash || got[2].Kind != "Image" {
		t.Fatalf("RevisionDocs = %+v, %v", got, err)
	}
	if d, err := s.Doc(ctx, b.Hash); err != nil || string(d.Body) != string(b.Body) {
		t.Errorf("Doc = %+v, %v", d, err)
	}
	if _, err := s.RevisionDoc(ctx, 1, "Image", "c"); !errors.Is(err, ErrNotFound) {
		t.Errorf("doc not in revision: %v", err)
	}
	list, err := s.Revisions(ctx, 0, 10)
	if err != nil || len(list) != 2 || list[0].ID != 2 {
		t.Errorf("Revisions = %+v, %v", list, err)
	}
	for _, limit := range []int{0, -1} {
		if list, _ := s.Revisions(ctx, 0, limit); len(list) != 1 || list[0].ID != 2 {
			t.Errorf("Revisions with limit %d = %+v, want only the head", limit, list)
		}
	}
	if list, _ := s.Revisions(ctx, 2, 10); len(list) != 1 || list[0].ID != 1 {
		t.Errorf("Revisions before 2 = %+v", list)
	}

	diff, err := s.Diff(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff) != 2 || diff[0] != (DocChange{Kind: "Block", Name: "b", Op: DocChanged, From: b.Hash, To: b2.Hash}) ||
		diff[1] != (DocChange{Kind: "Image", Name: "c", Op: DocAdded, To: c.Hash}) {
		t.Errorf("Diff(1, 2) = %+v", diff)
	}
	back, _ := s.Diff(ctx, 2, 1)
	if len(back) != 2 || back[0].Op != DocChanged || back[1].Op != DocRemoved || back[1].From != c.Hash {
		t.Errorf("Diff(2, 1) = %+v", back)
	}
	if fromEmpty, _ := s.Diff(ctx, 0, 1); len(fromEmpty) != 2 || fromEmpty[0].Op != DocAdded {
		t.Errorf("Diff(0, 1) = %+v", fromEmpty)
	}
	if same, err := s.Diff(ctx, 2, 2); err != nil || len(same) != 0 {
		t.Errorf("Diff(2, 2) = %+v, %v", same, err)
	}
	if _, err := s.Diff(ctx, 1, 7); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown revision: %v", err)
	}

	// Bad input never reaches the database.
	bad := row("Block", "x", `{"kind":"Block","name":"x","spec":{}}`)
	bad.Hash = strings.Repeat("0", 64)
	for name, in := range map[string]NewRevision{
		"wrong hash":    {Author: "system", Docs: []DocRow{bad}},
		"name mismatch": {Author: "system", Docs: []DocRow{row("Block", "y", `{"kind":"Block","name":"x","spec":{}}`)}},
		"kind mismatch": {Author: "system", Docs: []DocRow{row("Image", "x", `{"kind":"Block","name":"x","spec":{}}`)}},
		"duplicate":     {Author: "system", Docs: []DocRow{a, a}},
		"no author":     {Docs: []DocRow{a}},
		"unknown plan":  {Author: "system", PlanID: 42, Docs: []DocRow{a}},
		"body not JSON": {Author: "system", Docs: []DocRow{row("Block", "x", `nope`)}},
	} {
		if _, err := s.CommitRevision(ctx, in); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if head, _ := s.Head(ctx); head.ID != 2 {
		t.Errorf("a failed commit left revision %d", head.ID)
	}
}

// Two writers at the same time both succeed and get different numbers.
func TestConcurrentCommits(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.CommitRevision(ctx, NewRevision{Author: "system", Docs: []DocRow{
				row("Block", "a", `{"kind":"Block","name":"a","spec":{"i":`+string(rune('0'+i))+`}}`),
			}})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if head, _ := s.Head(ctx); head.ID != 8 {
		t.Errorf("head = %d, want 8", head.ID)
	}
}
