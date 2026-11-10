package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// ResolutionIgnore is the resolution of an Issue that a user ignored once
// or that a Rule covers (D-091, D-092).
const ResolutionIgnore = "ignore"

// IgnoreIssue resolves an open Issue as "ignore" by a principal and
// remembers its changes for its target and resource (D-091), in one
// transaction. changes is the canonical JSON that later reports are
// compared with. An Issue that is not open is ErrIssueNotOpen.
func (s *Store) IgnoreIssue(ctx context.Context, id int64, by string, changes json.RawMessage) (Issue, error) {
	var out Issue
	err := s.tx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		iss, err := scanIssue(tx.QueryRowContext(ctx, `SELECT `+issueColumns+` FROM issue WHERE id = ?`, id))
		if err != nil {
			return err
		}
		if iss.Status != IssueOpen {
			return ErrIssueNotOpen
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE issue SET status = 'resolved', resolution = 'ignore', resolved_by = ?, resolved_at = ?, updated_at = ?
			 WHERE id = ? AND status = 'open'`, by, now, now, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO issue_ignore (target, resource, changes, issue_id, created_at) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (target, resource) DO UPDATE SET changes = excluded.changes, issue_id = excluded.issue_id,
			   created_at = excluded.created_at`,
			iss.Target, iss.Resource, string(changes), id, now); err != nil {
			return err
		}
		out, err = scanIssue(tx.QueryRowContext(ctx, `SELECT `+issueColumns+` FROM issue WHERE id = ?`, id))
		return err
	})
	return out, err
}

// IgnoredOnce returns the remembered changes of "Ignore once" for a target
// and resource, or ErrNotFound.
func (s *Store) IgnoredOnce(ctx context.Context, target, resource string) (json.RawMessage, error) {
	var changes string
	err := s.db.QueryRowContext(ctx, `SELECT changes FROM issue_ignore WHERE target = ? AND resource = ?`, target, resource).Scan(&changes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return json.RawMessage(changes), err
}

// ForgetIgnoreOnce ends "Ignore once" for a target and resource: the
// resource reported unchanged, or a Run applied it (D-091).
func (s *Store) ForgetIgnoreOnce(ctx context.Context, target, resource string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM issue_ignore WHERE target = ? AND resource = ?`, target, resource)
	return err
}

// ForgetIgnoredDrift drops the record of dropped drift of a target and
// resource: the resource reported unchanged (D-092).
func (s *Store) ForgetIgnoredDrift(ctx context.Context, target, resource string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ignored_drift WHERE target = ? AND resource = ?`, target, resource)
	return err
}

// PruneIgnoredDrift drops the records of rules that are not in keep (the
// rules of the head revision): a removed rule ignores nothing (D-092).
func (s *Store) PruneIgnoredDrift(ctx context.Context, keep []string) error {
	q := `DELETE FROM ignored_drift`
	args := make([]any, len(keep))
	if len(keep) > 0 {
		q += ` WHERE rule NOT IN (?` + strings.Repeat(", ?", len(keep)-1) + `)`
		for i, k := range keep {
			args[i] = k
		}
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// IgnoredDrift is drift that a Rule dropped at intake (D-092). No values.
type IgnoredDrift struct {
	Target   string
	Resource string
	Rule     string
	Attrs    []string
	LastSeen time.Time
}

// RecordIgnored notes that a rule dropped the attrs of a target and resource now.
func (s *Store) RecordIgnored(ctx context.Context, target, resource, rule string, attrs []string) error {
	sorted := append([]string{}, attrs...)
	sort.Strings(sorted)
	raw, err := json.Marshal(sorted)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO ignored_drift (target, resource, rule, attrs, last_seen) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (target, resource, rule) DO UPDATE SET attrs = excluded.attrs, last_seen = excluded.last_seen`,
		target, resource, rule, string(raw), s.now())
	return err
}

// IgnoredDriftOf returns the dropped drift of a target, ordered by resource and rule.
func (s *Store) IgnoredDriftOf(ctx context.Context, target string) ([]IgnoredDrift, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT resource, rule, attrs, last_seen FROM ignored_drift WHERE target = ? ORDER BY resource, rule`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IgnoredDrift
	for rows.Next() {
		d := IgnoredDrift{Target: target}
		var attrs, last string
		if err := rows.Scan(&d.Resource, &d.Rule, &attrs, &last); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(attrs), &d.Attrs); err != nil {
			return nil, err
		}
		if d.LastSeen, err = parseTime(last); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetIssueChanges replaces the changes of an open Issue (a new Rule
// dropped some of them). Only an open Issue changes.
func (s *Store) SetIssueChanges(ctx context.Context, id int64, changes json.RawMessage) error {
	_, err := s.db.ExecContext(ctx, `UPDATE issue SET changes = ?, updated_at = ? WHERE id = ? AND status = 'open'`,
		string(changes), s.now(), id)
	return err
}
