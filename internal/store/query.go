package store

import (
	"context"
	"database/sql"
	"errors"
)

// Paged lists go newest first. before is the id of the last item of the
// previous page (0 = start); limit is clamped to 1…200.

func clampLimit(limit int) int { return min(max(limit, 1), 200) }

func beforeOrMax(before int64) int64 {
	if before <= 0 {
		return 1<<63 - 1
	}
	return before
}

// ListPlans returns Plans, newest first. status "" means all.
func (s *Store) ListPlans(ctx context.Context, status string, before int64, limit int) ([]Plan, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+planColumns+` FROM plan WHERE id < ? AND (? = '' OR status = ?) ORDER BY id DESC LIMIT ?`,
		beforeOrMax(before), status, status, clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListRuns returns Runs, newest first.
func (s *Store) ListRuns(ctx context.Context, before int64, limit int) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM run WHERE id < ? ORDER BY id DESC LIMIT ?`,
		beforeOrMax(before), clampLimit(limit))
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := make([]Run, 0, len(ids))
	for _, id := range ids {
		r, err := s.GetRun(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ListIssues returns Issues, newest first. Empty filters mean all.
func (s *Store) ListIssues(ctx context.Context, status, target string, before int64, limit int) ([]Issue, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+issueColumns+` FROM issue WHERE id < ?1 AND (?2 = '' OR status = ?2) AND (?3 = '' OR target = ?3)
		 ORDER BY id DESC LIMIT ?4`, beforeOrMax(before), status, target, clampLimit(limit))
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

// RunOfPlan returns the Run of an approved Plan, or ErrNotFound.
func (s *Store) RunOfPlan(ctx context.Context, planID int64) (Run, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM run WHERE plan_id = ?`, planID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, id)
}

// LastRunOfTarget returns the newest Run with a step on the target.
func (s *Store) LastRunOfTarget(ctx context.Context, target string) (Run, error) {
	var id sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(run_id) FROM run_step WHERE target = ?`, target).Scan(&id); err != nil {
		return Run{}, err
	}
	if !id.Valid {
		return Run{}, ErrNotFound
	}
	return s.GetRun(ctx, id.Int64)
}

// AppliedRevision returns the revision of the newest succeeded Run with a
// step on the target, or 0.
func (s *Store) AppliedRevision(ctx context.Context, target string) (int64, error) {
	var rev sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(r.revision_id) FROM run r WHERE r.status = 'succeeded'
		 AND EXISTS (SELECT 1 FROM run_step s WHERE s.run_id = r.id AND s.target = ?)`, target).Scan(&rev)
	return rev.Int64, err
}

// DocChangedAt returns the revision that last changed a document of
// revision head: the first revision of the unbroken run of revisions up to
// head in which the document has the same hash.
func (s *Store) DocChangedAt(ctx context.Context, head int64, kind, name, hash string) (int64, error) {
	var last sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(r.id) FROM revision r WHERE r.id <= ?1 AND NOT EXISTS (
			SELECT 1 FROM revision_doc d WHERE d.revision_id = r.id AND d.kind = ?2 AND d.name = ?3 AND d.hash = ?4)`,
		head, kind, name, hash).Scan(&last)
	if err != nil {
		return 0, err
	}
	return last.Int64 + 1, nil
}
