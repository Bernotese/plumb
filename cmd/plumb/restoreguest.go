package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/proxmox"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// guestRestorer is plan.GuestRestorer for the Proxmox driver (T-222): the
// volumes of a guest as backup.Classify sees them (only a volume that a job
// of this controller recorded is plumb, M1 of #331), and the restore.
type guestRestorer struct {
	d       *proxmox.Driver
	st      *store.Store
	storage string // PLUMB_PVE_GUEST_BACKUP_STORAGE
	install string // PLUMB_INSTALL_ID
	// readOnly is restored mode: the raw driver has no guard, so the
	// restore checks it itself (D-303).
	readOnly func() bool
	// guests is the job runner (T-221): the safety backup runs as one of
	// its jobs (D-420). A func: serve makes the runner after the engine.
	guests func() *backup.Guests
}

// guestRestorerOf returns the restorer, or nil without Proxmox or without
// a guest backup storage: then every restore is refused.
func guestRestorerOf(drv driver.Driver, st *store.Store, x proxmox.InstallExtras, install string, readOnly func() bool, guests func() *backup.Guests) plan.GuestRestorer {
	pd, ok := drv.(*proxmox.Driver)
	if !ok || x.GuestBackupStorage == "" || install == "" {
		return nil
	}
	return guestRestorer{d: pd, st: st, storage: x.GuestBackupStorage, install: install, readOnly: readOnly, guests: guests}
}

func (g guestRestorer) FindBackup(ctx context.Context, target, ref, id string) (plan.RestoreVolume, error) {
	b, err := g.st.GetBinding(ctx, state.KindTarget, target)
	if err != nil {
		return plan.RestoreVolume{}, err
	}
	if b.Ref != ref {
		return plan.RestoreVolume{}, plan.ErrBackupNotFound
	}
	lineage, err := g.st.BackupLineage(ctx, target, b.Gen)
	if err != nil {
		return plan.RestoreVolume{}, err
	}
	vols, err := guestDriver{g.d}.ListGuestBackups(ctx, g.storage, ref)
	if errors.Is(err, backup.ErrGuestGone) {
		return plan.RestoreVolume{}, plan.ErrGuestGone // S3 of T-225
	}
	if err != nil {
		return plan.RestoreVolume{}, err
	}
	rows, err := g.st.RecordedGuestBackups(ctx, target)
	if err != nil {
		return plan.RestoreVolume{}, err
	}
	rec := make(map[string]backup.Recorded, len(rows))
	for v, r := range rows {
		rec[v] = backup.Recorded{Lineage: r.Lineage, Trigger: r.Trigger, Size: r.Size, CTime: r.CTime}
	}
	for _, c := range backup.Classify(vols, g.install, target, lineage, rec) {
		if c.ID == id {
			return plan.RestoreVolume{ID: c.ID, Volid: c.Volid, Kind: c.Kind, Notes: c.Notes, Time: c.Time, Size: c.Size}, nil
		}
	}
	return plan.RestoreVolume{}, plan.ErrBackupNotFound
}

func (g guestRestorer) RestoreGuest(ctx context.Context, ref string, v plan.RestoreVolume) error {
	if g.readOnly != nil && g.readOnly() {
		return fmt.Errorf("%w: %s (%w)", plan.ErrRestoreNotStarted, "restored mode: Plumb sends no Proxmox write", driver.ErrReadOnly)
	}
	err := g.d.RestoreGuest(ctx, g.storage, ref, v.Volid, v.Size, v.Time.Unix())
	if errors.Is(err, proxmox.ErrRestoreNotStarted) {
		return fmt.Errorf("%w: %s", plan.ErrRestoreNotStarted, notStartedReason(err))
	}
	return err
}

// notStartedReason is Plumb's text of a restore that Proxmox did not begin
// (M1 of #346): never the raw answer.
func notStartedReason(err error) string {
	switch {
	case errors.Is(err, proxmox.ErrNetworkRefused):
		return "Proxmox refused it before it changed anything: a NIC of the backup is on a bridge or VLAN tag that the install does not name (no SDN.Use). Run install.sh with that network (a repair run), or restore another backup"
	case errors.Is(err, proxmox.ErrBackupChanged):
		return "the backup volume changed since it was checked"
	case errors.Is(err, proxmox.ErrBackupNotFound):
		return "the backup volume is not listed for this guest any more"
	case errors.Is(err, proxmox.ErrGuestRunning):
		return "the guest was not stopped"
	case errors.Is(err, driver.ErrNotFound):
		return "the guest is not in the pool any more"
	}
	var ae *proxmox.APIError
	if errors.As(err, &ae) {
		return fmt.Sprintf("Proxmox refused the restore (HTTP %d)", ae.Status)
	}
	return "the guest or its backups could not be read"
}

// SafetyBackup runs the pre-restore job of the Target (D-420).
func (g guestRestorer) SafetyBackup(ctx context.Context, target string) (plan.RestoreVolume, error) {
	jobs := g.guests()
	if jobs == nil {
		return plan.RestoreVolume{}, errors.New("this controller has no guest backup jobs")
	}
	c, err := jobs.SafetyBackup(ctx, target, "system")
	if err != nil {
		return plan.RestoreVolume{}, err
	}
	return plan.RestoreVolume{ID: c.ID, Volid: c.Volid, Kind: c.Kind, Notes: c.Notes, Time: c.Time, Size: c.Size}, nil
}

// BackupConfig reads the guest config inside a backup volume of the guest
// ref (vzdump/extractconfig, T-244 R2), for the checks before a restore
// stops the guest (T-241 networks, T-238 data disks).
func (g guestRestorer) BackupConfig(ctx context.Context, ref string, v plan.RestoreVolume) (map[string]string, error) {
	return g.d.BackupConfig(ctx, g.storage, ref, v.Volid)
}

// DropSafetyBackups deletes the older pre-restore volumes (D-420).
func (g guestRestorer) DropSafetyBackups(ctx context.Context, target, keep string) error {
	jobs := g.guests()
	if jobs == nil {
		return nil
	}
	return jobs.DropSafetyBackups(ctx, target, keep, "system")
}

var (
	_ plan.GuestRestorer      = guestRestorer{}
	_ plan.BackupConfigReader = guestRestorer{}
)
