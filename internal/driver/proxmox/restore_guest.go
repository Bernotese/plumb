package proxmox

// Restore of a guest backup over the same guest (slice 7, T-222, D-410,
// D-411). Only over a guest of the pool, only a volume of that guest that
// the backup storage lists for it (check_volume_access: the token sees the
// backups of pool guests only, T-214 FACT 1), only while the guest is
// stopped. force=1 overwrites the guest; the VMID, the pool membership and
// (without unique) the MAC addresses stay. The disks go to the disk storage
// of the install, where the token has Datastore.AllocateSpace (D-081).
// No other parameter: no bwlimit, no pool, no unique (D-405). To prove on
// the host: C9.6.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"

	"github.com/Bernotese/plumb/internal/driver"
)

// ErrGuestRunning: a restore needs a stopped guest; the caller stops it.
var ErrGuestRunning = errors.New("proxmox: the guest runs; a restore needs it stopped")

// ErrRestoreNotStarted wraps every error before Proxmox began the restore:
// a check of the driver, or an HTTP refusal of the POST. The guest's disks
// are as before (M1 of #346). A failed task, or a call whose answer was
// lost, is not wrapped: then the disks may be gone.
var ErrRestoreNotStarted = errors.New("proxmox: the restore did not start")

func notStarted(err error) error { return fmt.Errorf("%w: %w", ErrRestoreNotStarted, err) }

// ErrBackupChanged: the volume has another size or time than the one the
// caller checked (a replaced archive, F2 of #331, review L1 of #335).
var ErrBackupChanged = errors.New("proxmox: the backup volume changed since it was checked")

// RestoreGuest writes the backup volume volid of storage over the guest.
// size and ctime are what the caller checked: the list read right before
// the call must show the same, so the window is one API call.
func (d *Driver) RestoreGuest(ctx context.Context, storage, guest, volid string, size, ctime int64) error {
	id, m, err := d.own(ctx, guest)
	if err != nil {
		return notStarted(err)
	}
	st, err := d.Observe(ctx, driver.GuestID(guest))
	if err != nil {
		return notStarted(err)
	}
	if st.Power != driver.PowerStopped {
		return notStarted(ErrGuestRunning)
	}
	vols, err := d.listBackups(ctx, storage, id)
	if err != nil {
		return notStarted(err)
	}
	i := slices.IndexFunc(vols, func(v BackupVolume) bool { return v.Volid == volid })
	if i < 0 {
		return notStarted(ErrBackupNotFound)
	}
	if vols[i].Size != size || vols[i].CTime != ctime {
		return notStarted(ErrBackupChanged)
	}
	// H1 of #369: a VM's disks before the restore, to prove a refusal by
	// state. T-244 R1 proved PVE's order (SDN check before any disk) for a
	// VM only; a container stays on the normal failure path (T-247).
	var before map[string]string
	if m.Type != typeCT {
		if before, err = d.vmDisks(ctx, id); err != nil {
			return notStarted(err)
		}
	}
	params, path := restoreParams(d.cfg.Node, d.cfg.DiskStorage, m.Type == typeCT, id, volid)
	upid, err := d.c.start(ctx, "POST", path, params)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) { // Proxmox answered and refused: no task
			return notStarted(err)
		}
		return fmt.Errorf("proxmox: restore of %s over %d: %w", volid, id, err)
	}
	if upid == "" {
		return nil
	}
	if err := d.c.WaitTask(ctx, d.cfg.Node, upid); err != nil {
		if before != nil && sdnRefused(err) {
			// T-244 R1: PVE checks SDN.Use of the backup's networks in the
			// task, before it touches a disk. "Not started" only when the
			// VM's disk volumes read back the same as before.
			if after, rerr := d.vmDisks(ctx, id); rerr == nil && maps.Equal(before, after) {
				return notStarted(fmt.Errorf("%w: %w", ErrNetworkRefused, err))
			}
		}
		return fmt.Errorf("proxmox: restore of %s over %d: %w", volid, id, err)
	}
	return nil
}

// restoreParams are the call of a restore: POST /nodes/<n>/qemu with
// archive, or POST /nodes/<n>/lxc with ostemplate and restore=1.
func restoreParams(node, diskStorage string, ct bool, id int, volid string) (url.Values, string) {
	params := url.Values{"vmid": {strconv.Itoa(id)}, "force": {"1"}, "storage": {diskStorage}}
	if ct {
		params.Set("ostemplate", volid)
		params.Set("restore", "1")
		params.Set("unprivileged", "1") // D-227: never a privileged container
		return params, "/nodes/" + url.PathEscape(node) + "/lxc"
	}
	params.Set("archive", volid)
	return params, "/nodes/" + url.PathEscape(node) + "/qemu"
}
