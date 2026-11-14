package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/store"
)

// The guest backup jobs (T-221; D-405 … D-409, D-417). A job is no Plan:
// the approved backup field is the desired state, and a job changes no
// guest. serve wires the driver and the Targets in; this package knows
// neither the engine nor the Proxmox driver (ARCHITECTURE §2).

// GuestDriver is the part of the Proxmox driver that jobs use.
type GuestDriver interface {
	BackUpGuest(ctx context.Context, storage, guest, notes string, excludePaths []string) (volid string, warned bool, err error)
	ListGuestBackups(ctx context.Context, storage, guest string) ([]Volume, error)
	DeleteGuestBackup(ctx context.Context, storage, guest, volid string) error
	BackupStorageState(ctx context.Context, storage string) (StorageState, error)
}

// StorageState is the guest backup storage as the token sees it.
type StorageState struct {
	Encrypted bool // PBS with client-side encryption
	FreePct   int  // -1: unknown
}

// Storage states (GuestBackupList.storage.state).
const (
	StoragePBSKey      = "pbs-key"
	StorageStatedClear = "stated-clear"
	StorageClearRefuse = "clear-refused"
)

// GuestTarget is one bound Target as the jobs need it.
type GuestTarget struct {
	Name  string
	Guest string // the binding ref (VMID)
	Gen   int64  // the binding generation (D-397)
	LXC   bool
	// Backup is the Target's backup field; nil: no Plumb backups.
	Backup *GuestSchedule
}

// GuestSchedule is a Target's backup field.
type GuestSchedule struct {
	Schedule string // daily | weekly
	Weekday  time.Weekday
	Hour     int
	Minute   int
	Keep     int
}

// GuestConfig wires the jobs.
type GuestConfig struct {
	Store     *store.Store
	Events    *event.Log
	Driver    GuestDriver // nil: no Proxmox (serve --dev): every job is refused
	InstallID string
	Storage   string // the install's guest backup storage; "": none
	Clear     bool   // --guest-backup-clear was given (D-406)
	Restored  func() bool
	// Target returns one bound Target; store.ErrNotFound when the name is
	// not a bound Target.
	Target func(ctx context.Context, name string) (GuestTarget, error)
	// Targets returns every bound Target (for the schedule).
	Targets func(ctx context.Context) ([]GuestTarget, error)
	Now     func() time.Time
	Logger  *slog.Logger
}

// Guests runs the guest backup jobs.
type Guests struct {
	cfg  GuestConfig
	mu   sync.Mutex
	last map[string]time.Time // the last scheduled slot that ran, per Target
	// base ends the background jobs at Close; wg waits for them.
	base   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewGuests returns the job runner.
func NewGuests(cfg GuestConfig) *Guests {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Restored == nil {
		cfg.Restored = func() bool { return false }
	}
	base, cancel := context.WithCancel(context.Background())
	return &Guests{cfg: cfg, last: map[string]time.Time{}, base: base, cancel: cancel}
}

// Background runs a started job without the caller's request context; a
// job that runs at Close is stopped (its Proxmox task too) and fails.
func (g *Guests) Background(target string, run func(context.Context) error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		if err := run(g.base); err != nil && g.base.Err() == nil {
			g.cfg.Logger.Warn("guest backup", "target", target, "err", err)
		}
	}()
}

// Wait waits for the background jobs (tests).
func (g *Guests) Wait() { g.wg.Wait() }

// Close stops and waits for the background jobs.
func (g *Guests) Close() {
	g.cancel()
	g.wg.Wait()
}

// Errors of a job request.
var (
	// ErrGuestBackupRefused: the job did not start (the reason is in the
	// error and in the run row).
	ErrGuestBackupRefused = errors.New("backup: guest backup refused")
	// ErrGuestBackupBusy: a job of this Target runs.
	ErrGuestBackupBusy = errors.New("backup: a guest backup of this Target runs")
)

// excludeLXC: the agent's files stay out of a container archive (L1 of
// #321); a VM archive holds the whole disk (D-406).
var excludeLXC = []string{"/etc/plumb-agent", "/var/lib/plumb-agent"}

// minFreePct: below this, a job is refused (D-408).
const minFreePct = 10

// storageState returns the state word and the free share, or the reason
// why no job may run.
func (g *Guests) storageState(ctx context.Context) (string, int, error) {
	if g.cfg.Driver == nil || g.cfg.Storage == "" {
		return "", -1, fmt.Errorf("%w: the install named no guest backup storage", ErrGuestBackupRefused)
	}
	st, err := g.cfg.Driver.BackupStorageState(ctx, g.cfg.Storage)
	if err != nil {
		return "", -1, err
	}
	switch {
	case st.Encrypted:
		return StoragePBSKey, st.FreePct, nil
	case g.cfg.Clear:
		return StorageStatedClear, st.FreePct, nil
	}
	return StorageClearRefuse, st.FreePct, nil
}

// BackUp runs one job for target now and waits for it. trigger is
// TriggerScheduled or TriggerManual; actor is who asked.
func (g *Guests) BackUp(ctx context.Context, target, trigger, actor string) error {
	run, err := g.Start(ctx, target, trigger, actor)
	if err != nil {
		return err
	}
	return run(ctx)
}

