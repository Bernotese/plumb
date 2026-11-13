package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRestoreUnlocksAdmin(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.CreateUser(ctx, User{Name: "admin", PasswordHash: "old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE user SET locked_at = ? WHERE name = 'admin'`, s.now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(ctx, time.Now(), json.RawMessage(`{}`), "new"); err != nil {
		t.Fatal(err)
	}
	if admin, err := s.UserByName(ctx, "admin"); err != nil || !admin.LockedAt.IsZero() {
		t.Fatalf("restored admin is locked: %+v %v", admin, err)
	}
}
