package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

func mkUser(t *testing.T, s *Store, name, role string) User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), User{Name: name, PasswordHash: "h", Role: role, CreatedBy: "user:admin"})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Users from before 0017 become admin, made by system (D-329).
func TestMigrationUserRoles(t *testing.T) {
	v := migrationNumber(t, "_user_roles.sql")
	path := openAt(t, v-1,
		`INSERT INTO user (name, password_hash, created_at) VALUES ('admin', 'h', '2026-11-01T00:00:00Z')`)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, err := s.UserByName(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "admin" || u.CreatedBy != "system" || !u.DisabledAt.IsZero() || !u.LastLoginAt.IsZero() {
		t.Fatalf("migrated user: %+v", u)
	}
	if _, err := s.db.Exec(`UPDATE user SET role = 'root' WHERE name = 'admin'`); err == nil {
		t.Fatal("role outside the four values was stored")
	}
}

func TestCreateUserRoleAndNameReuse(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	boot, err := s.CreateUser(ctx, User{Name: "admin", PasswordHash: "h"})
	if err != nil || boot.Role != "admin" || boot.CreatedBy != "system" {
		t.Fatalf("bootstrap user: %+v %v", boot, err)
	}
	mkUser(t, s, "admin2", "admin")
	mkUser(t, s, "bob", "plan")
	if _, err := s.ChangeUser(ctx, "bob", UserChange{Disabled: boolp(true)}); err != nil {
		t.Fatal(err)
	}
	// A disabled user keeps its name for ever (D-330).
	if _, err := s.CreateUser(ctx, User{Name: "bob", PasswordHash: "h", Role: "read"}); !errors.Is(err, ErrExists) {
		t.Fatalf("reuse of a disabled name: %v", err)
	}
	us, err := s.Users(ctx, "", 10)
	if err != nil || len(us) != 3 || us[0].Name != "admin" || us[2].Name != "bob" || us[2].DisabledAt.IsZero() {
		t.Fatalf("users: %+v %v", us, err)
	}
	page, err := s.Users(ctx, "admin2", 10)
	if err != nil || len(page) != 1 || page[0].Name != "bob" {
		t.Fatalf("page after admin2: %+v %v", page, err)
	}
}

func TestChangeUserLastAdmin(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	mkUser(t, s, "admin", "admin")
	for _, c := range []UserChange{{Role: strp("apply")}, {Disabled: boolp(true)}} {
		if _, err := s.ChangeUser(ctx, "admin", c); !errors.Is(err, ErrLastAdmin) {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	// A locked admin (restore, D-303) cannot log in, so it does not count.
	mkUser(t, s, "old", "admin")
	if _, err := s.db.Exec(`UPDATE user SET locked_at = '2026-11-01T00:00:00Z' WHERE name = 'old'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeUser(ctx, "admin", UserChange{Role: strp("read")}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("locked admin counted: %v", err)
	}
	// Nothing changed by the refusals.
	if u, _ := s.UserByName(ctx, "admin"); u.Role != "admin" || !u.DisabledAt.IsZero() {
		t.Fatalf("after refusal: %+v", u)
	}
	// A disabled admin does not count either.
	mkUser(t, s, "off", "admin")
	if _, err := s.ChangeUser(ctx, "off", UserChange{Disabled: boolp(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeUser(ctx, "admin", UserChange{Disabled: boolp(true)}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disabled admin counted: %v", err)
	}
	// Re-enable "off": now "admin" may go.
	if _, err := s.ChangeUser(ctx, "off", UserChange{Disabled: boolp(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeUser(ctx, "admin", UserChange{Role: strp("plan")}); err != nil {
		t.Fatal(err)
	}
	// A non-admin change never needs the count.
	mkUser(t, s, "eve", "read")
	if _, err := s.ChangeUser(ctx, "eve", UserChange{Role: strp("apply")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeUser(ctx, "nobody", UserChange{Role: strp("read")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

// Two admins demote each other at the same time: exactly one wins
// (risks.md §1 worst case 3).
func TestChangeUserParallelDemote(t *testing.T) {
	for round := 0; round < 20; round++ {
		s, _ := openTemp(t)
		ctx := context.Background()
		mkUser(t, s, "a", "admin")
		mkUser(t, s, "b", "admin")
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, name := range []string{"a", "b"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c := UserChange{Role: strp("apply")}
				if i == 1 {
					c = UserChange{Disabled: boolp(true)}
				}
				_, errs[i] = s.ChangeUser(ctx, name, c)
			}()
		}
		wg.Wait()
		var ok, last int
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrLastAdmin):
				last++
			default:
				t.Fatalf("round %d: %v", round, err)
			}
		}
		var admins int
		s.db.QueryRow(`SELECT COUNT(*) FROM user WHERE role = 'admin' AND disabled_at IS NULL`).Scan(&admins)
		if ok != 1 || last != 1 || admins != 1 {
			t.Fatalf("round %d: ok=%d last=%d admins=%d", round, ok, last, admins)
		}
	}
}

// A role change or a disable ends every session; a loss of admin or a
// disable disables the user's tokens (risks.md §1 worst case 4).
func TestChangeUserSessionsAndTokens(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	mkUser(t, s, "root", "admin")
	alice := mkUser(t, s, "alice", "admin")
	bob := mkUser(t, s, "bob", "plan")
	exp := time.Now().Add(time.Hour)
	sessions := func(u User) int {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM session WHERE user_id = ?`, u.ID).Scan(&n)
		return n
	}
	for _, id := range []string{"s1", "s2"} {
		if err := s.CreateSession(ctx, Session{IDHash: id, UserID: alice.ID, ExpiresAt: exp}); err != nil {
			t.Fatal(err)
		}
	}
	s.CreateSession(ctx, Session{IDHash: "s3", UserID: bob.ID, ExpiresAt: exp})
	for _, tok := range []Token{
		{ID: "t1", Name: "a", Hash: "x", Tier: "read", CreatedBy: "user:alice"},
		{ID: "t2", Name: "b", Hash: "x", Tier: "apply", MaxRisk: "reversible", CreatedBy: "user:alice"},
		{ID: "t3", Name: "c", Hash: "x", Tier: "read", CreatedBy: "user:root"},
		{ID: "t4", Name: "d", Hash: "x", Tier: "read", CreatedBy: "user:alice"},
	} {
		if _, err := s.CreateToken(ctx, tok); err != nil {
			t.Fatal(err)
		}
	}
	s.RevokeToken(ctx, "t4")

	// No change: nothing ends.
	r, err := s.ChangeUser(ctx, "alice", UserChange{Role: strp("admin"), Disabled: boolp(false)})
	if err != nil || r.Changed || sessions(alice) != 2 {
		t.Fatalf("no-op change: %+v %v sessions=%d", r, err, sessions(alice))
	}
	// admin → apply: sessions end, the two live tokens get disabled_at.
	r, err = s.ChangeUser(ctx, "alice", UserChange{Role: strp("apply")})
	if err != nil || !r.Changed || r.TokensDisabled != 2 || r.User.Role != "apply" || sessions(alice) != 0 {
		t.Fatalf("demote: %+v %v sessions=%d", r, err, sessions(alice))
	}
	for id, want := range map[string]bool{"t1": true, "t2": true, "t3": false} {
		tok, err := s.TokenByID(ctx, id)
		if err != nil || tok.DisabledAt.IsZero() != !want {
			t.Fatalf("%s: disabled=%v %v", id, !tok.DisabledAt.IsZero(), err)
		}
	}
	// A role change of a non-admin ends its sessions, but its tokens stay
	// (only an admin makes tokens).
	r, err = s.ChangeUser(ctx, "bob", UserChange{Role: strp("read")})
	if err != nil || r.TokensDisabled != 0 || sessions(bob) != 0 {
		t.Fatalf("bob: %+v %v", r, err)
	}
	// Disable and enable again: the tokens stay disabled; a person enables
	// them one by one.
	s.db.Exec(`UPDATE api_token SET disabled_at = NULL WHERE id = 't1'`)
	if r, err = s.ChangeUser(ctx, "alice", UserChange{Disabled: boolp(true)}); err != nil || r.TokensDisabled != 1 || r.User.DisabledAt.IsZero() {
		t.Fatalf("disable: %+v %v", r, err)
	}
	if r, err = s.ChangeUser(ctx, "alice", UserChange{Disabled: boolp(false)}); err != nil || !r.User.DisabledAt.IsZero() {
		t.Fatalf("enable: %+v %v", r, err)
	}
	if tok, _ := s.TokenByID(ctx, "t1"); tok.DisabledAt.IsZero() {
		t.Fatal("enable of the user enabled its token")
	}
}

func TestTouchLogin(t *testing.T) {
	s, _ := openTemp(t)
	u := mkUser(t, s, "admin", "admin")
	if err := s.TouchLogin(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.UserByID(context.Background(), u.ID); got.LastLoginAt.IsZero() {
		t.Fatal("last_login_at not set")
	}
}
