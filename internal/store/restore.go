package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RestoreState is the restore record (D-290, D-303). The zero value with
// Restored false is a controller that was never restored.
type RestoreState struct {
	Restored        bool
	RestoredMode    bool
	RestoredAt      time.Time
	BackupCreatedAt time.Time
	Header          json.RawMessage
	ConfirmedAt     time.Time
	ConfirmedBy     string
	ConfirmReason   string
}

// ErrNotRestoredMode: the controller is not in restored mode.
var ErrNotRestoredMode = errors.New("store: not in restored mode")

// Restore marks a restored database (plumb restore), in one transaction:
// every session goes; every API token that is not revoked is disabled;
// admin gets adminHash and must change it at the next login; every other
// user is locked; every unused enrollment token goes; the restore record
// says restored mode. The database must have the user admin.
func (s *Store) Restore(ctx context.Context, backupCreatedAt time.Time, header json.RawMessage, adminHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now()
	steps := []struct {
		q    string
		args []any
	}{
		{`DELETE FROM session`, nil},
		{`UPDATE api_token SET disabled_at = ? WHERE revoked_at IS NULL AND disabled_at IS NULL`, []any{now}},
		{`UPDATE user SET locked_at = ? WHERE name <> 'admin' AND locked_at IS NULL`, []any{now}},
		{`DELETE FROM enroll_token WHERE used_at IS NULL`, nil},
		{`DELETE FROM restore_state`, nil},
		{`INSERT INTO restore_state (id, restored_at, backup_created_at, header, restored_mode) VALUES (1, ?, ?, ?, 1)`,
			[]any{now, formatTime(backupCreatedAt), string(header)}},
	}
	for _, st := range steps {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			return fmt.Errorf("store: restore: %w", err)
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE user SET password_hash = ?, must_change_password = 1, locked_at = NULL WHERE name = 'admin'`, adminHash)
	if err != nil {
		return fmt.Errorf("store: restore: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("store: restore: the backup has no user admin")
	}
	return tx.Commit()
}

// GetRestore returns the restore record.
func (s *Store) GetRestore(ctx context.Context) (RestoreState, error) {
	var r RestoreState
	var restored, created, header string
	var mode int
	var confirmed, by sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT restored_at, backup_created_at, header, restored_mode, confirmed_at, confirmed_by, confirm_reason
		FROM restore_state WHERE id = 1`).Scan(&restored, &created, &header, &mode, &confirmed, &by, &r.ConfirmReason)
	if errors.Is(err, sql.ErrNoRows) {
		return RestoreState{}, nil
	}
	if err != nil {
		return RestoreState{}, err
	}
	r.Restored, r.RestoredMode, r.Header, r.ConfirmedBy = true, mode == 1, json.RawMessage(header), by.String
	r.RestoredAt, _ = parseTime(restored)
	r.BackupCreatedAt, _ = parseTime(created)
	if confirmed.Valid {
		r.ConfirmedAt, _ = parseTime(confirmed.String)
	}
	return r, nil
}

// ConfirmRestore ends restored mode. ErrNotRestoredMode when it does not
// hold.
func (s *Store) ConfirmRestore(ctx context.Context, by, reason string) (RestoreState, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE restore_state SET restored_mode = 0, confirmed_at = ?, confirmed_by = ?, confirm_reason = ?
		WHERE id = 1 AND restored_mode = 1`, s.now(), by, reason)
	if err != nil {
		return RestoreState{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return RestoreState{}, ErrNotRestoredMode
	}
	return s.GetRestore(ctx)
}

// CountDisabledTokens counts the tokens that wait to be enabled.
func (s *Store) CountDisabledTokens(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_token WHERE disabled_at IS NOT NULL AND revoked_at IS NULL`).Scan(&n)
	return n, err
}

// EnableToken clears disabled_at. ErrNotFound for an unknown token,
// ErrTokenRevoked for a revoked one.
func (s *Store) EnableToken(ctx context.Context, id string) error {
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT revoked_at FROM api_token WHERE id = ?`, id).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if revoked.Valid {
		return ErrTokenRevoked
	}
	_, err = s.db.ExecContext(ctx, `UPDATE api_token SET disabled_at = NULL WHERE id = ?`, id)
	return err
}

// ErrTokenRevoked: a revoked token cannot be enabled.
var ErrTokenRevoked = errors.New("store: the token is revoked")

// MaxSchemaVersion is the number of the last migration of this binary.
func MaxSchemaVersion() (int, error) {
	ms, err := migrations()
	if err != nil || len(ms) == 0 {
		return 0, err
	}
	return ms[len(ms)-1].version, nil
}

// TokenRow returns a token, revoked or not.
func (s *Store) TokenRow(ctx context.Context, id string) (Token, error) {
	return scanToken(s.db.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM api_token WHERE id = ?`, id))
}
