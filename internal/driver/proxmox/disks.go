package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
// foreign one. A container mount point that is a host path or a device
// occupies its slot too, as a foreign occupant (H1 of the #373 review):
// a named slot with it refuses the Plan, and with an unmanaged disks list
// the diff counts it as the person's mount again (driver.mountsOf).
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
			s.Mounts = slices.DeleteFunc(s.Mounts, func(m string) bool { return strings.HasPrefix(m, key+"=") })
		}
		d := driver.Disk{Slot: n, Volume: vol, SizeGB: sizeGB(p["size"]), Path: p["mp"]}
		plumb := isStorageVolume(vol) && ownVolume(vol, storage, vmid) && p["media"] != "cdrom"
		if s.Kind != wire.KindLXC {
			plumb = plumb && p["serial"] == driver.DiskSerial(n)
		}
		if !plumb {
			d = driver.Disk{Slot: n, Foreign: true, Raw: v}
			if isStorageVolume(vol) {
				d.Volume = vol // named, never freed: only a Plumb disk is (D-436)
			}
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

// addDiskParams adds the data disks of a new guest to its create (or
// first configure) params. A new container is not started yet, so its
// mount points are written cold (D-437).
func addDiskParams(params url.Values, kind wire.Kind, storage string, disks []driver.Disk) {
	for _, dk := range disks {
		if dk.Slot < 1 || dk.Slot > driver.MaxDisks || dk.Foreign {
			continue
		}
		if kind == wire.KindLXC {
			params.Set(diskKey(kind, dk.Slot), ctDiskValue(storage, dk.SizeGB, dk.Path))
		} else {
			params.Set(diskKey(kind, dk.Slot), vmDiskValue(storage, dk.Slot, dk.SizeGB))
		}
	}
}

// diskChanges applies the data disk changes of an Update, one Proxmox
// call per step with the digest of a fresh read (D-436, D-437). It runs
// after the other settings. A VM disk is hot-added and hot-removed; a
// container disk is added, removed or detached while the container is
// stopped (D-437): a running one is stopped and started again (the Plan
// holds allow_restart for it).
func (d *Driver) diskChanges(ctx context.Context, id int, kind wire.Kind, changes []wire.Change) error {
	var stopped bool // a container this call stopped
	path := vmPath(d, id)
	if kind == wire.KindLXC {
		path = ctPath(d, id)
	}
	read := func() (map[string]string, error) {
		if kind == wire.KindLXC {
			return d.ctConfig(ctx, id, true)
		}
		return d.readConfig(ctx, id, true)
	}
	write := func(params url.Values) error {
		method := "POST"
		if kind == wire.KindLXC {
			method = "PUT"
		}
		for k := range params {
			if k != "digest" && k != "delete" && !reDiskWriteKey.MatchString(k) {
				return fmt.Errorf("proxmox: a disk step must not write %q", k)
			}
		}
		return d.c.MaybeTask(ctx, d.cfg.Node, method, path+"/config", params)
	}
	stopCT := func() error {
		if kind != wire.KindLXC || stopped {
			return nil
		}
		var st struct {
			Status string `json:"status"`
		}
		if err := d.c.Get(ctx, path+"/status/current", nil, &st); err != nil {
			return notFound(err)
		}
		if st.Status != "running" {
			return nil
		}
		if err := d.c.Task(ctx, d.cfg.Node, "POST", path+"/status/shutdown", url.Values{
			"timeout": {strconv.Itoa(int(d.cfg.ShutdownTimeout.Seconds()))}, "forceStop": {"1"},
		}); err != nil {
			return fmt.Errorf("proxmox: stop the container for its disk change: %w", err)
		}
		stopped = true
		return nil
	}
	start := func() error { return d.c.Task(ctx, d.cfg.Node, "POST", path+"/status/start", nil) }
	// free detaches the volume of slot n and frees it by its id (never by
	// the unused index alone): a Plumb disk only.
	free := func(n int, vol string) error {
		cfg, err := read()
		if err != nil {
			return &driver.VolumeLeftError{Slot: n, Volume: vol, Err: err}
		}
		key := unusedKeyOf(cfg, vol)
		if key == "" {
			return nil // Proxmox freed it with the detach
		}
		if err := write(url.Values{"digest": {cfg["digest"]}, "delete": {key}}); err != nil {
			return &driver.VolumeLeftError{Slot: n, Volume: vol, Err: err}
		}
		// Read back: Proxmox may answer OK and keep the entry (M1 of #373).
		if cfg, err = read(); err != nil {
			return &driver.VolumeLeftError{Slot: n, Volume: vol, Err: err}
		}
		if unusedKeyOf(cfg, vol) != "" {
			return &driver.VolumeLeftError{Slot: n, Volume: vol, Err: errors.New("it is still in the config")}
		}
		return nil
	}
	err := func() error {
		for _, c := range changes {
			n := driver.DiskIndex(c.Attr)
			cfg, err := read()
			if err != nil {
				return err
			}
			have := withDisks(driver.GuestSpec{Kind: kind}, cfg, d.cfg.DiskStorage, id).DiskAt(n)
			want, err := driver.ParseDiskValue(c.To)
			if err != nil {
				return err
			}
			key := diskKey(kind, n)
			switch {
			case want == nil && have == nil:
			case want == nil: // remove a Plumb disk, or detach a foreign volume
				if err := stopCT(); err != nil {
					return err
				}
				if cfg, err = read(); err != nil {
					return err
				}
				if err := write(url.Values{"digest": {cfg["digest"]}, "delete": {key}}); err != nil {
					return fmt.Errorf("proxmox: remove disk %d: %w", n, err)
				}
				after, err := read()
				if err != nil {
					return err
				}
				if after[key] != "" {
					return fmt.Errorf("proxmox: disk %d is still attached after the remove (it waits for a stop and start); nothing was freed", n)
				}
				if have.Foreign {
					continue // a foreign occupant is only detached, never freed (D-436)
				}
				if err := free(n, have.Volume); err != nil {
					return err
				}
			case have == nil: // add
				if kind == wire.KindLXC && !reAbsPath.MatchString(want.Path) {
					return fmt.Errorf("proxmox: disk %d needs a mount path", n)
				}
				if err := stopCT(); err != nil {
					return err
				}
				if cfg, err = read(); err != nil {
					return err
				}
				// H1 of #373: never write over anything in the slot.
				if cfg[key] != "" {
					return &driver.DiskForeignError{Slot: n, Raw: cfg[key]}
				}
				before := guestVolumes(cfg) // H2 of #373: the undo frees only a new volume
				v := vmDiskValue(d.cfg.DiskStorage, n, want.SizeGB)
				if kind == wire.KindLXC {
					v = ctDiskValue(d.cfg.DiskStorage, want.SizeGB, want.Path)
				}
				if err := write(url.Values{"digest": {cfg["digest"]}, key: {v}}); err != nil {
					return fmt.Errorf("proxmox: add disk %d: %w", n, err)
				}
				if !stopped {
					continue
				}
				if err := start(); err != nil {
					err := d.undoCTAdd(ctx, id, n, want.Path, before, read, write, free, start)
					if !errors.As(err, new(*driver.DiskStartError)) && !errors.As(err, new(*driver.VolumeLeftError)) {
						return err // it did not start again: the deferred start tries once more
					}
					stopped = false
					return err
				}
				stopped = false
			case have.Foreign:
				return &driver.DiskForeignError{Slot: n, Raw: have.Raw}
			default: // grow
				if want.Path != have.Path {
					return &driver.DiskPathError{Slot: n, Have: have.Path, Want: want.Path}
				}
				gb, err := growTo(want.SizeGB, have.SizeGB)
				if err != nil {
					var se *driver.ShrinkError
					if errors.As(err, &se) {
						se.Slot = n
					}
					return err
				}
				if gb > 0 {
					if err := d.resize(ctx, path, key, gb); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}()
	if stopped {
		if serr := start(); serr != nil && err == nil {
			err = fmt.Errorf("proxmox: start the container after its disk change: %w", serr)
		}
	}
	return err
}

// undoCTAdd removes a container disk whose start failed (D-437 (2)):
// detach it (stopped: it becomes unusedM at once), free it by its id, and
// start the container again. It frees only a volume that this add made:
// on the disk storage, owned by the guest, and not among the guest's
// volumes before the add (H2 of #373). Anything else is detached only, and
// the step names it as a volume left (guest:volume-left).
func (d *Driver) undoCTAdd(ctx context.Context, id, n int, path string, before map[string]bool, read func() (map[string]string, error),
	write func(url.Values) error, free func(int, string) error, start func() error) error {
	cfg, err := read()
	if err != nil {
		return err
	}
	key := "mp" + strconv.Itoa(n)
	vol := parseProps(cfg[key])[""]
	if err := write(url.Values{"digest": {cfg["digest"]}, "delete": {key}}); err != nil {
		return &driver.VolumeLeftError{Slot: n, Volume: vol, Err: err}
	}
	var ferr error
	if isStorageVolume(vol) && ownVolume(vol, d.cfg.DiskStorage, id) && !before[vol] {
		ferr = free(n, vol)
	} else {
		ferr = &driver.VolumeLeftError{Slot: n, Volume: vol, Err: errors.New("Plumb did not free it: it is not the volume this Run made")}
	}
	if err := start(); err != nil {
		return fmt.Errorf("proxmox: the container did not start with disk %d at %s, and it did not start again after Plumb removed the disk: %w", n, path, err)
	}
	if ferr != nil {
		return ferr
	}
	return &driver.DiskStartError{Slot: n, Path: path}
}

var (
	reDiskWriteKey = regexp.MustCompile(`^(scsi|mp)[1-3]$`)
	reAbsPath      = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
)

// guestVolumes is the set of volumes that a config view names (every
// disk, mount point and unused entry).
func guestVolumes(cfg map[string]string) map[string]bool {
	out := map[string]bool{}
	for k, v := range cfg {
		if k == "rootfs" || reDiskKeyAny.MatchString(k) || reUnusedKey.MatchString(k) {
			if vol := parseProps(v)[""]; vol != "" {
				out[vol] = true
			}
		}
	}
	return out
}

var reDiskKeyAny = regexp.MustCompile(`^(scsi|sata|ide|virtio|mp)[0-9]+$`)
