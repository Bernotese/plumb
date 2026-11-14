package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Issue states and resolutions (D-045).
const (
	IssueOpen      = "open"
	IssueResolving = "resolving"
	IssueResolved  = "resolved"
)

// Issue is a drift report that the user resolves.
type Issue struct {
	ID         int64
	Target     string
	Resource   string
	Status     string
	Changes    json.RawMessage // [wire.Change], already redacted
	Resolution string
	PlanID     int64
	OpenedAt   time.Time
	UpdatedAt  time.Time
	ResolvedBy string
	ResolvedAt time.Time
	// ReopenedAt: a new mismatch opened this guest:address Issue again
	// after it was gone (D-215); zero otherwise.
	ReopenedAt time.Time
}

const issueColumns = `id, target, resource, status, changes, COALESCE(resolution, ''), COALESCE(plan_id, 0),
	opened_at, updated_at, COALESCE(resolved_by, ''), resolved_at, reopened_at`

func scanIssue(row interface{ Scan(...any) error }) (Issue, error) {
	var i Issue
	var changes, opened, updated string
	var resolved, reopened sql.NullString
	err := row.Scan(&i.ID, &i.Target, &i.Resource, &i.Status, &changes, &i.Resolution, &i.PlanID,
		&opened, &updated, &i.ResolvedBy, &resolved, &reopened)
	if errors.Is(err, sql.ErrNoRows) {
		return Issue{}, ErrNotFound
	}
	if err != nil {
		return Issue{}, err
	}
	i.Changes = json.RawMessage(changes)
	if i.OpenedAt, err = parseTime(opened); err != nil {
		return Issue{}, err
	}
	if i.UpdatedAt, err = parseTime(updated); err != nil {
		return Issue{}, err
	}
	if i.ResolvedAt, err = nullTime(resolved); err != nil {
		return Issue{}, err
	}
	i.ReopenedAt, err = nullTime(reopened)
	return i, err
}

// UpsertIssue opens an Issue for the target and resource, or updates the
// changes of the one that is not resolved. opened is true for a new Issue.
func (s *Store) UpsertIssue(ctx context.Context, target, resource string, changes json.RawMessage) (Issue, bool, error) {
	var out Issue
	opened := false
	err := s.tx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		res, err := tx.ExecContext(ctx,
			`UPDATE issue SET changes = ?, updated_at = ? WHERE target = ? AND resource = ? AND status <> 'resolved'`,
			string(changes), now, target, resource)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			opened = true
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO issue (target, resource, status, changes, opened_at, updated_at) VALUES (?, ?, 'open', ?, ?, ?)`,
				target, resource, string(changes), now, now); err != nil {
				return err
			}
		}
		out, err = scanIssue(tx.QueryRowContext(ctx,
			`SELECT `+issueColumns+` FROM issue WHERE target = ? AND resource = ? AND status <> 'resolved'`, target, resource))
		return err
	})
	return out, opened, err
}

// GetIssue returns one Issue.
func (s *Store) GetIssue(ctx context.Context, id int64) (Issue, error) {
	return scanIssue(s.db.QueryRowContext(ctx, `SELECT `+issueColumns+` FROM issue WHERE id = ?`, id))
}

// OpenIssue returns the Issue of a target and resource that is not
// resolved, or ErrNotFound.
func (s *Store) OpenIssue(ctx context.Context, target, resource string) (Issue, error) {
	return scanIssue(s.db.QueryRowContext(ctx,
		`SELECT `+issueColumns+` FROM issue WHERE target = ? AND resource = ? AND status <> 'resolved'`, target, resource))
}

// Issues returns the Issues of a target that are not resolved; an empty
// target returns all of them.
func (s *Store) Issues(ctx context.Context, target string) ([]Issue, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+issueColumns+` FROM issue WHERE status <> 'resolved' AND (?1 = '' OR target = ?1) ORDER BY id`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// IssuesOfPlan returns the Issues that a Plan resolves.
func (s *Store) IssuesOfPlan(ctx context.Context, planID int64) ([]Issue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+issueColumns+` FROM issue WHERE plan_id = ? ORDER BY id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// ErrIssueNotOpen is returned when an Issue is not open (another Plan
// resolves it, or it is resolved).
var ErrIssueNotOpen = errors.New("store: the issue is not open")

// SetIssueResolving links an open Issue to the Plan that resolves it.
// Only an open Issue can change; else ErrIssueNotOpen.
func (s *Store) SetIssueResolving(ctx context.Context, id, planID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE issue SET status = 'resolving', plan_id = ?, resolution = NULL, updated_at = ?
		 WHERE id = ? AND status = 'open'`, planID, s.now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrIssueNotOpen
	}
	return nil
}

// ReopenIssue sets a resolving Issue back to open (its Plan failed or went stale).
func (s *Store) ReopenIssue(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE issue SET status = 'open', plan_id = NULL, updated_at = ? WHERE id = ? AND status = 'resolving'`, s.now(), id)
	return err
}

// ResolveIssue closes an Issue. resolution is accept, revert, gone or ignore.
func (s *Store) ResolveIssue(ctx context.Context, id int64, resolution, by string) error {
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE issue SET status = 'resolved', resolution = ?, resolved_by = ?, resolved_at = ?, updated_at = ?
		 WHERE id = ? AND status <> 'resolved'`, resolution, by, now, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResolveIssueTargetDeleted resolves an Issue as "gone" by system because
// its Target was deleted, and marks it so that ReopenGoneIssue never opens
// it again (T-227 row 31).
func (s *Store) ResolveIssueTargetDeleted(ctx context.Context, id int64) error {
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE issue SET status = 'resolved', resolution = 'gone', resolved_by = 'system', resolved_at = ?, updated_at = ?, target_deleted = 1
		 WHERE id = ? AND status <> 'resolved'`, now, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkIssuesTargetDeleted marks the already resolved Issues of a deleted
// Target, so that ReopenGoneIssue never opens one of them again (F1 of
// #324: an Issue that went "gone" by itself shortly before the delete).
// It returns how many it marked.
func (s *Store) MarkIssuesTargetDeleted(ctx context.Context, target string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE issue SET target_deleted = 1 WHERE target = ? AND status = 'resolved' AND target_deleted = 0`, target)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ReopenGoneIssue opens the newest Issue of a target and resource again
// when it was resolved as "gone" at or after since (and not by the delete of
// its Target, T-227 row 31), and no other Issue of
// them is open. It sets the changes and reopened_at. It returns ErrNotFound
// when there is no such Issue (D-215: a flapping guest gets one Issue row).
func (s *Store) ReopenGoneIssue(ctx context.Context, target, resource string, since time.Time, changes json.RawMessage) (Issue, error) {
	var out Issue
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM issue WHERE target = ?1 AND resource = ?2 AND status = 'resolved' AND resolution = 'gone'
			 AND target_deleted = 0 AND resolved_at >= ?3
			 AND NOT EXISTS (SELECT 1 FROM issue WHERE target = ?1 AND resource = ?2 AND status <> 'resolved')
			 ORDER BY id DESC LIMIT 1`, target, resource, formatTime(since)).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		now := s.now()
		if _, err := tx.ExecContext(ctx,
			`UPDATE issue SET status = 'open', resolution = NULL, resolved_by = NULL, resolved_at = NULL, plan_id = NULL,
			 changes = ?, updated_at = ?, reopened_at = ? WHERE id = ?`, string(changes), now, now, id); err != nil {
			return err
		}
		out, err = scanIssue(tx.QueryRowContext(ctx, `SELECT `+issueColumns+` FROM issue WHERE id = ?`, id))
		return err
	})
	return out, err
}
