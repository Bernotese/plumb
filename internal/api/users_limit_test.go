package api

import (
	"fmt"
	"testing"
	"time"
)

// The login limit grows, has a cap and always ends (D-330, risks.md §1
// worst case 5).
func TestBackoffLimiterGrowsAndEnds(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	l := newBackoffLimiter(clock, 5, time.Minute, 15*time.Minute, time.Hour)
	const k = "user:admin|ip:1"
	for i := 0; i < 4; i++ {
		l.fail(k)
	}
	if w := l.blocked(k); w != 0 {
		t.Fatalf("blocked after 4 failures: %v", w)
	}
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for i, d := range want {
		l.fail(k)
		if w := l.blocked(k); w != d {
			t.Fatalf("failure %d: wait %v, want %v", i+5, w, d)
		}
		now = now.Add(d) // the guesser waits exactly until the end
		if w := l.blocked(k); w != 0 {
			t.Fatalf("failure %d: still blocked after the wait: %v", i+5, w)
		}
	}
	// Many more failures: still capped at max, never permanent.
	for i := 0; i < 100; i++ {
		l.fail(k)
	}
	if w := l.blocked(k); w != 15*time.Minute {
		t.Fatalf("after 100 more: %v", w)
	}
	// One quiet hour after the last block ends: the key is forgotten.
	now = now.Add(15*time.Minute + time.Hour)
	if w := l.blocked(k); w != 0 {
		t.Fatalf("after an hour: %v", w)
	}
	for i := 0; i < 4; i++ {
		l.fail(k)
	}
	if w := l.blocked(k); w != 0 {
		t.Fatalf("count did not restart: %v", w)
	}
	// Another key is not touched; reset frees at once.
	l.fail(k)
	if l.blocked("user:admin|ip:2") != 0 {
		t.Fatal("other client blocked")
	}
	l.reset(k)
	if l.blocked(k) != 0 {
		t.Fatal("reset did not free")
	}
}

func TestBackoffLimiterBoundsKeys(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	l := newBackoffLimiter(func() time.Time { return now }, 1, time.Minute, time.Minute, time.Hour)
	for i := 0; i < maxLimiterKeys+50; i++ {
		l.fail(fmt.Sprintf("k%d", i))
	}
	if len(l.keys) > maxLimiterKeys {
		t.Fatalf("%d keys", len(l.keys))
	}
}
