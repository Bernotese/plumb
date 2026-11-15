package store

import (
	"context"
	"database/sql"
)

// DropHostKeys removes the host key pins and the repair report of the live
// binding of a Target, in one transaction (T-222, D-417 (4), N1 of the
// #325 re-check). A restore over the guest brings back the disk of the
// backup time, with its old host keys and its old repair line: no pin of
// the pre-restore guest may be used. The binding, its generation and the
// backup lineage stay (the guest is the same). The next repair report
// pins the keys again. It returns how many rows went.
func (s *Store) DropHostKeys(ctx context.Context, target string) (int64, error) {
	var n int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"guest_host_key", "repair_report"} {
			res, err := tx.ExecContext(ctx, `DELETE FROM `+table+` AS r WHERE r.target = ? AND `+liveGuestSQL, target)
			if err != nil {
				return err
			}
			m, err := res.RowsAffected()
			if err != nil {
				return err
			}
			n += m
		}
		return nil
	})
	return n, err
}
