package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestAgentAddrs(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	if err := s.PutEnrollToken(ctx, "web1", "h", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "h", t0, Agent{ID: "a1", CredentialHash: "c"}); err != nil {
		t.Fatal(err)
	}
	gap := 30 * time.Second
	get := func() ([]string, bool) {
		t.Helper()
		r, known, err := s.ReportedAddrs(ctx, "web1")
		if err != nil {
			t.Fatal(err)
		}
		return r.List, known
	}
	if _, known := get(); known {
		t.Fatal("a new agent has a list")
	}
	set := func(at time.Time, list []string) (bool, error) {
		return s.SetAgentAddrs(ctx, "a1", list, false, at, gap)
	}
	if ch, err := set(t0, []string{"192.0.2.1"}); !ch || err != nil {
		t.Fatalf("first list: %v %v", ch, err)
	}
	if ch, err := set(t0.Add(time.Second), []string{"192.0.2.1"}); ch || err != nil {
		t.Fatalf("equal list: %v %v", ch, err)
	}
	if _, err := set(t0.Add(29*time.Second), []string{"192.0.2.2"}); !errors.Is(err, ErrAddrsRateLimited) {
		t.Fatalf("within the gap: %v", err)
	}
	if l, _ := get(); !slices.Equal(l, []string{"192.0.2.1"}) {
		t.Fatalf("list %q", l)
	}
	// Unknown is not limited, and only writes when a list is known.
	if ch, err := set(t0.Add(29*time.Second), nil); !ch || err != nil {
		t.Fatalf("clear: %v %v", ch, err)
	}
	if ch, err := set(t0.Add(29*time.Second), nil); ch || err != nil {
		t.Fatalf("clear again: %v %v", ch, err)
	}
	if _, known := get(); known {
		t.Fatal("cleared list known")
	}
	if ch, err := set(t0.Add(30*time.Second), []string{}); !ch || err != nil {
		t.Fatalf("empty list after the gap: %v %v", ch, err)
	}
	if l, known := get(); !known || l == nil || len(l) != 0 {
		t.Fatalf("empty list: %#v %v", l, known)
	}
	if _, err := s.SetAgentAddrs(ctx, "nope", nil, false, t0, gap); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing agent: %v", err)
	}
	// A re-enrollment removes the row and the list with it.
	if err := s.PutEnrollToken(ctx, "web1", "h2", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, known := get(); known {
		t.Fatal("list after re-enrollment")
	}
}
