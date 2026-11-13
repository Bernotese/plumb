package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrLastAdmin: the change would leave no enabled, unlocked admin (D-330).
var ErrLastAdmin = errors.New("store: last admin")

// Users returns up to n users sorted by name, disabled ones included.
// after is the name of the last user of the previous page, or "".
func (s *Store) Users(ctx context.Context, after string, n int) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+userCols+` FROM user WHERE name > ? ORDER BY name LIMIT ?`, after, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserChange is a change of PATCH /users/{name}; nil fields stay.
type UserChange struct {
	Role     *string
	Disabled *bool
}

// UserChanged is the result of ChangeUser.
type UserChanged struct {
	User    User
	Changed bool // false: the change changed nothing
	// TokensDisabled counts the tokens of the user that got disabled_at.
	TokensDisabled int
}

// isAdmin: the user counts for the last-admin rule. A user that a restore
// locked cannot log in, so it does not count.
func isAdmin(u User) bool {
	return u.Role == "admin" && u.DisabledAt.IsZero() && u.LockedAt.IsZero()
}

// ChangeUser changes the role or the disable of a user in one transaction
// (D-330). It refuses with ErrLastAdmin when the user is the last enabled,
// unlocked admin and would lose that; count and change are in the same
// write transaction (BEGIN IMMEDIATE), so two parallel demotes cannot both
// pass. A change of role or disable ends every session of the user; a loss
// of admin or a disable also sets disabled_at on every token that the user
// created and that is not revoked (re-enable: POST /tokens/{id}/enable).
func (s *Store) ChangeUser(ctx context.Context, name string, c UserChange) (UserChanged, error) {
	var out UserChanged
	err := s.tx(ctx, func(tx *sql.Tx) error {
		old, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userCols+` FROM user WHERE name = ?`, name))
		if err != nil {
			return err
		}
		role := old.Role
		if c.Role != nil {
			role = *c.Role
		}
		disabled := !old.DisabledAt.IsZero()
		if c.Disabled != nil {
			disabled = *c.Disabled
		}
		if role == old.Role && disabled == !old.DisabledAt.IsZero() {
			out = UserChanged{User: old}
			return nil
		}
		now := s.now()
		next := old
		next.Role = role
		next.DisabledAt = old.DisabledAt
		if disabled && old.DisabledAt.IsZero() {
			next.DisabledAt, _ = parseTime(now)
		} else if !disabled {
			next.DisabledAt = time.Time{}
		}
		if isAdmin(old) && !isAdmin(next) {
			var others int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM user WHERE id <> ? AND role = 'admin' AND disabled_at IS NULL AND locked_at IS NULL`,
				old.ID).Scan(&others); err != nil {
				return err
			}
			if others == 0 {
				return ErrLastAdmin
			}
		}
		var disabledAt any
		if !next.DisabledAt.IsZero() {
			disabledAt = formatTime(next.DisabledAt)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE user SET role = ?, disabled_at = ? WHERE id = ?`,
			role, disabledAt, old.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM session WHERE user_id = ?`, old.ID); err != nil {
			return err
		}
		lostAdmin := old.Role == "admin" && role != "admin"
		newlyDisabled := disabled && old.DisabledAt.IsZero()
		if lostAdmin || newlyDisabled {
			res, err := tx.ExecContext(ctx,
				`UPDATE api_token SET disabled_at = ? WHERE created_by = ? AND revoked_at IS NULL AND disabled_at IS NULL`,
				now, "user:"+old.Name)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			out.TokensDisabled = int(n)
		}
		out.User, out.Changed = next, true
		return nil
	})
	return out, err
}

// TouchLogin records a good login of the user.
func (s *Store) TouchLogin(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE user SET last_login_at = ? WHERE id = ?`, s.now(), userID)
	return err
}
