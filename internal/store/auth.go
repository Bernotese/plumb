package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// User is a human user.
type User struct {
	ID                 int64
	Name               string
	PasswordHash       string // argon2id PHC string
	MustChangePassword bool
	CreatedAt          time.Time
	// LockedAt: a restore locked the user (D-303); zero when not locked.
	LockedAt time.Time
}

// ErrExists is returned when a unique row exists already.
var ErrExists = errors.New("store: exists already")

// CountUsers returns the number of users.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user`).Scan(&n)
	return n, err
}

// CreateUser stores a new user.
func (s *Store) CreateUser(ctx context.Context, u User) (User, error) {
	created := s.now()
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO user (name, password_hash, must_change_password, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (name) DO NOTHING RETURNING id`,
		u.Name, u.PasswordHash, u.MustChangePassword, created).Scan(&u.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrExists
	}
	if err != nil {
		return User{}, err
	}
	u.CreatedAt, _ = parseTime(created)
	return u, nil
}

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created string
	var locked sql.NullString
	err := row.Scan(&u.ID, &u.Name, &u.PasswordHash, &u.MustChangePassword, &created, &locked)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if u.CreatedAt, err = parseTime(created); err != nil {
		return User{}, err
	}
	u.LockedAt, err = nullTime(locked)
	return u, err
}

// UserByName returns a user.
func (s *Store) UserByName(ctx context.Context, name string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, name, password_hash, must_change_password, created_at, locked_at FROM user WHERE name = ?`, name))
}

// UserByID returns a user.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, name, password_hash, must_change_password, created_at, locked_at FROM user WHERE id = ?`, id))
}

// SetPassword stores a new password hash, clears must_change_password and
// ends every session of the user except keep (a session id hash, or "").
func (s *Store) SetPassword(ctx context.Context, userID int64, hash, keep string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE user SET password_hash = ?, must_change_password = 0 WHERE id = ?`, hash, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM session WHERE user_id = ? AND id_hash <> ?`, userID, keep)
		return err
	})
}

// ResetPassword gives a user a new password hash that must change at the
// first login, ends a lock of a restore and every session of the user
// (D-321).
func (s *Store) ResetPassword(ctx context.Context, name, hash string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRowContext(ctx,
			`UPDATE user SET password_hash = ?, must_change_password = 1, locked_at = NULL WHERE name = ? RETURNING id`,
			hash, name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM session WHERE user_id = ?`, id)
		return err
	})
}

// Session is a login session. Only the hash of its id is stored.
type Session struct {
	IDHash    string
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
}

// CreateSession stores a session.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO session (id_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		sess.IDHash, sess.UserID, s.now(), formatTime(sess.ExpiresAt))
	return err
}

// SessionByHash returns a session that has not expired at now.
func (s *Store) SessionByHash(ctx context.Context, idHash string, now time.Time) (Session, error) {
	sess := Session{IDHash: idHash}
	var created, expires string
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, created_at, expires_at FROM session WHERE id_hash = ? AND expires_at > ?`, idHash, formatTime(now)).
		Scan(&sess.UserID, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if sess.CreatedAt, err = parseTime(created); err != nil {
		return Session{}, err
	}
	sess.ExpiresAt, err = parseTime(expires)
	return sess, err
}

// DeleteSession ends a session.
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM session WHERE id_hash = ?`, idHash)
	return err
}

// Token is an API token. Only the hash of its secret part is stored.
type Token struct {
	Seq        int64 // insertion order, for paging
	ID         string
	Name       string
	Hash       string
	Tier       string // read | plan | apply
	MaxRisk    string // reversible | disruptive, only for apply
	CreatedBy  string
	CreatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  time.Time
	// DisabledAt: a restore disabled the token (D-303); zero when enabled.
	DisabledAt time.Time
}

// CreateToken stores a token. The database refuses a max_risk for a tier
// other than apply, and any max_risk besides reversible and disruptive.
func (s *Store) CreateToken(ctx context.Context, t Token) (Token, error) {
	var maxRisk any
	if t.MaxRisk != "" {
		maxRisk = t.MaxRisk
	}
	created := s.now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO api_token (id, name, hash, tier, max_risk, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Hash, t.Tier, maxRisk, t.CreatedBy, created)
	if err != nil {
		return Token{}, err
	}
	t.CreatedAt, _ = parseTime(created)
	return t, nil
}

const tokenCols = `rowid, id, name, hash, tier, COALESCE(max_risk, ''), created_by, created_at, last_used_at, revoked_at, disabled_at`

func scanToken(row interface{ Scan(...any) error }) (Token, error) {
	var t Token
	var created string
	var lastUsed, revoked, disabled sql.NullString
	err := row.Scan(&t.Seq, &t.ID, &t.Name, &t.Hash, &t.Tier, &t.MaxRisk, &t.CreatedBy, &created, &lastUsed, &revoked, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, ErrNotFound
	}
	if err != nil {
		return Token{}, err
	}
	if t.CreatedAt, err = parseTime(created); err != nil {
		return Token{}, err
	}
	if t.LastUsedAt, err = nullTime(lastUsed); err != nil {
		return Token{}, err
	}
	if t.RevokedAt, err = nullTime(revoked); err != nil {
		return Token{}, err
	}
	t.DisabledAt, err = nullTime(disabled)
	return t, err
}

// TokenByID returns a token that is not revoked.
func (s *Store) TokenByID(ctx context.Context, id string) (Token, error) {
	return scanToken(s.db.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM api_token WHERE id = ? AND revoked_at IS NULL`, id))
}

