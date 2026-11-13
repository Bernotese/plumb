package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// Separate Store pools exercise SQLite's write lock, not one Go connection.
// Both last admins attempt demotion after the same start barrier.
func TestParallelDemoteSeparateConnectionsQA(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()
	peer, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	mkUser(t, s, "a", "admin")
	mkUser(t, s, "b", "admin")
	for round := range 20 {
		for _, name := range []string{"a", "b"} {
			if _, err := s.ChangeUser(ctx, name, UserChange{Role: strp("admin")}); err != nil {
				t.Fatal(err)
			}
		}
		start := make(chan struct{})
		var ready, done sync.WaitGroup
		ready.Add(2)
		done.Add(2)
		errs := make([]error, 2)
		for i, db := range []*Store{s, peer} {
			go func() {
				defer done.Done()
				ready.Done()
				<-start
				_, errs[i] = db.ChangeUser(ctx, []string{"a", "b"}[i], UserChange{Role: strp("read")})
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()
		successes, refused := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrLastAdmin):
				refused++
			default:
				t.Fatalf("round %d: %v", round, err)
			}
		}
		var admins int
		if err := peer.db.QueryRow(`SELECT COUNT(*) FROM user WHERE role = 'admin' AND disabled_at IS NULL AND locked_at IS NULL`).Scan(&admins); err != nil {
			t.Fatal(err)
		}
		if successes != 1 || refused != 1 || admins != 1 {
			t.Fatalf("round %d: success=%d last-admin=%d admins=%d", round, successes, refused, admins)
		}
	}
}
