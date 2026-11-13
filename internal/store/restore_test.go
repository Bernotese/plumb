package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A restore makes the user admin an enabled admin again, also when the
// backup had it demoted or disabled (D-351); every other user is locked,
// so admin is the one admin who can log in, and the last-admin rule
// holds at once.
func TestRestoreMakesAdminAnEnabledAdmin(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	mkUser(t, s, "admin", "admin")
	mkUser(t, s, "boss", "admin")
	if _, err := s.ChangeUser(ctx, "admin", UserChange{Role: strp("read"), Disabled: boolp(true)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(ctx, time.Now(), json.RawMessage(`{}`), "new-hash"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByName(ctx, "admin")
	if err != nil || u.Role != "admin" || !u.DisabledAt.IsZero() || !u.LockedAt.IsZero() || !u.MustChangePassword || u.PasswordHash != "new-hash" {
		t.Fatalf("admin after restore: %+v %v", u, err)
	}
	if b, _ := s.UserByName(ctx, "boss"); b.LockedAt.IsZero() || b.Role != "admin" {
		t.Fatalf("boss after restore: %+v", b)
	}
	// boss is locked, so admin is the last admin.
	if _, err := s.ChangeUser(ctx, "admin", UserChange{Role: strp("apply")}); err != ErrLastAdmin {
		t.Fatalf("demote of the only usable admin: %v", err)
	}
}

// Without a user admin (it cannot be renamed, but a database may lack it)
// the restore fails and changes nothing: the users stay as they were.
func TestRestoreWithoutAdminChangesNothing(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	mkUser(t, s, "boss", "admin")
	mkUser(t, s, "eve", "plan")
	err := s.Restore(ctx, time.Now(), json.RawMessage(`{}`), "new-hash")
	if err == nil || !strings.Contains(err.Error(), "no user admin") {
		t.Fatalf("restore without admin: %v", err)
	}
	for _, n := range []string{"boss", "eve"} {
		if u, _ := s.UserByName(ctx, n); !u.LockedAt.IsZero() {
			t.Fatalf("%s locked by a failed restore", n)
		}
	}
	if r, err := s.GetRestore(ctx); err != nil || r.Restored {
		t.Fatalf("restore record after a failed restore: %+v %v", r, err)
	}
}