// Start checks a job and records it as running (all refusals and the
// one-job-per-Target rule are decided here, at once); run does the
// vzdump, the read-back and retention. "Back up now" answers after Start
// and calls run in the background.
func (g *Guests) Start(ctx context.Context, target, trigger, actor string) (run func(context.Context) error, err error) {
	t, err := g.cfg.Target(ctx, target)
	if err != nil {
		return nil, err
	}
	lineage, err := g.cfg.Store.BackupLineage(ctx, t.Name, t.Gen)
	if err != nil {
		return nil, err
	}
	refuse := func(msg string) (func(context.Context) error, error) {
		if err := g.cfg.Store.RefusedGuestBackupRun(ctx, t.Name, lineage, trigger, msg); err != nil {
			return nil, err
		}
		g.emit(ctx, "guest_backup.failed", t.Name, actor, map[string]any{"result": "refused", "trigger": trigger})
		return nil, fmt.Errorf("%w: %s", ErrGuestBackupRefused, msg)
	}
	if g.cfg.Restored() {
		return refuse("restored mode: guest backups wait for the restore confirm (D-303)")
	}
	state, free, err := g.storageState(ctx)
	if errors.Is(err, ErrGuestBackupRefused) {
		return refuse("the install named no guest backup storage")
	}
	if err != nil {
		return refuse("the guest backup storage cannot be read")
	}
	if state == StorageClearRefuse {
		return refuse("the storage keeps backups in clear, and the install did not allow that (--guest-backup-clear, D-406)")
	}
	if free >= 0 && free < minFreePct {
		return refuse(fmt.Sprintf("the storage has %d %% free, less than %d %%", free, minFreePct))
	}
	marker, err := Marker{Install: g.cfg.InstallID, Target: t.Name, Lineage: lineage, Manual: trigger == TriggerManual}.String()
	if err != nil {
		return refuse("this controller has no install id, so its backups cannot be marked")
	}
	id, err := g.cfg.Store.StartGuestBackupRun(ctx, t.Name, lineage, trigger)
	if errors.Is(err, store.ErrGuestBackupRunning) {
		return nil, ErrGuestBackupBusy
	}
	if err != nil {
		return nil, err
	}
	g.emit(ctx, "guest_backup.started", t.Name, actor, map[string]any{"trigger": trigger})
	return func(ctx context.Context) error { return g.run(ctx, id, t, lineage, marker, trigger, actor) }, nil
}

func (g *Guests) run(ctx context.Context, id int64, t GuestTarget, lineage, marker, trigger, actor string) error {
	var exclude []string
	if t.LXC {
		exclude = excludeLXC
	}
	volid, warned, err := g.cfg.Driver.BackUpGuest(ctx, g.cfg.Storage, t.Guest, marker, exclude)
	if err != nil {
		g.finish(ctx, id, t.Name, actor, trigger, "failed", "", "the backup failed in Proxmox (see the task log there)")
		return fmt.Errorf("backup: guest backup of %s: %w", t.Name, err)
	}
	// vzdump only warns when the notes fail (T-214): read the marker back.
	result, msg := "done", ""
	vols, err := g.cfg.Driver.ListGuestBackups(ctx, g.cfg.Storage, t.Guest)
	marked := false
	if err == nil {
		i := slices.IndexFunc(vols, func(v Volume) bool { return v.Volid == volid })
		if i >= 0 {
			m, ok := ParseMarker(vols[i].Notes)
			marked = ok && m.Install == g.cfg.InstallID && m.Target == t.Name && m.Lineage == lineage
		}
	}
	if !marked {
		result, msg = "warning", "the backup has no Plumb marker (the notes could not be written): Plumb never deletes or restores it"
	} else if warned {
		msg = "Proxmox reported warnings"
	}
	g.finish(ctx, id, t.Name, actor, trigger, result, volid, msg)
	if err == nil && t.Backup != nil {
		g.retain(ctx, t, lineage, vols)
	}
	return nil
}

func (g *Guests) finish(ctx context.Context, id int64, target, actor, trigger, result, volid, msg string) {
	if err := g.cfg.Store.FinishGuestBackupRun(ctx, id, result, volid, msg); err != nil {
		g.cfg.Logger.Error("finish guest backup run", "target", target, "err", err)
	}
	typ := "guest_backup.done"
	if result == "failed" {
		typ = "guest_backup.failed"
	}
	g.emit(ctx, typ, target, actor, map[string]any{"result": result, "trigger": trigger})
}

// retain deletes the Target's own volumes above keep (scheduled) and above
// MaxManualKept (manual). Nothing else (D-407, D-417).
func (g *Guests) retain(ctx context.Context, t GuestTarget, lineage string, vols []Volume) {
	for _, v := range Retention(Classify(vols, g.cfg.InstallID, t.Name, lineage), t.Backup.Keep) {
		if err := g.cfg.Driver.DeleteGuestBackup(ctx, g.cfg.Storage, t.Guest, v.Volid); err != nil {
			g.cfg.Logger.Warn("guest backup retention", "target", t.Name, "err", err)
			continue
		}
		g.emit(ctx, "guest_backup.deleted", t.Name, "system", map[string]any{"backup": v.ID, "reason": "retention"})
	}
}

func (g *Guests) emit(ctx context.Context, typ, target, actor string, data map[string]any) {
	if g.cfg.Events == nil {
		return
	}
	if _, err := g.cfg.Events.Emit(ctx, typ, "target:"+target, actor, data); err != nil {
		g.cfg.Logger.Error("event "+typ, "err", err)
	}
}
