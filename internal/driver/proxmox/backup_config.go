package proxmox

import (
	"context"
	"errors"
	"net/url"
	"regexp"
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

// reSDNRefused is PVE's exact end status of a restore that the token may
// not do for a network of the backup (T-244 R1).
var reSDNRefused = regexp.MustCompile(`^403 Permission check failed \(/sdn/zones/[^,()\s]+, SDN\.Use\)$`)

// sdnRefused reports a task that ended with PVE's own SDN.Use permission
// error. Only the task's exit status counts, never a log line: a
// container restore writes file names of the guest's archive into the
// log (H1 of #369).
func sdnRefused(err error) bool {
	var te *TaskError
	return errors.As(err, &te) && reSDNRefused.MatchString(te.ExitStatus)
}

// reDiskKey are the config keys that hold a VM's volumes.
var reDiskKey = regexp.MustCompile(`^(scsi|sata|ide|virtio|unused|efidisk|tpmstate)[0-9]+$`)

// reCTDiskKey are the config keys that hold a container's volumes.
var reCTDiskKey = regexp.MustCompile(`^(rootfs|(mp|unused)[0-9]+)$`)

// guestDisks returns the volumes of a guest's config by key (the volume
// part of each value), to prove later that a refused restore changed no
// disk. T-253: a container too (host fact of T-247 3c: PVE refuses the
// network before it touches the rootfs). A compare of names cannot see a
// volume destroyed and made again under the same name; for a VM the proof
// also rests on the task's exit status and the order of the restore steps.
func (d *Driver) guestDisks(ctx context.Context, m member) (map[string]string, error) {
	cfg, err := d.readPath(ctx, guestPath(d, m), true)
	if err != nil {
		return nil, err
	}
	keys := reDiskKey
	if m.Type == typeCT {
		keys = reCTDiskKey
	}
	out := map[string]string{}
	for k, v := range cfg {
		if keys.MatchString(k) {
			vol, _, _ := strings.Cut(v, ",")
			out[k] = vol
		}
	}
	return out, nil
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
