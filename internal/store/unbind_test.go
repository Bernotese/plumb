package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPark(t *testing.T) {
	ctx := context.Background()
	fresh := filepath.Join(t.TempDir(), "p.db")
	copyMigratedTemplate(t, fresh) // T-227 row 30
	s, err := Open(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := Parked{Target: "wiki", AgentID: "ag1", GuestRef: "150", Node: "pve", Since: time.Now().UTC().Truncate(time.Second)}
	if err := s.Park(ctx, p); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.IsParked(ctx, "wiki", "ag1"); !ok {
		t.Fatal("not parked")
	}
	if ok, _ := s.IsParked(ctx, "wiki", "ag2"); ok {
		t.Fatal("another agent id of the same target counts as parked")
	}
	if got, err := s.ParkedByRef(ctx, "150"); err != nil || got.Target != "wiki" {
		t.Fatalf("%+v %v", got, err)
	}
	if l, _ := s.ParkedTargets(ctx); len(l) != 1 {
		t.Fatalf("%v", l)
	}
	if err := s.Unpark(ctx, "wiki"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ParkedByRef(ctx, "150"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after unpark: %v", err)
	}
}
