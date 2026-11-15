package proxmox

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// Data disks (D-423, D-436, D-437): a VM scsi1 … scsi3, a container
// mp1 … mp3, on the install's disk storage.

// diskKey is the config key of data disk slot n.
func diskKey(kind wire.Kind, n int) string {
	if kind == wire.KindLXC {
		return "mp" + strconv.Itoa(n)
	}
	return "scsi" + strconv.Itoa(n)
}

var reUnusedKey = regexp.MustCompile(`^unused[0-9]+$`)

// ownVolume reports whether vol is a volume of guest vmid on storage:
// "<storage>:vm-<vmid>-disk-<n>" (lvm, lvmthin, zfs, rbd) or
// "<storage>:<vmid>/vm-<vmid>-disk-<n>.<fmt>" (dir, nfs).
func ownVolume(vol, storage string, vmid int) bool {
	name, ok := strings.CutPrefix(vol, storage+":")
	if !ok {
		return false
	}
	id := strconv.Itoa(vmid)
	if rest, ok := strings.CutPrefix(name, id+"/"); ok {
		name = rest
		if i := strings.LastIndexByte(name, '.'); i > 0 {
			name = name[:i]
		}
	}
	n, ok := strings.CutPrefix(name, "vm-"+id+"-disk-")
	if !ok || n == "" {
		return false
	}
	_, err := strconv.Atoi(n)
	return err == nil
}

// isStorageVolume reports whether a mount point value names a storage volume
// (not a host path or a device, which stay the person's mounts).
func isStorageVolume(vol string) bool {
	return vol != "" && !strings.HasPrefix(vol, "/") && strings.Contains(vol, ":")
}

// withDisks fills s.Disks (and s.Unused) from a config view of guest vmid,
// and takes the data disk slots out of s.Mounts. A slot holds a Plumb disk
// when its volume is on the disk storage and owned by vmid (a VM disk also
// with serial plumb-N and no cdrom); any other volume in the slot is a
// foreign one. A container mount point that is a host path or a device is
// never a data disk: it stays in Mounts (outside, D-436).
func withDisks(s driver.GuestSpec, cfg map[string]string, storage string, vmid int) driver.GuestSpec {
	s.DisksSet = true
	s.Disks, s.Unused = nil, nil
	for n := 1; n <= driver.MaxDisks; n++ {
		key := diskKey(s.Kind, n)
		v, ok := cfg[key]
		if !ok || v == "" {
			continue
		}
		p := parseProps(v)
		vol := p[""]
		if s.Kind == wire.KindLXC {
			if !isStorageVolume(vol) {
				continue // a bind mount or a device: Mounts
			}
			s.Mounts = slices.DeleteFunc(s.Mounts, func(m string) bool { return strings.HasPrefix(m, key+"=") })
		}
		d := driver.Disk{Slot: n, Volume: vol, SizeGB: sizeGB(p["size"]), Path: p["mp"]}
		plumb := ownVolume(vol, storage, vmid) && p["media"] != "cdrom"
		if s.Kind != wire.KindLXC {
			plumb = plumb && p["serial"] == driver.DiskSerial(n)
		}
		if !plumb {
			d = driver.Disk{Slot: n, Foreign: true, Raw: v}
		}
		s.Disks = append(s.Disks, d)
	}
	for k, v := range cfg {
		if reUnusedKey.MatchString(k) {
			s.Unused = append(s.Unused, parseProps(v)[""])
		}
	}
	sort.Strings(s.Unused)
	return s
}

// unusedKeyOf returns the unusedN key whose volume is vol, or "".
func unusedKeyOf(cfg map[string]string, vol string) string {
	for k, v := range cfg {
		if reUnusedKey.MatchString(k) && parseProps(v)[""] == vol {
			return k
		}
	}
	return ""
}

// vmDiskValue is the scsiN value of a new Plumb VM data disk: exactly
// these options and nothing else (ARCHITECTURE §21; pinned by a test).
func vmDiskValue(storage string, n, gb int) string {
	return storage + ":" + strconv.Itoa(gb) + ",serial=" + driver.DiskSerial(n)
}

// ctDiskValue is the mpN value of a new Plumb container data disk.
func ctDiskValue(storage string, gb int, path string) string {
	return storage + ":" + strconv.Itoa(gb) + ",mp=" + path + ",backup=1"
}
