package main

import (
	"context"
	"errors"

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
}

// guestRestorerOf returns the restorer, or nil without Proxmox or without
// a guest backup storage: then every restore is refused.
func guestRestorerOf(drv driver.Driver, st *store.Store, x proxmox.InstallExtras, install string, readOnly func() bool) plan.GuestRestorer {
	pd, ok := drv.(*proxmox.Driver)
	if !ok || x.GuestBackupStorage == "" || install == "" {
		return nil
	}
	return guestRestorer{d: pd, st: st, storage: x.GuestBackupStorage, install: install, readOnly: readOnly}
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
		return driver.ErrReadOnly
	}
	err := g.d.RestoreGuest(ctx, g.storage, ref, v.Volid, v.Size, v.Time.Unix())
	if errors.Is(err, proxmox.ErrBackupNotFound) {
		return plan.ErrBackupNotFound
	}
	return err
}

var _ plan.GuestRestorer = guestRestorer{}
