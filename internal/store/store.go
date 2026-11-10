// Package store is the only package that talks to SQLite
// (docs/ARCHITECTURE.md §2, D-020). It opens the database, runs the
// migrations and has typed methods for the tables. It knows nothing about
// the meaning of documents; package state does.
package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// Migrations holds the forward-only migration files, named
// migrations/NNNN_name.sql. NNNN counts from 0001 without gaps. They run in
// file-name order, each exactly once; PRAGMA user_version holds the number
// of the last applied migration.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Store is an open Plumb database.
type Store struct {
	db *sql.DB
	// Now returns the current time. Tests replace it.
	Now func() time.Time
}

// Open opens (or creates) the database file at path and runs all pending
// migrations. Every connection has foreign keys on (the schema relies on
// them), WAL mode and a busy timeout. Write transactions start with
// BEGIN IMMEDIATE, so two writers wait for each other instead of failing
// on a lock upgrade.
func Open(ctx context.Context, path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	s := &Store{db: db, Now: time.Now}
	if err := s.check(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// check makes sure that the connection settings took effect.
func (s *Store) check(ctx context.Context) error {
	var fk int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if fk != 1 {
		return errors.New("store: foreign keys are off")
	}
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if mode != "wal" {
		return fmt.Errorf("store: journal mode is %q, want wal", mode)
	}
	return nil
}

var migrationName = regexp.MustCompile(`^migrations/(\d{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	version int
	name    string
	body    string
}

// migrations returns the embedded migrations in order. The numbers must be
// 1, 2, 3, … without gaps.
func migrations() ([]migration, error) {
	names, err := fs.Glob(Migrations, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []migration
	for i, name := range names {
		m := migrationName.FindStringSubmatch(name)
		if m == nil {
			return nil, fmt.Errorf("store: bad migration name %s", name)
		}
		v, _ := strconv.Atoi(m[1])
		if v != i+1 {
			return nil, fmt.Errorf("store: migration %s has number %d, want %d", name, v, i+1)
		}
		body, err := Migrations.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: name, body: string(body)})
	}
	return out, nil
}

// migrate applies every migration above the user_version of the database.
// Each migration and its version bump run in one transaction.
//
// Foreign keys are off while a migration runs, so a migration can rebuild a
// table that other tables refer to (the SQLite procedure for a changed
// CHECK, D-096, D-115): DROP TABLE would otherwise delete the rows of the
// parent first. Before the commit, PRAGMA foreign_key_check must find no
// broken reference, so a migration cannot leave the database worse.
func (s *Store) migrate(ctx context.Context) error {
	all, err := migrations()
	if err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if current > len(all) {
		return fmt.Errorf("store: database has schema version %d, this program knows only %d", current, len(all))
	}
	if current == len(all) {
		return nil
	}
	// One connection: the foreign_keys pragma is per connection and is a
	// no-op inside a transaction.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	for _, m := range all[current:] {
		if err := migrateOne(ctx, conn, m); err != nil {
			conn.Raw(func(any) error { return driver.ErrBadConn }) // never reuse a connection without foreign keys
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		conn.Raw(func(any) error { return driver.ErrBadConn })
		return fmt.Errorf("store: %w", err)
	}
	return nil
}

func migrateOne(ctx context.Context, conn *sql.Conn, m migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.body); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	broken := rows.Next()
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if broken {
		return errors.New("a foreign key is broken after the migration")
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return err
	}
	return tx.Commit()
}

// tx runs fn in a write transaction and commits when fn returns nil.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// timeFormat is the text form of all times in the database: UTC RFC 3339
// with a fixed number of fraction digits, so that text order is time order
// and SQL can compare times as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

func (s *Store) now() string { return formatTime(s.Now()) }

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(v string) (time.Time, error) { return time.Parse(time.RFC3339Nano, v) }

// nullTime parses a nullable time column.
func nullTime(v sql.NullString) (time.Time, error) {
	if !v.Valid {
		return time.Time{}, nil
	}
	return parseTime(v.String)
}
