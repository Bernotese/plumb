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
func (s *Store) ForgetTargetMarks(ctx context.Context, target string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM issue_ignore WHERE target = ?`,
			`DELETE FROM ignored_drift WHERE target = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, target); err != nil {
				return err
			}
		}
		return nil
	})
}
