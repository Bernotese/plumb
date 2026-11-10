package store

import (
	"context"
	"errors"
	"testing"
)

func TestCommitRevisionRollsBackLateFailure(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	// Fail after the transaction has inserted a revision and its first document.
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_second_doc BEFORE INSERT ON doc
		WHEN NEW.name = 'b' BEGIN SELECT RAISE(ABORT, 'injected late failure'); END`); err != nil {
		t.Fatal(err)
	}
	a := row("Block", "a", `{"kind":"Block","name":"a","spec":{}}`)
	b := row("Block", "b", `{"kind":"Block","name":"b","spec":{}}`)
	if _, err := s.CommitRevision(ctx, NewRevision{Author: "system", Docs: []DocRow{a, b}}); err == nil {
		t.Fatal("injected database failure must reject the commit")
	}
	if _, err := s.Head(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed transaction left a revision: %v", err)
	}
	if _, err := s.Doc(ctx, a.Hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed transaction left its first document: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fail_second_doc"); err != nil {
		t.Fatal(err)
	}
	rev, err := s.CommitRevision(ctx, NewRevision{Author: "system", Docs: []DocRow{a, b}})
	if err != nil || rev.ID != 1 {
		t.Fatalf("retry must create revision 1: %+v, %v", rev, err)
	}
}
