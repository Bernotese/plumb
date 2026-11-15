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
	"net/url"
	"slices"
	"strconv"

	"github.com/Bernotese/plumb/internal/driver"
)

// ErrGuestRunning: a restore needs a stopped guest; the caller stops it.
var ErrGuestRunning = errors.New("proxmox: the guest runs; a restore needs it stopped")

// RestoreGuest writes the backup volume volid of storage over the guest.
func (d *Driver) RestoreGuest(ctx context.Context, storage, guest, volid string) error {
	id, m, err := d.own(ctx, guest)
	if err != nil {
		return err
	}
	vols, err := d.listBackups(ctx, storage, id)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(vols, func(v BackupVolume) bool { return v.Volid == volid }) {
		return ErrBackupNotFound
	}
	st, err := d.Observe(ctx, driver.GuestID(guest))
	if err != nil {
		return err
	}
	if st.Power != driver.PowerStopped {
		return ErrGuestRunning
	}
	params, path := restoreParams(d.cfg.Node, d.cfg.DiskStorage, m.Type == typeCT, id, volid)
	if err := d.c.MaybeTask(ctx, d.cfg.Node, "POST", path, params); err != nil {
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