// Tokens returns up to n tokens, revoked ones included, newest first.
// before is the Seq of the last token of the previous page, or 0.
func (s *Store) Tokens(ctx context.Context, before int64, n int) ([]Token, error) {
	q := `SELECT ` + tokenCols + ` FROM api_token`
	args := []any{}
	if before > 0 {
		q += ` WHERE rowid < ?`
		args = append(args, before)
	}
	q += ` ORDER BY rowid DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, append(args, n)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TouchToken records a use of a token.
func (s *Store) TouchToken(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_token SET last_used_at = ? WHERE id = ?`, s.now(), id)
	return err
}

// RevokeToken revokes a token. The row stays (soft revoke, D-085).
// revoked is false when the token was revoked already; an unknown id is
// ErrNotFound.
func (s *Store) RevokeToken(ctx context.Context, id string) (t Token, revoked bool, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE api_token SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, s.now(), id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		revoked = n == 1
		t, err = scanToken(tx.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM api_token WHERE id = ?`, id))
		return err
	})
	return t, revoked, err
}

// Comment is a comment on an Issue.
type Comment struct {
	ID        int64
	IssueID   int64
	Author    string
	Body      string
	CreatedAt time.Time
}

// AddComment stores a comment on an existing Issue.
func (s *Store) AddComment(ctx context.Context, c Comment) (Comment, error) {
	created := s.now()
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO issue_comment (issue_id, author, body, created_at) VALUES (?, ?, ?, ?) RETURNING id`,
		c.IssueID, c.Author, c.Body, created).Scan(&c.ID)
	if err != nil {
		return Comment{}, err
	}
	c.CreatedAt, _ = parseTime(created)
	return c, nil
}

// Comments returns the comments of an Issue in order.
func (s *Store) Comments(ctx context.Context, issueID int64) ([]Comment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, author, body, created_at FROM issue_comment WHERE issue_id = ? ORDER BY id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		c := Comment{IssueID: issueID}
		var created string
		if err := rows.Scan(&c.ID, &c.Author, &c.Body, &created); err != nil {
			return nil, err
		}
		if c.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TokenHashes returns the hashes of all tokens. For tests that prove no
// response leaks one.
func (s *Store) TokenHashes(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT hash FROM api_token`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
