package store

import (
	"context"
	"database/sql"
)

// ForgetTargetMarks drops the per-Target marks that belong to the guest of
// a deleted Target (T-209 sweep, D-397): the "Ignore once" memory (D-091)
// and the record of drift that a Rule dropped (D-092). A new Target of the
// same name starts without them. What the copy rule needs stays: the
// stored guest values (D-331, normal prune) and the value cap flag (D-353,
// until it ends).
// It returns the number of rows it dropped.
func (s *Store) ForgetTargetMarks(ctx context.Context, target string) (int64, error) {
	var n int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM issue_ignore WHERE target = ?`,
			`DELETE FROM ignored_drift WHERE target = ?`,
		} {
			res, err := tx.ExecContext(ctx, q, target)
			if err != nil {
				return err
			}
			c, _ := res.RowsAffected()
			n += c
		}
		return nil
	})
	return n, err
}

// MarkedTargets lists the target names that have an Issue that is not
// resolved, an "Ignore once" memory or a record of dropped drift (T-212).
func (s *Store) MarkedTargets(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT target FROM issue WHERE status <> 'resolved'
		 UNION SELECT target FROM issue_ignore
		 UNION SELECT target FROM ignored_drift
		 ORDER BY target`)
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
