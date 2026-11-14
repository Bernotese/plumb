package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// Guest backups (T-221; D-405 … D-409, D-417). The lineage and the job
// rows of a Target; the volumes themselves are in Proxmox.

// ErrGuestBackupRunning: a backup job of the Target runs (one at a time).
var ErrGuestBackupRunning = errors.New("store: a backup job of this Target runs")

// BackupLineage returns the backup lineage of target for the binding
// generation gen. A new generation (a new guest of the Target, D-417)
// gets a new random lineage; the same generation keeps it.
func (s *Store) BackupLineage(ctx context.Context, target string, gen int64) (string, error) {
	var lineage string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var have int64
		err := tx.QueryRowContext(ctx, `SELECT gen, lineage FROM guest_backup_lineage WHERE target = ?`, target).Scan(&have, &lineage)
		if err == nil && have == gen {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		lineage = hex.EncodeToString(b)
		_, err = tx.ExecContext(ctx,
			`INSERT INTO guest_backup_lineage (target, gen, lineage, created_at) VALUES (?, ?, ?, ?)
			 ON CONFLICT (target) DO UPDATE SET gen = excluded.gen, lineage = excluded.lineage, created_at = excluded.created_at`,
			target, gen, lineage, s.now())
		return err
	})
	return lineage, err
}

// GuestBackupRun is one backup job of a Target.
type GuestBackupRun struct {
	ID         int64
	Target     string
	Lineage    string
	Trigger    string // scheduled | manual
	StartedAt  time.Time
	FinishedAt time.Time // zero while running
	Result     string    // running | done | warning | failed | refused
	Volid      string
	Message    string // Plumb's own text, never a task log (D-417)
}

// StartGuestBackupRun records a running job; ErrGuestBackupRunning when one runs.
func (s *Store) StartGuestBackupRun(ctx context.Context, target, lineage, trigger string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO guest_backup_run (target, lineage, trigger, started_at, result) VALUES (?, ?, ?, ?, 'running')
		 ON CONFLICT DO NOTHING RETURNING id`,
		target, lineage, trigger, s.now()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrGuestBackupRunning
	}
	return id, err
}

// RefusedGuestBackupRun records a job that did not start (no storage, clear
// storage, no space, restored mode) with Plumb's reason.
func (s *Store) RefusedGuestBackupRun(ctx context.Context, target, lineage, trigger, message string) error {
	now := s.now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO guest_backup_run (target, lineage, trigger, started_at, finished_at, result, message) VALUES (?, ?, ?, ?, ?, 'refused', ?)`,
		target, lineage, trigger, now, now, clipText(message, 1024))
	return err
}

// FinishGuestBackupRun ends a running job.
func (s *Store) FinishGuestBackupRun(ctx context.Context, id int64, result, volid, message string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE guest_backup_run SET result = ?, volid = ?, message = ?, finished_at = ? WHERE id = ? AND result = 'running'`,
		result, volid, clipText(message, 1024), s.now(), id)
	return err
}

// LastGuestBackupRun returns the newest job of target, or ErrNotFound.
func (s *Store) LastGuestBackupRun(ctx context.Context, target string) (GuestBackupRun, error) {
	runs, err := s.guestBackupRuns(ctx, `WHERE target = ? ORDER BY id DESC LIMIT 1`, target)
	if err != nil {
		return GuestBackupRun{}, err
	}
	if len(runs) == 0 {
		return GuestBackupRun{}, ErrNotFound
	}
	return runs[0], nil
}

// GuestBackupRunOfVolume returns the job that made volid, or ErrNotFound.
func (s *Store) GuestBackupRunOfVolume(ctx context.Context, target, volid string) (GuestBackupRun, error) {
	runs, err := s.guestBackupRuns(ctx, `WHERE target = ? AND volid = ? ORDER BY id DESC LIMIT 1`, target, volid)
	if err != nil {
		return GuestBackupRun{}, err
	}
	if len(runs) == 0 {
		return GuestBackupRun{}, ErrNotFound
	}
	return runs[0], nil
}

// EndRunningGuestBackups marks every running job as failed (a controller
// start: no job survives a restart).
func (s *Store) EndRunningGuestBackups(ctx context.Context, message string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE guest_backup_run SET result = 'failed', message = ?, finished_at = ? WHERE result = 'running'`,
		clipText(message, 1024), s.now())
	return err
}

func (s *Store) guestBackupRuns(ctx context.Context, where string, args ...any) ([]GuestBackupRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, target, lineage, trigger, started_at, finished_at, result, volid, message FROM guest_backup_run `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GuestBackupRun
	for rows.Next() {
		var r GuestBackupRun
		var started string
		var finished sql.NullString
		if err := rows.Scan(&r.ID, &r.Target, &r.Lineage, &r.Trigger, &started, &finished, &r.Result, &r.Volid, &r.Message); err != nil {
			return nil, err
		}
		if r.StartedAt, err = parseTime(started); err != nil {
			return nil, err
		}
		if r.FinishedAt, err = nullTime(finished); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
