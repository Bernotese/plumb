package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Image version states (D-198).
const (
	ImageBuilding = "building"
	ImageReady    = "ready"
	ImageFailed   = "failed"
	ImageRemoved  = "removed"
)

// Build triggers (D-199).
const (
	TriggerChange   = "change"
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
)

// ImageVersion is one build of an Image (table image_version). It holds
// no VMID (D-188).
type ImageVersion struct {
	Image        string
	Version      int
	Status       string
	Trigger      string
	RunID        int64
	RevisionID   int64
	RecipeSHA256 string
	BaseSHA256   string
	Digest       string // build=<digest> in the template description
	DiskGB       int
	StartedAt    time.Time
	FinishedAt   time.Time // zero while building
	RemovedAt    time.Time // zero unless removed
}

// ErrBuildRunning is returned when an Image has a version in the state
// building already: one build per Image at a time (D-203).
var ErrBuildRunning = errors.New("store: a build of this image is running")

// ErrImageVersionState is returned for a status change that the rules of
// D-198 do not allow (building → ready | failed; ready | failed → removed).
var ErrImageVersionState = errors.New("store: image version status change not allowed")

// AddImageVersion stores a new version of v.Image in the state building
// with the next free number. The number counts up per Image name and is
// never used again (rows are never deleted). digest makes the build
// digest from the number (D-198). Status, Version, StartedAt and Digest of
// v are set here.
func (s *Store) AddImageVersion(ctx context.Context, v ImageVersion, digest func(version int) string) (ImageVersion, error) {
	started := s.now()
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var busy int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_version WHERE image = ? AND status = 'building'`, v.Image).Scan(&busy); err != nil {
			return err
		}
		if busy > 0 {
			return ErrBuildRunning
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM image_version WHERE image = ?`, v.Image).Scan(&v.Version); err != nil {
			return err
		}
		v.Digest = digest(v.Version)
		_, err := tx.ExecContext(ctx,
			`INSERT INTO image_version (image, version, status, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb, started_at)
			 VALUES (?, ?, 'building', ?, ?, ?, ?, ?, ?, ?, ?)`,
			v.Image, v.Version, v.Trigger, v.RunID, v.RevisionID, v.RecipeSHA256, v.BaseSHA256, v.Digest, v.DiskGB, started)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrBuildRunning) {
			return ImageVersion{}, err
		}
		return ImageVersion{}, fmt.Errorf("store: add image version: %w", err)
	}
	v.Status = ImageBuilding
	v.StartedAt, _ = parseTime(started)
	v.FinishedAt, v.RemovedAt = time.Time{}, time.Time{}
	return v, nil
}

// FinishImageVersion ends a build: status is ImageReady or ImageFailed.
func (s *Store) FinishImageVersion(ctx context.Context, image string, version int, status string) error {
	if status != ImageReady && status != ImageFailed {
		return ErrImageVersionState
	}
	return s.setImageStatus(ctx, image, version, status,
		`UPDATE image_version SET status = ?, finished_at = ? WHERE image = ? AND version = ? AND status = 'building'`)
}

// RemoveImageVersion marks a ready or failed version as removed: its
// template is gone (retention, or a template that a create or the
// retention found missing).
func (s *Store) RemoveImageVersion(ctx context.Context, image string, version int) error {
	return s.setImageStatus(ctx, image, version, ImageRemoved,
		`UPDATE image_version SET status = ?, removed_at = ? WHERE image = ? AND version = ? AND status IN ('ready', 'failed')`)
}

func (s *Store) setImageStatus(ctx context.Context, image string, version int, status, query string) error {
	res, err := s.db.ExecContext(ctx, query, status, s.now(), image, version)
	if err != nil {
		return fmt.Errorf("store: image version: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	if _, err := s.GetImageVersion(ctx, image, version); err != nil {
		return err
	}
	return ErrImageVersionState
}

const imageVersionColumns = `image, version, status, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb,
	started_at, finished_at, removed_at`

func scanImageVersion(row interface{ Scan(...any) error }) (ImageVersion, error) {
	var v ImageVersion
	var started string
	var finished, removed sql.NullString
	err := row.Scan(&v.Image, &v.Version, &v.Status, &v.Trigger, &v.RunID, &v.RevisionID, &v.RecipeSHA256, &v.BaseSHA256,
		&v.Digest, &v.DiskGB, &started, &finished, &removed)
	if errors.Is(err, sql.ErrNoRows) {
		return ImageVersion{}, ErrNotFound
	}
	if err != nil {
		return ImageVersion{}, err
	}
	if v.StartedAt, err = parseTime(started); err != nil {
		return ImageVersion{}, err
	}
	if v.FinishedAt, err = nullTime(finished); err != nil {
		return ImageVersion{}, err
	}
	v.RemovedAt, err = nullTime(removed)
	return v, err
}

// GetImageVersion returns one version.
func (s *Store) GetImageVersion(ctx context.Context, image string, version int) (ImageVersion, error) {
	return scanImageVersion(s.db.QueryRowContext(ctx,
		`SELECT `+imageVersionColumns+` FROM image_version WHERE image = ? AND version = ?`, image, version))
}

// ImageVersions returns every version of an Image, newest first.
func (s *Store) ImageVersions(ctx context.Context, image string) ([]ImageVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+imageVersionColumns+` FROM image_version WHERE image = ? ORDER BY version DESC`, image)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImageVersion
	for rows.Next() {
		v, err := scanImageVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// BuildingImageVersions returns the versions in the state building of all
// Images: Recover fails them after a restart (D-203).
func (s *Store) BuildingImageVersions(ctx context.Context) ([]ImageVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+imageVersionColumns+` FROM image_version WHERE status = 'building' ORDER BY image`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImageVersion
	for rows.Next() {
		v, err := scanImageVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ImageScheduleSlot returns the last planned schedule slot of an Image,
// or the zero time (D-210).
func (s *Store) ImageScheduleSlot(ctx context.Context, image string) (time.Time, error) {
	var slot string
	err := s.db.QueryRowContext(ctx, `SELECT last_slot FROM image_schedule WHERE image = ?`, image).Scan(&slot)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return parseTime(slot)
}

// SetImageScheduleSlot records the last planned slot of an Image.
func (s *Store) SetImageScheduleSlot(ctx context.Context, image string, slot time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO image_schedule (image, last_slot) VALUES (?, ?)
		 ON CONFLICT (image) DO UPDATE SET last_slot = excluded.last_slot`, image, formatTime(slot))
	return err
}

// ImageVersionsOfRun returns the versions that a Run builds or built.
func (s *Store) ImageVersionsOfRun(ctx context.Context, runID int64) ([]ImageVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+imageVersionColumns+` FROM image_version WHERE run_id = ? ORDER BY image`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImageVersion
	for rows.Next() {
		v, err := scanImageVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
