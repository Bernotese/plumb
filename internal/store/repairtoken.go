package store

import (
	"context"
	"errors"
	"time"
)

// PutRepairToken stores the enrollment token of a repair (D-385, review
// R1 of #290). Unlike PutEnrollToken it keeps the agent row: the old
// credential works until the guest enrolls with the new token (Enroll
// replaces it then). A failed repair leaves the agent as it was. It
// replaces an unused token of the target.
func (s *Store) PutRepairToken(ctx context.Context, target, hash string, expires time.Time) error {
	if target == "" || hash == "" {
		return errors.New("store: repair token needs a target and a hash")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO enroll_token (target, hash, expires_at, used_at, created_at) VALUES (?, ?, ?, NULL, ?)
		 ON CONFLICT (target) DO UPDATE SET hash = excluded.hash, expires_at = excluded.expires_at,
		 used_at = NULL, created_at = excluded.created_at`,
		target, hash, formatTime(expires), s.now())
	return err
}

// DropEnrollToken removes the token of a target when it is still the
// unused token with this hash: a repair that sent nothing drops its
// token. Another or a used token stays. It reports whether it removed one.
func (s *Store) DropEnrollToken(ctx context.Context, target, hash string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM enroll_token WHERE target = ? AND hash = ? AND used_at IS NULL`, target, hash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
