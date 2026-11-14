package main

import (
	"context"
	"time"

	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/driver/proxmox"
	"github.com/Bernotese/plumb/internal/plan"
)

// The wiring of the guest backup jobs (T-221): the Proxmox driver and the
// engine's Targets for internal/backup, which may import neither.

// guestDriver adapts the Proxmox driver to backup.GuestDriver.
type guestDriver struct{ d *proxmox.Driver }

func (g guestDriver) BackUpGuest(ctx context.Context, storage, guest, notes string, exclude []string) (string, bool, error) {
	r, err := g.d.BackUpGuest(ctx, storage, guest, notes, exclude)
	return r.Volid, r.Warned, err
}

func (g guestDriver) ListGuestBackups(ctx context.Context, storage, guest string) ([]backup.Volume, error) {
	vols, err := g.d.ListGuestBackups(ctx, storage, guest)
	if err != nil {
		return nil, err
	}
	out := make([]backup.Volume, 0, len(vols))
	for _, v := range vols {
		out = append(out, backup.Volume{Volid: v.Volid, Time: time.Unix(v.CTime, 0).UTC(), Size: v.Size, Notes: v.Notes})
	}
	return out, nil
}

func (g guestDriver) DeleteGuestBackup(ctx context.Context, storage, guest, volid string) error {
	return g.d.DeleteGuestBackup(ctx, storage, guest, volid)
}

func (g guestDriver) BackupStorageState(ctx context.Context, storage string) (backup.StorageState, error) {
	st, err := g.d.BackupStorageInfo(ctx, storage)
	return backup.StorageState{Encrypted: st.Encrypted, FreePct: st.FreePct}, err
}

var _ backup.GuestDriver = guestDriver{}

var weekdays = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}

// guestTargetOf converts the engine's view.
func guestTargetOf(t plan.BackupTarget) backup.GuestTarget {
	g := backup.GuestTarget{Name: t.Name, Guest: t.Ref, Gen: t.Gen, LXC: t.LXC}
	if t.Backup != nil {
		g.Backup = &backup.GuestSchedule{Schedule: t.Backup.Schedule, Weekday: weekdays[t.Backup.Weekday],
			Hour: t.Backup.Hour, Minute: t.Backup.Minute, Keep: t.Backup.Keep}
	}
	return g
}

// guestTargets gives backup.GuestConfig its Target and Targets funcs.
func guestTargets(eng *plan.Engine) (one func(context.Context, string) (backup.GuestTarget, error), all func(context.Context) ([]backup.GuestTarget, error)) {
	one = func(ctx context.Context, name string) (backup.GuestTarget, error) {
		t, err := eng.BackupTarget(ctx, name)
		return guestTargetOf(t), err
	}
	all = func(ctx context.Context) ([]backup.GuestTarget, error) {
		ts, err := eng.BackupTargets(ctx)
		out := make([]backup.GuestTarget, 0, len(ts))
		for _, t := range ts {
			out = append(out, guestTargetOf(t))
		}
		return out, err
	}
	return one, all
}
