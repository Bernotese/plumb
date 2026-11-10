package event

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/store"
)

func newLog(t *testing.T) *Log {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st)
}

func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("subscription closed")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	return Event{}
}

func TestAppendAndSubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := newLog(t)
	for i := 1; i <= 3; i++ {
		e, err := l.Emit(ctx, "test.old", fmt.Sprintf("target:t%d", i), "system", map[string]int{"i": i})
		if err != nil || e.Seq != int64(i) {
			t.Fatalf("append %d: %+v, %v", i, e, err)
		}
	}
	// Replay starts after the given sequence number.
	sub := l.Subscribe(ctx, 1)
	if e := recv(t, sub.C); e.Seq != 2 || e.Subject != "target:t2" || string(e.Data) != `{"i":2}` {
		t.Fatalf("first replayed event: %+v", e)
	}
	if e := recv(t, sub.C); e.Seq != 3 {
		t.Fatalf("second replayed event: %+v", e)
	}
	// Live events follow without a gap, also when many arrive at once.
	go func() {
		for i := 0; i < 250; i++ {
			l.Emit(ctx, "test.new", "target:x", "system", nil)
		}
	}()
	for want := int64(4); want < 254; want++ {
		if e := recv(t, sub.C); e.Seq != want || string(e.Data) != `{}` {
			t.Fatalf("got %+v, want seq %d", e, want)
		}
	}
	cancel()
	select {
	case _, ok := <-sub.C:
		if ok {
			t.Fatal("event after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("subscription did not close")
	}
	if err := sub.Err(); err != nil {
		t.Errorf("a cancel is no error: %v", err)
	}
}

// A store failure closes the subscription with an error (review T-009 nit 8).
func TestSubscribeStoreError(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	l := New(st)
	st.Close()
	sub := l.Subscribe(context.Background(), 0)
	select {
	case _, ok := <-sub.C:
		if ok {
			t.Fatal("event from a closed store")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("subscription did not close")
	}
	if sub.Err() == nil {
		t.Error("store failure: want an error")
	}
}

func TestAppendRejectsBadEvents(t *testing.T) {
	l := newLog(t)
	ctx := context.Background()
	for name, e := range map[string]Event{
		"no type":         {Subject: "s:1", Actor: "system"},
		"no subject":      {Type: "t", Actor: "system"},
		"no actor":        {Type: "t", Subject: "s:1"},
		"data not object": {Type: "t", Subject: "s:1", Actor: "system", Data: []byte(`[1]`)},
		"data not JSON":   {Type: "t", Subject: "s:1", Actor: "system", Data: []byte(`{`)},
	} {
		if _, err := l.Append(ctx, e); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := l.Emit(ctx, "t", "s:1", "system", []int{1}); err == nil {
		t.Error("Emit with a non-object: want an error")
	}
}
