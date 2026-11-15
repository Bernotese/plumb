package backup

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// T-248 (host finding F1 of T-244): a job can end without a recorded
// volume although its own vzdump made one. The paths:
//   - another vzdump of the guest ended in the job's window, so the list
//     before and after the task has more than one new volume (ErrManyNew);
//   - the controller stopped during the task (Close, or a crash that
//     Recover ends): the task may still end OK in Proxmox;
//   - the task ended with an error after it wrote its archive.
// Each such job keeps its task (the UPID, recorded right after the start)
// to be checked. settle claims a volume for the job only with proof: the
// job's own task ended OK (or with warnings), its log names exactly one
// volume of this guest, that volume carries this job's exact marker, it
// is not older than the job, and no other job recorded it. A marker alone
// proves nothing (M1 of #331); anything less claims nothing, and the job's
// message says what stays and that a person deletes it in Proxmox.

// taskCheckFor is how long settle tries to read a task that it cannot
// read (a Proxmox error), before it gives up and claims nothing.
const taskCheckFor = 24 * time.Hour

// markerSkew: a volume's ctime may be this much before the job's start
// (the clocks of Proxmox and the controller).
const markerSkew = 2 * time.Minute

// settle checks the jobs of target ("" every Target) whose task is still
// to be checked. running is the UPID of a task of target that still runs
// (and has run for less than taskCheckFor), or "".
func (g *Guests) settle(ctx context.Context, target string) (running string, err error) {
	rows, err := g.cfg.Store.UncheckedGuestBackupTasks(ctx, target)
	if err != nil {
		return "", err
	}
	var errs []error
	for _, r := range rows {
		run, err := g.settleOne(ctx, r.ID, r.Target, r.Lineage, r.Trigger, r.UPID, r.StartedAt, r.FinishedAt)
		if run && running == "" {
			running = r.UPID
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return running, errors.Join(errs...)
}

func (g *Guests) settleOne(ctx context.Context, id int64, target, lineage, trigger, upid string, started, finished time.Time) (bool, error) {
	end := func(volid string, size, ctime int64, msg string) error {
		ok, err := g.cfg.Store.SettleGuestBackupTask(ctx, id, volid, size, ctime, msg)
		if err == nil && ok && volid != "" {
			g.emit(ctx, "guest_backup.done", target, "system", map[string]any{"result": "done", "trigger": trigger, "completed_later": true})
		}
		return err
	}
	const stays = "; it stays unmarked: Plumb never restores or deletes it, a person deletes it in Proxmox"
	if g.cfg.Driver == nil {
		return false, nil
	}
	t, err := g.cfg.Target(ctx, target)
	if err != nil {
		// The Target is gone or unbound: its volumes are no longer its own.
		return false, end("", 0, 0, "the job's task was not checked: the Target is no longer bound; Plumb claims no volume")
	}
	task, err := g.cfg.Driver.GuestBackupTask(ctx, g.cfg.Storage, t.Guest, upid)
	switch {
	case errors.Is(err, ErrNotGuestTask), errors.Is(err, ErrGuestGone):
		return false, end("", 0, 0, "the job's task is not one of this guest any more (the guest is gone or new); Plumb claims no volume")
	case err != nil:
		if !finished.IsZero() && g.cfg.Now().Sub(finished) > taskCheckFor {
			return false, end("", 0, 0, "the job's Proxmox task could not be read within a day; Plumb claims no volume"+stays)
		}
		return false, fmt.Errorf("backup: check task of %s: %w", target, err)
	case task.Running:
		// M1 of #368: a hung task must not block the guest's jobs for
		// ever (the safety backup of a restore too). After a day it is
		// lost: Plumb no longer waits and never claims its volume.
		if g.cfg.Now().Sub(started) > taskCheckFor {
			return false, end("", 0, 0, "the job's Proxmox task "+upid+" still ran after a day; Plumb no longer waits for it and claims no volume of it. Stop it in Proxmox if it hangs"+stays)
		}
		return true, nil
	}
	vols, err := g.cfg.Driver.ListGuestBackups(ctx, g.cfg.Storage, t.Guest)
	if err != nil {
		return false, fmt.Errorf("backup: check task of %s: %w", target, err)
	}
	i := slices.IndexFunc(vols, func(v Volume) bool { return v.Volid == task.Volid })
	switch {
	case task.Volid == "" || i < 0:
		if !task.OK {
			return false, end("", 0, 0, "the backup failed in Proxmox (see the task log there); it left no volume of this guest")
		}
		return false, end("", 0, 0, "the job's Proxmox task ended, but its log names no single volume of this guest; Plumb claims none"+stays)
	case !task.OK:
		return false, end("", 0, 0, fmt.Sprintf("the backup failed in Proxmox, but its task left volume %s, which may be incomplete; Plumb does not claim it", VolumeID(task.Volid))+stays)
	}
	v := vols[i]
	m, ok := ParseMarker(v.Notes)
	if !ok || m.Install != g.cfg.InstallID || m.Target != target || m.Lineage != lineage || m.Trigger() != trigger {
		return false, end("", 0, 0, fmt.Sprintf("the job's task made volume %s, but it does not carry this job's marker; Plumb does not claim it", VolumeID(v.Volid))+stays)
	}
	if v.Time.Before(started.Add(-markerSkew)) {
		return false, end("", 0, 0, fmt.Sprintf("volume %s is older than the job; Plumb does not claim it", VolumeID(v.Volid))+stays)
	}
	rec, err := g.cfg.Store.RecordedGuestBackups(ctx, target)
	if err != nil {
		return false, err
	}
	if _, taken := rec[v.Volid]; taken {
		return false, end("", 0, 0, fmt.Sprintf("volume %s is already the record of another job; Plumb claims it only once", VolumeID(v.Volid)))
	}
	var ctime int64
	if !v.Time.IsZero() {
		ctime = v.Time.Unix()
	}
	return false, end(v.Volid, v.Size, ctime, "the job ended without its volume, but its own Proxmox task made it and ended OK: Plumb completed the record")
}
