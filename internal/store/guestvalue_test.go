package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func junkValues(from, n int) []GuestValue {
	out := make([]GuestValue, n)
	for i := range out {
		out[i] = GuestValue{Resource: "file:/junk", Attr: "content", Hash: fmt.Sprintf("%064x", from+i)}
	}
	return out
}

// The race job of #248: RecordGuestValues writes in chunks, so no write
// transaction holds the database past the busy timeout. The cap still
// holds exactly across a chunk boundary, and a known row only moves.
func TestRecordGuestValuesChunks(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if over, err := s.RecordGuestValues(ctx, "web1", junkValues(0, GuestValueCap-1)); err != nil || over {
		t.Fatalf("fill: %v %v", over, err)
	}
	// One report: guestValueChunk-1 known rows, then two new ones; the
	// second new one falls into the next chunk and past the cap.
	vals := append(junkValues(0, guestValueChunk-1), junkValues(GuestValueCap, 2)...)
	over, err := s.RecordGuestValues(ctx, "web1", vals)
	if err != nil || !over {
		t.Fatalf("across the boundary: over %v %v, want true", over, err)
	}
	if n, _ := s.GuestValueCount(ctx, "web1"); n != GuestValueCap {
		t.Fatalf("rows %d, want %d", n, GuestValueCap)
	}
	if m, _ := s.MatchGuestValues(ctx, []string{fmt.Sprintf("%064x", GuestValueCap)}); len(m) != 1 {
		t.Errorf("the value below the cap was not stored: %+v", m)
	}
	if m, _ := s.MatchGuestValues(ctx, []string{fmt.Sprintf("%064x", GuestValueCap+1)}); len(m) != 0 {
		t.Errorf("a value past the cap was stored: %+v", m)
	}
	// Repeats only move: no overflow for a report of known rows.
	if over, err := s.RecordGuestValues(ctx, "web1", junkValues(0, 3*guestValueChunk)); err != nil || over {
		t.Fatalf("known rows: %v %v", over, err)
	}
	// Another Target has its own cap.
	if over, err := s.RecordGuestValues(ctx, "web2", junkValues(0, guestValueChunk+1)); err != nil || over {
		t.Fatalf("web2: %v %v", over, err)
	}
}

// The race job of #248: a guest that reports large lists again and again
// must not starve the other writers of the store. An agent poll writes its
// agent row (TouchAgent) and fails after the busy timeout (5 s). Before
// the fix a writer of back-to-back chunks made TouchAgent wait 2.5 s here
// and over 5 s under -race.
func TestRecordGuestValuesYields(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bound := time.Second
	if raceOn {
		bound = 2 * time.Second
	}
	for _, newValues := range []bool{false, true} {
		var stop atomic.Bool
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; !stop.Load(); i += 1000 {
				from := i % GuestValueCap // repeats after the first round
				if newValues {
					from = i
				}
				if _, err := s.RecordGuestValues(ctx, "web1", junkValues(from, 1000)); err != nil {
					t.Errorf("writer: %v", err)
					return
				}
			}
		}()
		var worst time.Duration
		for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
			start := time.Now()
			if err := s.TouchAgent(ctx, "nope", time.Now()); err != nil && !errors.Is(err, ErrNotFound) {
				t.Errorf("TouchAgent after %v: %v", time.Since(start), err)
			}
			worst = max(worst, time.Since(start))
			time.Sleep(20 * time.Millisecond)
		}
		stop.Store(true)
		<-done
		t.Logf("new values %v: worst TouchAgent wait %v (bound %v)", newValues, worst, bound)
		if worst > bound {
			t.Errorf("new values %v: TouchAgent waited %v for the writer, want at most %v", newValues, worst, bound)
		}
	}
}
