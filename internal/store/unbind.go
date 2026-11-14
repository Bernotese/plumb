package store

import (
	"context"
	"slices"
	"strings"
	"time"
)

// Parked is the agent of a deleted adopted Target whose unbind was not
// confirmed (D-396).
type Parked struct {
	Target, AgentID, GuestRef, Node string
	Since                           time.Time
	// KeyIDs are the repair keys valid at the park: the guest may hold
	// their lines.
	KeyIDs []string
	// Forgotten: an admin forgot the agent (no longer parked); the row
	// stays for the repair lines (review U2 of #309).
	Forgotten bool
}

// Park records the agent of a deleted adopted Target that still has to
// take unbind. It also drops the unused enrollment and repair tokens of
// the target: an enrollment would replace the parked agent with one that
// is not parked (the link makes no new token while the park lasts).
func (s *Store) Park(ctx context.Context, p Parked) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM enroll_token WHERE target = ? AND used_at IS NULL`, p.Target); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO unbind_pending (target, agent_id, guest_ref, node, since, key_ids, forgotten_at) VALUES (?, ?, ?, ?, ?, ?, NULL)
		 ON CONFLICT (target) DO UPDATE SET agent_id = excluded.agent_id, guest_ref = excluded.guest_ref, node = excluded.node,
		   since = excluded.since, key_ids = excluded.key_ids, forgotten_at = NULL`,
		p.Target, p.AgentID, p.GuestRef, p.Node, formatTime(p.Since), strings.Join(p.KeyIDs, ",")); err != nil {
		return err
	}
	return tx.Commit()
}

// Forget ends the park of a target but keeps the row as forgotten.
func (s *Store) Forget(ctx context.Context, target string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE unbind_pending SET forgotten_at = ? WHERE target = ? AND forgotten_at IS NULL`, s.now(), target)
	return err
}

// DropForgotten deletes the forgotten rows that hold none of the valid
// repair key ids: their lines no longer let anybody in.
func (s *Store) DropForgotten(ctx context.Context, valid []string) error {
	rows, err := s.LeftBehind(ctx)
	if err != nil {
		return err
	}
	for _, p := range rows {
		if !p.Forgotten || slices.ContainsFunc(p.KeyIDs, func(id string) bool { return slices.Contains(valid, id) }) {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM unbind_pending WHERE target = ? AND forgotten_at IS NOT NULL`, p.Target); err != nil {
			return err
		}
	}
	return nil
}

// Unpark drops the record of a target.
func (s *Store) Unpark(ctx context.Context, target string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM unbind_pending WHERE target = ?`, target)
	return err
}

// IsParked reports whether the agent agentID of target is parked.
func (s *Store) IsParked(ctx context.Context, target, agentID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM unbind_pending WHERE target = ? AND agent_id = ? AND forgotten_at IS NULL`, target, agentID).Scan(&n)
	return n > 0, err
}

// ParkedTargets lists every parked agent (not the forgotten ones).
func (s *Store) ParkedTargets(ctx context.Context) ([]Parked, error) {
	all, err := s.LeftBehind(ctx)
	return slices.DeleteFunc(all, func(p Parked) bool { return p.Forgotten }), err
}

// LeftBehind lists the parked and the forgotten rows.
func (s *Store) LeftBehind(ctx context.Context) ([]Parked, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT target, agent_id, guest_ref, node, since, key_ids, forgotten_at IS NOT NULL FROM unbind_pending ORDER BY target`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Parked
	for rows.Next() {
		var p Parked
		var since, keys string
		if err := rows.Scan(&p.Target, &p.AgentID, &p.GuestRef, &p.Node, &since, &keys, &p.Forgotten); err != nil {
			return nil, err
		}
		if keys != "" {
			p.KeyIDs = strings.Split(keys, ",")
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
	parked, err := s.ParkedTargets(ctx)
	if err != nil {
		return Parked{}, err
	}
	for _, p := range parked {
		if p.GuestRef == ref {
			return p, nil
		}
	}
	return Parked{}, ErrNotFound
}

// IsParkedTarget reports whether target has a parked agent (any id).
func (s *Store) IsParkedTarget(ctx context.Context, target string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM unbind_pending WHERE target = ? AND forgotten_at IS NULL`, target).Scan(&n)
	return n > 0, err
}
