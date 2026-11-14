package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Parked is the agent of a deleted adopted Target whose unbind was not
// confirmed (D-396).
type Parked struct {
	Target, AgentID, GuestRef, Node string
	Since                           time.Time
}

// Park records the agent of a deleted adopted Target that still has to
// take unbind.
func (s *Store) Park(ctx context.Context, p Parked) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO unbind_pending (target, agent_id, guest_ref, node, since) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (target) DO UPDATE SET agent_id = excluded.agent_id, guest_ref = excluded.guest_ref, node = excluded.node, since = excluded.since`,
		p.Target, p.AgentID, p.GuestRef, p.Node, formatTime(p.Since))
	return err
}

// Unpark drops the record of a target.
func (s *Store) Unpark(ctx context.Context, target string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM unbind_pending WHERE target = ?`, target)
	return err
}

// IsParked reports whether the agent agentID of target is parked.
func (s *Store) IsParked(ctx context.Context, target, agentID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM unbind_pending WHERE target = ? AND agent_id = ?`, target, agentID).Scan(&n)
	return n > 0, err
}

// ParkedTargets lists every parked agent.
func (s *Store) ParkedTargets(ctx context.Context) ([]Parked, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT target, agent_id, guest_ref, node, since FROM unbind_pending ORDER BY target`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Parked
	for rows.Next() {
		var p Parked
		var since string
		if err := rows.Scan(&p.Target, &p.AgentID, &p.GuestRef, &p.Node, &since); err != nil {
			return nil, err
		}
		if p.Since, err = parseTime(since); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ParkedByRef returns the parked agent of a guest, or ErrNotFound.
func (s *Store) ParkedByRef(ctx context.Context, ref string) (Parked, error) {
	var p Parked
	var since string
	err := s.db.QueryRowContext(ctx, `SELECT target, agent_id, guest_ref, node, since FROM unbind_pending WHERE guest_ref = ? LIMIT 1`, ref).
		Scan(&p.Target, &p.AgentID, &p.GuestRef, &p.Node, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return Parked{}, ErrNotFound
	}
	if err != nil {
		return Parked{}, err
	}
	p.Since, err = parseTime(since)
	return p, err
}

// IsParkedTarget reports whether target has a parked agent (any id).
func (s *Store) IsParkedTarget(ctx context.Context, target string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM unbind_pending WHERE target = ?`, target).Scan(&n)
	return n > 0, err
}
