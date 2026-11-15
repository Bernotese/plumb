package proxmox

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
)

// T-241 (T-244 part 3): the guest config inside a backup volume, read
// with the token before a restore, and PVE's refusal of a restore whose
// network the token may not use (it refuses before it touches a disk).

// ErrNetworkRefused: PVE refused a restore because the token has no
// SDN.Use on a bridge or VLAN tag of the backup (T-244 R1). PVE checks it
// in the restore task before it touches a disk, so nothing changed.
var ErrNetworkRefused = errors.New("proxmox: the restore was refused: a network of the backup is not allowed for the token")

// sdnRefused reports a restore task that ended with PVE's SDN.Use
// permission error ("403 Permission check failed (/sdn/…, SDN.Use)").
func sdnRefused(err error) bool {
	var te *TaskError
	if !errors.As(err, &te) {
		return false
	}
	for _, l := range append([]string{te.ExitStatus}, te.Log...) {
		if strings.Contains(l, "Permission check failed (/sdn/") && strings.Contains(l, "SDN.Use") {
			return true
		}
	}
	return false
}

// BackupConfig returns the guest config in a backup volume of a pool
// guest (vzdump/extractconfig; the token may, T-244 R2): the keys of the
// current config, without the snapshot sections. The volume must be in the
// guest's own backup list (never a volume of another VMID). T-238 reads
// the data disks from it, T-241 the networks.
func (d *Driver) BackupConfig(ctx context.Context, storage, guest, volid string) (map[string]string, error) {
	id, _, err := d.own(ctx, guest)
	if err != nil {
		return nil, err
	}
	vols, err := d.listBackups(ctx, storage, id)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(vols, func(v BackupVolume) bool { return v.Volid == volid }) {
		return nil, ErrBackupNotFound
	}
	var conf string
	if err := d.c.Get(ctx, "/nodes/"+url.PathEscape(d.cfg.Node)+"/vzdump/extractconfig", url.Values{"volume": {volid}}, &conf); err != nil {
		return nil, err
	}
	return parseGuestConfig(conf), nil
}

// parseGuestConfig reads "key: value" lines up to the first snapshot
// section ("[name]"). Comment lines (the description) are left out.
func parseGuestConfig(conf string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "[") {
			break // a snapshot section: not what the restore makes
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}
