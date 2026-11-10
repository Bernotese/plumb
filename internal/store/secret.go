package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// SecretRow is one stored secret: ciphertext only (D-087). Package secret
// encrypts and opens it; store never sees a value.
type SecretRow struct {
	Name       string
	Version    int64
	Nonce      []byte
	Ciphertext []byte
	UpdatedBy  string
	UpdatedAt  time.Time
}

// SecretMeta is a secret without its ciphertext.
type SecretMeta struct {
	Name      string
	Version   int64
	UpdatedBy string
	UpdatedAt time.Time
}

// SealFunc encrypts a value for a version. PutSecret calls it inside its
// transaction, after it has taken the next version.
type SealFunc func(version int64) (nonce, ciphertext []byte, err error)

// PutSecret creates or replaces a secret. It takes the next version of the
// store-wide counter, lets seal encrypt for that version, and writes the
// row, all in one transaction.
func (s *Store) PutSecret(ctx context.Context, name, updatedBy string, seal SealFunc) (SecretMeta, error) {
	m := SecretMeta{Name: name, UpdatedBy: updatedBy}
	updated := s.now()
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			`UPDATE secret_counter SET value = value + 1 WHERE id = 1 RETURNING value`).Scan(&m.Version); err != nil {
			return err
		}
		nonce, ct, err := seal(m.Version)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO secret (name, version, nonce, ciphertext, updated_by, updated_at) VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT (name) DO UPDATE SET version = excluded.version, nonce = excluded.nonce,
			   ciphertext = excluded.ciphertext, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
			name, m.Version, nonce, ct, updatedBy, updated)
		return err
	})
	if err != nil {
		return SecretMeta{}, err
	}
	m.UpdatedAt, _ = parseTime(updated)
	return m, nil
}

// DeleteSecret removes a secret. ErrNotFound when it does not exist.
func (s *Store) DeleteSecret(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM secret WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SecretMetas returns the metadata of all secrets, sorted by name. It
// never reads a ciphertext.
func (s *Store) SecretMetas(ctx context.Context) ([]SecretMeta, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, version, updated_by, updated_at FROM secret ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecretMeta
	for rows.Next() {
		var m SecretMeta
		var updated string
		if err := rows.Scan(&m.Name, &m.Version, &m.UpdatedBy, &updated); err != nil {
			return nil, err
		}
		if m.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SecretRows returns the rows of the named secrets that exist, sorted by
// name. A missing name has no row.
func (s *Store) SecretRows(ctx context.Context, names []string) ([]SecretRow, error) {
	if len(names) == 0 {
		return nil, nil
	}
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, version, nonce, ciphertext, updated_by, updated_at FROM secret
		 WHERE name IN (?`+strings.Repeat(", ?", len(names)-1)+`) ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecretRow
	for rows.Next() {
		var r SecretRow
		var updated string
		if err := rows.Scan(&r.Name, &r.Version, &r.Nonce, &r.Ciphertext, &r.UpdatedBy, &updated); err != nil {
			return nil, err
		}
		if r.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountSecrets returns the number of stored secrets.
func (s *Store) CountSecrets(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM secret`).Scan(&n)
	return n, err
}

// KeyCheck returns the stored key check value, or ErrNotFound.
func (s *Store) KeyCheck(ctx context.Context) ([]byte, error) {
	var v []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM key_check WHERE id = 1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

// SetKeyCheck stores the key check value once. ErrExists when a value is
// stored already.
func (s *Store) SetKeyCheck(ctx context.Context, v []byte) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO key_check (id, value) VALUES (1, ?) ON CONFLICT (id) DO NOTHING`, v)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrExists
	}
	return nil
}

// SecretWrite is one write of PutSecretsTx.
type SecretWrite struct {
	Name string
	Seal SealFunc
}

// PutSecretsTx reads the rows of names and lets decide choose the writes,
// all in one transaction: decide sees the rows as they are now, and an
// error from it writes nothing. Each write takes the next version.
func (s *Store) PutSecretsTx(ctx context.Context, names []string, by string, decide func(rows []SecretRow) ([]SecretWrite, error)) error {
	updated := s.now()
	return s.tx(ctx, func(tx *sql.Tx) error {
		var rows []SecretRow
		if len(names) > 0 {
			args := make([]any, len(names))
			for i, n := range names {
				args[i] = n
			}
			r, err := tx.QueryContext(ctx,
				`SELECT name, version, nonce, ciphertext, updated_by, updated_at FROM secret
				 WHERE name IN (?`+strings.Repeat(", ?", len(names)-1)+`) ORDER BY name`, args...)
			if err != nil {
				return err
			}
			for r.Next() {
				var row SecretRow
				var at string
				if err := r.Scan(&row.Name, &row.Version, &row.Nonce, &row.Ciphertext, &row.UpdatedBy, &at); err != nil {
					r.Close()
					return err
				}
				row.UpdatedAt, _ = parseTime(at)
				rows = append(rows, row)
			}
			if err := errors.Join(r.Err(), r.Close()); err != nil {
				return err
			}
		}
		writes, err := decide(rows)
		if err != nil {
			return err
		}
		for _, w := range writes {
			var version int64
			if err := tx.QueryRowContext(ctx,
				`UPDATE secret_counter SET value = value + 1 WHERE id = 1 RETURNING value`).Scan(&version); err != nil {
				return err
			}
			nonce, ct, err := w.Seal(version)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO secret (name, version, nonce, ciphertext, updated_by, updated_at) VALUES (?, ?, ?, ?, ?, ?)
				 ON CONFLICT (name) DO UPDATE SET version = excluded.version, nonce = excluded.nonce,
				   ciphertext = excluded.ciphertext, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
				w.Name, version, nonce, ct, by, updated); err != nil {
				return err
			}
		}
		return nil
	})
}
