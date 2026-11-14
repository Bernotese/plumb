package store

import (
	"context"
	"database/sql"
)

// ForgetTargetMarks drops the per-Target marks that belong to the guest of
// a deleted Target (T-209 sweep): the "Ignore once" memory (D-091), the
// record of drift that a Rule dropped (D-092) and the value cap flag
// (D-353). A new Target of the same name starts without them. The stored
// guest values stay: they are the evidence of the copy rule (D-331) and
// go by the normal prune.
func (s *Store) ForgetTargetMarks(ctx context.Context, target string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM issue_ignore WHERE target = ?`,
			`DELETE FROM ignored_drift WHERE target = ?`,
			`DELETE FROM guest_value_flag WHERE target = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, target); err != nil {
				return err
			}
		}
		return nil
	})
}
