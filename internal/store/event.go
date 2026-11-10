package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Event is one entry of the append-only event log.
type Event struct {
	Seq     int64
	Time    time.Time
	Type    string // for example "plan.approved"
	Subject string // for example "plan:12"
	Actor   string // principal
	Data    json.RawMessage
}

// AppendEvent stores an event and returns it with its sequence number
// and time. Data must be a JSON object; nil means {}.
func (s *Store) AppendEvent(ctx context.Context, e Event) (Event, error) {
	if e.Type == "" || e.Subject == "" || e.Actor == "" {
		return Event{}, errors.New("store: event needs type, subject and actor")
	}
	if e.Data == nil {
		e.Data = json.RawMessage(`{}`)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(e.Data, &obj); err != nil || obj == nil {
		return Event{}, errors.New("store: event data must be a JSON object")
	}
	if e.Time.IsZero() {
		e.Time = s.Now()
	}
	e.Time = e.Time.UTC()
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO event (time, type, subject, actor, data) VALUES (?, ?, ?, ?, ?) RETURNING seq`,
		formatTime(e.Time), e.Type, e.Subject, e.Actor, string(e.Data)).Scan(&e.Seq)
	if err != nil {
		return Event{}, err
	}
	return e, nil
}

// EventsAfter returns up to limit events with a sequence number above
// after, in order.
func (s *Store) EventsAfter(ctx context.Context, after int64, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, time, type, subject, actor, data FROM event WHERE seq > ? ORDER BY seq LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var t, data string
		if err := rows.Scan(&e.Seq, &t, &e.Type, &e.Subject, &e.Actor, &data); err != nil {
			return nil, err
		}
		if e.Time, err = parseTime(t); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastEventSeq returns the highest sequence number, or 0.
func (s *Store) LastEventSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM event`).Scan(&seq)
	return seq, err
}
