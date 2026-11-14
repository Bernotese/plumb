package store

// Repair keys, host key pins and repair reports (migration 0022, T-191).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// RepairKeyRow is one repair key. Sealed and Nonce hold the private key,
// sealed by the secret store; only the repair package opens it.
type RepairKeyRow struct {
	ID          string
	Role        string // "current" or "previous"
	PublicKey   string
	Fingerprint string
	Nonce       []byte
	Sealed      []byte
	CreatedAt   time.Time
}

// ErrRotationRunning: a previous repair key exists; it must be revoked
// before the next rotation.
var ErrRotationRunning = errors.New("store: a previous repair key exists")

// ErrNothingToRevoke: a revoke without a previous repair key.
var ErrNothingToRevoke = errors.New("store: there is no previous repair key")

// RepairKeys returns the repair keys, the current one first.
func (s *Store) RepairKeys(ctx context.Context) ([]RepairKeyRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, role, public_key, fingerprint, nonce, sealed, created_at FROM repair_key
		 ORDER BY role = 'current' DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RepairKeyRow
	for rows.Next() {
		var k RepairKeyRow
		var at string
		if err := rows.Scan(&k.ID, &k.Role, &k.PublicKey, &k.Fingerprint, &k.Nonce, &k.Sealed, &at); err != nil {
			return nil, err
		}
		if k.CreatedAt, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// AddRepairKey stores k as the current key. A current key becomes the
// previous one; with a previous key already there it fails with
// ErrRotationRunning and changes nothing. first: add only when no key
// exists at all (the first start); then an existing key is no error and
// added is false.
func (s *Store) AddRepairKey(ctx context.Context, k RepairKeyRow, first bool) (added bool, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var n, prev int
		if err := tx.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE role = 'previous') FROM repair_key`).Scan(&n, &prev); err != nil {
			return err
		}
		if first && n > 0 {
			return nil
		}
		if prev > 0 {
			return ErrRotationRunning
		}
		if _, err := tx.ExecContext(ctx, `UPDATE repair_key SET role = 'previous' WHERE role = 'current'`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO repair_key (id, role, public_key, fingerprint, nonce, sealed, created_at) VALUES (?, 'current', ?, ?, ?, ?, ?)`,
			k.ID, k.PublicKey, k.Fingerprint, k.Nonce, k.Sealed, formatTime(k.CreatedAt)); err != nil {
			return err
		}
		added = true
		return nil
	})
	return added, err
}

// DeletePreviousRepairKey removes the previous key. It reports whether
// one existed.
func (s *Store) DeletePreviousRepairKey(ctx context.Context) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM repair_key WHERE role = 'previous'`)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// HostKeyPins returns the pinned fingerprints of the guest of a target.
func (s *Store) HostKeyPins(ctx context.Context, target, guestID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT fingerprint FROM guest_host_key WHERE target = ? AND guest_id = ? ORDER BY fingerprint`, target, guestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// PinFirstHostKeys pins fps for the guest when it has no pin yet (the
// first report after a new binding, D-384), and drops the pins of every
// other guest of the target. It reports whether it pinned.
func (s *Store) PinFirstHostKeys(ctx context.Context, target, guestID string, fps []string, at time.Time) (bool, error) {
	pinned := false
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM guest_host_key WHERE target = ? AND guest_id <> ?`, target, guestID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM guest_host_key WHERE target = ? AND guest_id = ?`, target, guestID).Scan(&n); err != nil {
			return err
		}
		if n > 0 || len(fps) == 0 {
			return nil
		}
		for _, f := range fps {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO guest_host_key (target, guest_id, fingerprint, pinned_at, pinned_by) VALUES (?, ?, ?, ?, 'agent')`,
				target, guestID, f, formatTime(at)); err != nil {
				return err
			}
		}
		pinned = true
		return nil
	})
	return pinned, err
}

// AddHostKeyPin pins one more fingerprint, accepted by a person in an
// approved repair Plan (by is the approver).
func (s *Store) AddHostKeyPin(ctx context.Context, target, guestID, fp, by string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO guest_host_key (target, guest_id, fingerprint, pinned_at, pinned_by) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT DO NOTHING`, target, guestID, fp, formatTime(at), by)
	return err
}

// RepairReportRow is the last repair report of a target.
type RepairReportRow struct {
	GuestID    string
	Report     json.RawMessage
	ReportedAt time.Time
}

// SetRepairReport stores the last repair report of a target.
func (s *Store) SetRepairReport(ctx context.Context, target string, r RepairReportRow) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO repair_report (target, guest_id, report, reported_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (target) DO UPDATE SET guest_id = excluded.guest_id, report = excluded.report, reported_at = excluded.reported_at`,
		target, r.GuestID, string(r.Report), formatTime(r.ReportedAt))
	return err
}

// RepairReport returns the last repair report of a target, or ErrNotFound.
func (s *Store) RepairReport(ctx context.Context, target string) (RepairReportRow, error) {
	var r RepairReportRow
	var rep, at string
	err := s.db.QueryRowContext(ctx, `SELECT guest_id, report, reported_at FROM repair_report WHERE target = ?`, target).Scan(&r.GuestID, &rep, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.Report = json.RawMessage(rep)
	r.ReportedAt, err = parseTime(at)
	return r, err
}

// RepairReportTargets lists the targets with a report and the key ids of
// their lines, for previous.targets of the rotation view.
func (s *Store) RepairReportTargets(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT target, report FROM repair_report ORDER BY target`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var t, r string
		if err := rows.Scan(&t, &r); err != nil {
			return nil, err
		}
		out[t] = json.RawMessage(r)
	}
	return out, rows.Err()
}

// HostKeyOwners maps each fingerprint that is pinned for another target
// than except to that target (review S3 of #298).
func (s *Store) HostKeyOwners(ctx context.Context, fps []string, except string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range fps {
		if f == "" {
			continue
		}
		var t string
		err := s.db.QueryRowContext(ctx,
			`SELECT target FROM guest_host_key WHERE fingerprint = ? AND target <> ? ORDER BY target LIMIT 1`, f, except).Scan(&t)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[f] = t
	}
	return out, nil
}
