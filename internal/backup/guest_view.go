package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Bernotese/plumb/internal/store"
)

// GuestList is GuestBackupList of the contract (T-217): no notes and no
// volume id ever leave it (a token reads it).
type GuestList struct {
	Storage *GuestStorage
	Items   []GuestItem
	NextAt  time.Time // zero: none
	Last    *store.GuestBackupRun
}

// GuestStorage is GuestBackupList.storage.
type GuestStorage struct {
	ID      string
	State   string
	FreePct int // -1: unknown
}

// GuestItem is GuestBackup of the contract.
type GuestItem struct {
	ID, Kind, Trigger, Mode string
	Time                    time.Time
	Size                    int64
}

// List returns the backups of a Target's guest.
func (g *Guests) List(ctx context.Context, target string) (GuestList, error) {
	t, err := g.cfg.Target(ctx, target)
	if err != nil {
		return GuestList{}, err
	}
	out := GuestList{Items: []GuestItem{}}
	if last, err := g.cfg.Store.LastGuestBackupRun(ctx, t.Name); err == nil {
		out.Last = &last
	} else if !errors.Is(err, store.ErrNotFound) {
		return GuestList{}, err
	}
	if t.Backup != nil {
		out.NextAt = NextAt(*t.Backup, g.cfg.Now())
	}
	state, free, err := g.storageState(ctx)
	if errors.Is(err, ErrGuestBackupRefused) {
		return out, nil // no storage: storage null, no items
	}
	if err != nil {
		return GuestList{}, err
	}
	out.Storage = &GuestStorage{ID: g.cfg.Storage, State: state, FreePct: free}
	lineage, err := g.cfg.Store.BackupLineage(ctx, t.Name, t.Gen)
	if err != nil {
		return GuestList{}, err
	}
	vols, err := g.cfg.Driver.ListGuestBackups(ctx, g.cfg.Storage, t.Guest)
	if err != nil {
		return GuestList{}, err
	}
	for _, c := range Classify(vols, g.cfg.InstallID, t.Name, lineage) {
		mode := "unknown"
		if run, err := g.cfg.Store.GuestBackupRunOfVolume(ctx, t.Name, c.Volid); err == nil && run.Result != "failed" {
			mode = "snapshot" // every Plumb job runs in snapshot mode
		}
		out.Items = append(out.Items, GuestItem{ID: c.ID, Kind: c.Kind, Trigger: c.Trigger, Mode: mode, Time: c.Time, Size: c.Size})
	}
	return out, nil
}

// Errors of a delete.
var (
	ErrGuestBackupNotFound = errors.New("backup: no such backup of this Target")
	// ErrNotPlumbBackup: an unmarked volume is never deleted by Plumb (409
	// not-plumb).
	ErrNotPlumbBackup = errors.New("backup: the volume has no Plumb marker of this install")
)

// Delete deletes one volume of the Target's guest by its id (D-409). The
// caller makes sure that a person (an admin session) asks. Only plumb and
// older-guest volumes of this install's marker.
func (g *Guests) Delete(ctx context.Context, target, id, actor string) error {
	t, err := g.cfg.Target(ctx, target)
	if err != nil {
		return err
	}
	if _, _, err := g.storageState(ctx); err != nil {
		return err
	}
	lineage, err := g.cfg.Store.BackupLineage(ctx, t.Name, t.Gen)
	if err != nil {
		return err
	}
	vols, err := g.cfg.Driver.ListGuestBackups(ctx, g.cfg.Storage, t.Guest)
	if err != nil {
		return err
	}
	for _, c := range Classify(vols, g.cfg.InstallID, t.Name, lineage) {
		if c.ID != id {
			continue
		}
		if c.Kind == KindUnmarked {
			return ErrNotPlumbBackup
		}
		if err := g.cfg.Driver.DeleteGuestBackup(ctx, g.cfg.Storage, t.Guest, c.Volid); err != nil {
			return fmt.Errorf("backup: delete: %w", err)
		}
		g.emit(ctx, "guest_backup.deleted", t.Name, actor, map[string]any{"backup": c.ID, "kind": c.Kind})
		return nil
	}
	return ErrGuestBackupNotFound
}

// NextAt is the next slot of a schedule after now (UTC).
func NextAt(s GuestSchedule, now time.Time) time.Time {
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, s.Minute, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		c := day.AddDate(0, 0, i)
		if !c.After(now) {
			continue
		}
		if s.Schedule == "weekly" && c.Weekday() != s.Weekday {
			continue
		}
		return c
	}
	return time.Time{}
}

// lastSlot is the newest slot at or before now (zero if none in a week).
func lastSlot(s GuestSchedule, now time.Time) time.Time {
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, s.Minute, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		c := day.AddDate(0, 0, -i)
		if c.After(now) {
			continue
		}
		if s.Schedule == "weekly" && c.Weekday() != s.Weekday {
			continue
		}
		return c
	}
	return time.Time{}
}

// Tick runs the jobs whose slot came since the last tick (one per Target
// and slot). A slot that passed while the controller was down is not
// made up for; the next one runs. The first tick after a start only
// learns the slots.
func (g *Guests) Tick(ctx context.Context) {
	targets, err := g.cfg.Targets(ctx)
	if err != nil {
		g.cfg.Logger.Error("guest backup schedule", "err", err)
		return
	}
	now := g.cfg.Now()
	for _, t := range targets {
		if t.Backup == nil {
			continue
		}
		slot := lastSlot(*t.Backup, now)
		g.mu.Lock()
		prev, seen := g.last[t.Name]
		g.last[t.Name] = slot
		g.mu.Unlock()
		if !seen || slot.IsZero() || !slot.After(prev) {
			continue
		}
		if err := g.BackUp(ctx, t.Name, TriggerScheduled, "system"); err != nil && ctx.Err() == nil {
			g.cfg.Logger.Warn("scheduled guest backup", "target", t.Name, "err", err)
		}
	}
}

// RunSchedule ticks every interval (serve: a minute) until ctx ends.
func (g *Guests) RunSchedule(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		g.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
