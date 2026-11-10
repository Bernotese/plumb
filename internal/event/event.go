// Package event is the append-only event log and its subscriptions
// (docs/ARCHITECTURE.md §4). The log is also the audit log. Events are
// stored first and then announced, so a subscriber never sees an event
// that is not in the database.
package event

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/Bernotese/plumb/internal/store"
)

// Event is one entry of the log. Seq is the SSE event id.
type Event = store.Event

// Log appends events and serves subscriptions.
type Log struct {
	st *store.Store
	mu sync.Mutex
	// wake is closed and replaced on every append; waiting subscribers
	// read the store again.
	wake chan struct{}
}

// New returns the event log on a store.
func New(st *store.Store) *Log {
	return &Log{st: st, wake: make(chan struct{})}
}

// Append stores an event and wakes all subscribers.
func (l *Log) Append(ctx context.Context, e Event) (Event, error) {
	e, err := l.st.AppendEvent(ctx, e)
	if err != nil {
		return Event{}, err
	}
	l.mu.Lock()
	close(l.wake)
	l.wake = make(chan struct{})
	l.mu.Unlock()
	return e, nil
}

// Emit is Append with data built from a Go value. data must marshal to a
// JSON object, or be nil. Never pass a secret.
func (l *Log) Emit(ctx context.Context, typ, subject, actor string, data any) (Event, error) {
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return Event{}, err
		}
		raw = b
	}
	return l.Append(ctx, Event{Type: typ, Subject: subject, Actor: actor, Data: raw})
}

func (l *Log) waitChan() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.wake
}

// batch is how many events a subscriber reads from the store at once.
const batch = 100

// Subscribe returns a channel with every event after the sequence number
// after, first the stored ones and then new ones, in order and without
// gaps. The channel closes when ctx ends or the store fails. A slow
// reader slows only its own subscription.
func (l *Log) Subscribe(ctx context.Context, after int64) <-chan Event {
	out := make(chan Event)
	go func() {
		defer close(out)
		last := after
		for {
			// Take the wake channel before reading, so an append between
			// the read and the wait is not missed.
			wake := l.waitChan()
			events, err := l.st.EventsAfter(ctx, last, batch)
			if err != nil {
				return
			}
			for _, e := range events {
				select {
				case out <- e:
					last = e.Seq
				case <-ctx.Done():
					return
				}
			}
			if len(events) == batch {
				continue
			}
			select {
			case <-wake:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
