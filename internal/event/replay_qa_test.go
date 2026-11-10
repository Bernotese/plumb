package event

import (
	"context"
	"testing"
)

func TestReplayPersistedBatchesFromCursorThenLive(t *testing.T) {
	l := newLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const count = 207 // More than two stored replay batches.
	for range count {
		if _, err := l.Emit(ctx, "stored", "target:web1", "system", nil); err != nil {
			t.Fatal(err)
		}
	}
	// A fresh Log must replay from the store, without in-memory history.
	reopened := New(l.st)
	const cursor = int64(17)
	sub := reopened.Subscribe(ctx, cursor)
	for want := cursor + 1; want <= count; want++ {
		if e := recv(t, sub.C); e.Seq != want || e.Type != "stored" {
			t.Fatalf("replay got %+v, want seq %d", e, want)
		}
	}
	if _, err := reopened.Emit(ctx, "live", "target:web1", "system", nil); err != nil {
		t.Fatal(err)
	}
	if e := recv(t, sub.C); e.Seq != count+1 || e.Type != "live" {
		t.Fatalf("replay/live boundary duplicated or lost an event: %+v", e)
	}
	// Reconnect after the last sequence excludes that event.
	reconnect := reopened.Subscribe(ctx, count+1)
	if _, err := reopened.Emit(ctx, "next", "target:web1", "system", nil); err != nil {
		t.Fatal(err)
	}
	if e := recv(t, reconnect.C); e.Seq != count+2 || e.Type != "next" {
		t.Fatalf("reconnect returned the cursor or missed its successor: %+v", e)
	}
}
