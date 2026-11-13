package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
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
