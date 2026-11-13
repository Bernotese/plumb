package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// Results of a backup run (D-302).
const (
	BackupRunning = "running"
	BackupOK      = "ok"
	BackupFailed  = "failed"
)

// BackupRun is one backup that serve started.
type BackupRun struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt time.Time // zero while running
	Result     string
	By         string
	Trigger    string // BackupSchedule, BackupAPI or BackupCLI
	Name       string
	Message    string
}

// Triggers of a backup run: only a scheduled run counts for the schedule.
const (
	BackupSchedule = "schedule"
	BackupAPI      = "api"
	BackupCLI      = "cli"
)

// ErrBackupRunning: another backup runs (only one at a time).
var ErrBackupRunning = errors.New("store: a backup is running")

// StartBackupRun records a new running backup. It fails with
// ErrBackupRunning while another one runs.
func (s *Store) StartBackupRun(ctx context.Context, by, trigger, name string) (BackupRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BackupRun{}, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_run WHERE result = 'running'`).Scan(&n); err != nil {
		return BackupRun{}, err
	}
	if n > 0 {
		return BackupRun{}, ErrBackupRunning
	}
	now := s.now()
	run := BackupRun{Result: BackupRunning, By: by, Trigger: trigger, Name: name}
	if err := tx.QueryRowContext(ctx, `INSERT INTO backup_run (started_at, result, by, trigger, name) VALUES (?, 'running', ?, ?, ?) RETURNING id`,
		now, by, trigger, name).Scan(&run.ID); err != nil {
		return BackupRun{}, fmt.Errorf("store: start backup run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return BackupRun{}, err
	}
	run.StartedAt, _ = parseTime(now)
	return run, nil
}

// FinishBackupRun ends a running backup with ok or failed.
func (s *Store) FinishBackupRun(ctx context.Context, id int64, result, message string) error {
	if result != BackupOK && result != BackupFailed {
		return fmt.Errorf("store: bad backup result %q", result)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE backup_run SET result = ?, message = ?, finished_at = ? WHERE id = ? AND result = 'running'`,
		result, message, s.now(), id)
	return err
}

// FailRunningBackups ends every running backup as failed: at the start of
// serve, a running row was cut by a stop.
func (s *Store) FailRunningBackups(ctx context.Context, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backup_run SET result = 'failed', message = ?, finished_at = ? WHERE result = 'running'`,
		message, s.now())
	return err
}

// LastBackupRun returns the newest backup run; trigger "" means any.
// ErrNotFound when there is none.
func (s *Store) LastBackupRun(ctx context.Context, trigger string) (BackupRun, error) {
	q := `SELECT id, started_at, COALESCE(finished_at, ''), result, by, trigger, name, message FROM backup_run`
	var args []any
	if trigger != "" {
		q += ` WHERE trigger = ?`
		args = append(args, trigger)
	}
	q += ` ORDER BY id DESC LIMIT 1`
	var r BackupRun
	var started, finished string
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&r.ID, &started, &finished, &r.Result, &r.By, &r.Trigger, &r.Name, &r.Message)
	if errors.Is(err, sql.ErrNoRows) {
		return BackupRun{}, ErrNotFound
	}
	if err != nil {
		return BackupRun{}, err
	}
	r.StartedAt, _ = parseTime(started)
	if finished != "" {
		r.FinishedAt, _ = parseTime(finished)
	}
	return r, nil
}

// BackupRunIDs maps the file name of every good or running backup run
// (the run that calls the retention is still running) to its id,
// the order in which the backups were made.
func (s *Store) BackupRunIDs(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, id FROM backup_run WHERE result IN ('ok', 'running')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var id int64
		if err := rows.Scan(&name, &id); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}

// Snapshot writes a consistent copy of the database to path (VACUUM INTO,
// D-302). path must not exist; the file gets mode 0600.
func (s *Store) Snapshot(ctx context.Context, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	// VACUUM INTO needs a file that does not exist or is empty.
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		os.Remove(path)
		return fmt.Errorf("store: snapshot: %w", err)
	}
	return nil
}

// SchemaVersion is the number of the last migration of the database.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}
