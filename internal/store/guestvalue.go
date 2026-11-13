package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// The copy rule (D-331, D-352; migration 0017_guest_value).

// GuestValueCap is the most rows that guest_value keeps per Target. A
// report past it stores nothing new and flags the Target; no row is ever
// evicted (review F1 of #243).
const GuestValueCap = 10_000

// GuestValue is one hashed value (or list element) of a drift report.
type GuestValue struct {
	Resource, Attr, Hash string
}

// GuestValueMatch is a stored row whose hash a Plan sets. IssueID is the
// Issue of the report, or else the latest Issue of the Target and
// resource; 0 when the drift never had one (a Rule covered it, D-352).
type GuestValueMatch struct {
	Target, Resource, Attr, Hash string
	IssueID                      int64
}

// RecordGuestValues stores the values of one drift report of target. A
// known row only moves last_seen (and takes the open Issue of its
// resource). A new row is stored only below GuestValueCap; overflow
// reports that at least one value was not stored.
//
// Work per report (review L1 of #248): repeats are dropped in memory;
// then one Issue lookup per resource and at most two statements per
// value, in one transaction. A report with more distinct values than
// GuestValueCap writes nothing and reports overflow at once (fail
// closed), so one report costs at most about 2 × GuestValueCap statements.
func (s *Store) RecordGuestValues(ctx context.Context, target string, vals []GuestValue) (overflow bool, err error) {
	seen := make(map[GuestValue]bool, len(vals))
	uniq := vals[:0:0]
	for _, v := range vals {
		if !seen[v] {
			seen[v] = true
			uniq = append(uniq, v)
		}
	}
	vals = uniq
	if len(vals) == 0 {
		return false, nil
	}
	if len(vals) > GuestValueCap {
		return true, nil
	}
	now := s.now()
	err = s.tx(ctx, func(tx *sql.Tx) error {
		count := -1
		openOf := map[string]sql.NullInt64{}
		for _, v := range vals {
			open, ok := openOf[v.Resource]
			if !ok {
				if err := tx.QueryRowContext(ctx,
					`SELECT MAX(id) FROM issue WHERE target = ? AND resource = ? AND status <> 'resolved'`,
					target, v.Resource).Scan(&open); err != nil {
					return err
				}
				openOf[v.Resource] = open
			}
			res, err := tx.ExecContext(ctx,
				`UPDATE guest_value SET last_seen = ?, issue_id = COALESCE(?, issue_id)
				 WHERE target = ? AND resource = ? AND attr = ? AND hash = ?`,
				now, open, target, v.Resource, v.Attr, v.Hash)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				continue
			}
			if count < 0 {
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM guest_value WHERE target = ?`, target).Scan(&count); err != nil {
					return err
				}
			}
			if count >= GuestValueCap {
				overflow = true
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO guest_value (target, resource, attr, hash, issue_id, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				target, v.Resource, v.Attr, v.Hash, open, now, now); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return overflow, err
}

// GuestValueCount returns the number of stored rows of target.
func (s *Store) GuestValueCount(ctx context.Context, target string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM guest_value WHERE target = ?`, target).Scan(&n)
	return n, err
}

// MatchGuestValues returns the stored rows whose hash is in hashes, for
// every Target.
func (s *Store) MatchGuestValues(ctx context.Context, hashes []string) ([]GuestValueMatch, error) {
	var out []GuestValueMatch
	const chunk = 500
	for len(hashes) > 0 {
		part := hashes[:min(chunk, len(hashes))]
		hashes = hashes[len(part):]
		args := make([]any, len(part))
		for i, h := range part {
			args[i] = h
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT g.target, g.resource, g.attr, g.hash,
			        COALESCE(g.issue_id, (SELECT MAX(i.id) FROM issue i WHERE i.target = g.target AND i.resource = g.resource), 0)
			 FROM guest_value g WHERE g.hash IN (?`+strings.Repeat(", ?", len(part)-1)+`)
			 ORDER BY g.target, g.resource, g.attr, g.hash`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var m GuestValueMatch
			if err := rows.Scan(&m.Target, &m.Resource, &m.Attr, &m.Hash, &m.IssueID); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, m)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// PruneGuestValues removes the rows last seen before before whose
// resource has no Issue that is not resolved: the drift ended then.
func (s *Store) PruneGuestValues(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM guest_value WHERE last_seen < ? AND NOT EXISTS (
		   SELECT 1 FROM issue i WHERE i.target = guest_value.target AND i.resource = guest_value.resource AND i.status <> 'resolved')`,
		formatTime(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// FlagGuestValues flags target until until (a later until wins).
func (s *Store) FlagGuestValues(ctx context.Context, target string, until time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO guest_value_flag (target, flagged_at, until) VALUES (?, ?, ?)
		 ON CONFLICT (target) DO UPDATE SET until = MAX(until, excluded.until)`,
		target, s.now(), formatTime(until))
	return err
}

// GuestValueFlagged returns the Targets of targets that are flagged at now.
func (s *Store) GuestValueFlagged(ctx context.Context, targets []string, now time.Time) ([]string, error) {
	var out []string
	for _, t := range targets {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM guest_value_flag WHERE target = ? AND until > ?`,
			t, formatTime(now)).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, t)
		}
	}
	return out, nil
}

// GuestValueFlags returns every Target that is flagged at now, sorted.
func (s *Store) GuestValueFlags(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT target FROM guest_value_flag WHERE until > ? ORDER BY target`, formatTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// EndGuestValueFlags removes the flags that ended at now and returns their
// Targets.
func (s *Store) EndGuestValueFlags(ctx context.Context, now time.Time) ([]string, error) {
	var out []string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT target FROM guest_value_flag WHERE until <= ? ORDER BY target`, formatTime(now))
		if err != nil {
			return err
		}
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err != nil {
				rows.Close()
				return err
			}
			out = append(out, t)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM guest_value_flag WHERE until <= ?`, formatTime(now))
		return err
	})
	return out, err
}

// SetPlanGuestData records the copy rule result of a pending Plan: its
// human_only reasons and the Issues and matches. ok is false when the
// Plan is not pending any more (nothing is written).
func (s *Store) SetPlanGuestData(ctx context.Context, planID int64, humanOnly []string, issues []int64, matches json.RawMessage) (ok bool, err error) {
	if issues == nil {
		issues = []int64{}
	}
	is, err := json.Marshal(issues)
	if err != nil {
		return false, err
	}
	err = s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE plan SET human_only = ? WHERE id = ? AND status = 'pending'`, humanOnlyJSON(humanOnly), planID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		ok = true
		_, err = tx.ExecContext(ctx,
			`INSERT INTO plan_guest_data (plan_id, issues, matches) VALUES (?, ?, ?)
			 ON CONFLICT (plan_id) DO UPDATE SET issues = excluded.issues, matches = excluded.matches`,
			planID, string(is), jsonOr(matches, "[]"))
		return err
	})
	return ok, err
}

// PlanGuestData returns what the copy rule recorded for a Plan;
// ErrNotFound when nothing.
func (s *Store) PlanGuestData(ctx context.Context, planID int64) (issues []int64, matches json.RawMessage, err error) {
	var is, ms string
	err = s.db.QueryRowContext(ctx, `SELECT issues, matches FROM plan_guest_data WHERE plan_id = ?`, planID).Scan(&is, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal([]byte(is), &issues); err != nil {
		return nil, nil, err
	}
	return issues, json.RawMessage(ms), nil
}
