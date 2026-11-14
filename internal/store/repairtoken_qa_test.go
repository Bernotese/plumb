package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// T-195 (mutant R9): dropping the token of a failed repair never removes a
// token that the guest already used; only the unused token with that hash
// goes.
func TestDropEnrollTokenKeepsUsed(t *testing.T) {
	ctx := context.Background()
	fresh := filepath.Join(t.TempDir(), "t.db")
	copyMigratedTemplate(t, fresh) // T-227 row 30
	s, err := Open(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	exp := time.Now().Add(15 * time.Minute)
	if err := s.PutRepairToken(ctx, "web1", "hash-a", exp); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "hash-a", time.Now(), Agent{ID: "agent-new", Target: "web1", CredentialHash: "cred", Hostname: "web1"}); err != nil {
		t.Fatal(err)
	}
	if dropped, err := s.DropEnrollToken(ctx, "web1", "hash-a"); err != nil || dropped {
		t.Fatalf("a used token was dropped: %v %v", dropped, err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM enroll_token WHERE target = 'web1' AND used_at IS NOT NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("used token rows %d %v, want 1", n, err)
	}
	// The unused token with the hash goes; another hash stays.
	if err := s.PutRepairToken(ctx, "web1", "hash-b", exp); err != nil {
		t.Fatal(err)
	}
	if dropped, _ := s.DropEnrollToken(ctx, "web1", "hash-x"); dropped {
		t.Fatal("a token with another hash was dropped")
	}
	if dropped, _ := s.DropEnrollToken(ctx, "web1", "hash-b"); !dropped {
		t.Fatal("the unused token was not dropped")
	}
}
