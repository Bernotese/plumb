package driver

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
)

// MaxDisks is the number of data disks after the main disk (D-423,
// D-436): slots 1 … 3, a VM scsi1 … scsi3, a container mp1 … mp3.
const MaxDisks = 3

// Disk is one data disk of a guest (D-423, D-436). The slot is its
// identity, never its position in a list.
type Disk struct {
	Slot   int
	SizeGB int
	// Path is the mount path in a container (create-only per slot); a VM
	// disk has none: the guest finds it by its serial (DiskSerial).
	Path string
	// Volume is the observed storage volume ("<storage>:vm-<vmid>-disk-<n>").
	// The desired state has none.
	Volume string
	// Foreign: the observed slot holds a volume that Plumb did not make
	// (another storage or owner, or a VM disk without Plumb's serial).
	// Plumb never grows, accepts or frees it; Raw is its config value.
	Foreign bool
	Raw     string
}

// AttrDiskN is the guest:config attribute of data disk slot n (1 … MaxDisks).
func AttrDiskN(n int) string { return "disk" + strconv.Itoa(n) }

// DiskIndex returns n of an attribute AttrDiskN(n), or 0.
func DiskIndex(attr string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(attr, "disk"))
	if !strings.HasPrefix(attr, "disk") || err != nil || n < 1 || n > MaxDisks || attr != AttrDiskN(n) {
		return 0
	}
	return n
}

// DiskSerial is the serial of a Plumb VM data disk in slot n (D-436). The
// guest sees it as lsblk SERIAL; on virtio-scsi it is not in
// /dev/disk/by-id (T-244 D4).
func DiskSerial(n int) string { return "plumb-" + strconv.Itoa(n) }

// Value is the change value of the disk: "<size> GiB", for a container
// "<size> GiB at <path>"; a foreign volume is "<raw> (not made by Plumb)".
// nil for an empty slot.
func (d *Disk) Value() any {
	switch {
	case d == nil:
		return nil
	case d.Foreign:
		return d.Raw + " (not made by Plumb)"
	case d.Path != "":
		return fmt.Sprintf("%d GiB at %s", d.SizeGB, d.Path)
	}
	return fmt.Sprintf("%d GiB", d.SizeGB)
}

var reDiskValue = regexp.MustCompile(`^([1-9][0-9]*) GiB(?: at (/.*))?$`)

// ParseDiskValue reads a value of a Plumb disk (Disk.Value) back. nil is
// an empty slot. A foreign value is an error: Plumb never writes one.
func ParseDiskValue(v any) (*Disk, error) {
	if v == nil {
		return nil, nil
	}
	s, _ := v.(string)
	m := reDiskValue.FindStringSubmatch(s)
	if m == nil {
		return nil, fmt.Errorf("driver: bad disk value %v", v)
	}
	gb, err := strconv.Atoi(m[1])
	if err != nil {
		return nil, fmt.Errorf("driver: bad disk value %v", v)
	}
	return &Disk{SizeGB: gb, Path: m[2]}, nil
}

// DiskAt returns the disk in slot n of s, or nil.
func (s GuestSpec) DiskAt(n int) *Disk {
	i := slices.IndexFunc(s.Disks, func(d Disk) bool { return d.Slot == n })
	if i < 0 {
		return nil
	}
	return &s.Disks[i]
}

// DisksGB is the sum of the data disk sizes (Plumb disks only).
func (s GuestSpec) DisksGB() int {
	n := 0
	for _, d := range s.Disks {
		if !d.Foreign {
			n += d.SizeGB
		}
	}
	return n
}

// foreignMounts are the container mount values of the foreign data disk
// slots, as Mounts holds them ("mpN=<value>"). Without a managed disks
// list they stay what they were before slice 8: a mount point that a
// person added (D-436: a missing disks field manages nothing).
func foreignMounts(s GuestSpec) []string {
	if s.Kind != wire.KindLXC {
		return nil
	}
	var out []string
	for _, d := range s.Disks {
		if d.Foreign {
			out = append(out, fmt.Sprintf("mp%d=%s", d.Slot, d.Raw))
		}
	}
	return out
}

// mountsOf is the mounts list to compare: with an unmanaged disks list a
// foreign volume in mp1 … mp3 is a person's mount point, as before.
func mountsOf(s GuestSpec, managed bool) []string {
	if managed {
		return s.Mounts
	}
	out := append(slices.Clone(s.Mounts), foreignMounts(s)...)
	slices.Sort(out)
	return out
}

// DiskRisk is the risk of a change of data disk slot n from have to want
// (ARCHITECTURE §21): an add is reversible (a hot-add on a VM; a
// container add is written while it is stopped, so the plan adds the
// restart rule), a grow disruptive as the main disk (D-299), a remove or
// the detach of a foreign volume destructive.
func DiskRisk(have, want *Disk) wire.Risk {
	switch {
	case want == nil:
		return wire.RiskDestructive
	case have == nil:
		return wire.RiskReversible
	}
	return wire.RiskDisruptive
}

// DiskForeignError is returned by Diff when the desired state names a
// slot that holds a volume Plumb did not make (H1 of #360): Plumb never
// takes it over.
type DiskForeignError struct {
	Slot int
	Raw  string
}

func (e *DiskForeignError) Error() string {
	return fmt.Sprintf("slot %d holds %s, which Plumb did not make; remove it in Proxmox or use another slot", e.Slot, e.Raw)
}

// DiskPathError is returned by Diff when the desired path of a container
// data disk differs from the path of its slot: the path is create-only
// per slot (D-436).
type DiskPathError struct {
	Slot       int
	Have, Want string
}

func (e *DiskPathError) Error() string {
	return fmt.Sprintf("the path of disk %d is %s and can only be set when the disk is added (desired: %s); move the data in the guest instead", e.Slot, e.Have, e.Want)
}

// diskErrors checks the managed data disks of the desired state against
// both views of the guest.
func diskErrors(desired GuestSpec, observed GuestState) error {
	if !desired.DisksSet || desired.Adopted {
		return nil
	}
	for _, s := range []GuestSpec{observed.Spec, observed.Next()} {
		for n := 1; n <= MaxDisks; n++ {
			w, h := desired.DiskAt(n), s.DiskAt(n)
			if w == nil || h == nil {
				continue
			}
			if h.Foreign {
				return &DiskForeignError{Slot: n, Raw: h.Raw}
			}
			if w.SizeGB < h.SizeGB {
				return &ShrinkError{Slot: n, Have: h.SizeGB, Want: w.SizeGB}
			}
			if w.Path != h.Path {
				return &DiskPathError{Slot: n, Have: h.Path, Want: w.Path}
			}
		}
	}
	return nil
}

// DiskStartError: a container did not start with the new data disk in
// slot Slot (D-437). The driver removed the disk again and started the
// container as before. The text is Plumb's own and never quotes the
// guest (ARCHITECTURE §21).
type DiskStartError struct {
	Slot int
	Path string
}

func (e *DiskStartError) Error() string {
	return fmt.Sprintf("the container did not start with disk %d at %s; Plumb removed the disk again, and the guest runs as before", e.Slot, e.Path)
}

// VolumeLeftError: a data disk volume of the guest could not be freed
// (D-437 (4), the remove step of D-436). The volume stays on the disk
// storage until a person frees it or the Target is deleted.
type VolumeLeftError struct {
	Slot   int
	Volume string
	Err    error
}

func (e *VolumeLeftError) Error() string {
	if e.InUse() {
		return fmt.Sprintf("the volume %s of disk %d could not be freed (%v); it stays on the disk storage. %s", e.Volume, e.Slot, e.Err, SnapshotHint)
	}
	return fmt.Sprintf("the volume %s of disk %d could not be freed (%v); it stays on the disk storage: a person removes it as root with `qm set <vmid> --delete unusedN` (`pct set` for a container; N of the unused line that names it), or with `pvesm free %s` when no unused line names it; or a Plumb Target delete frees it", e.Volume, e.Slot, e.Err, e.Volume)
}

// InUse reports a free that Proxmox refused because the volume is still in
// use, mostly by a snapshot of the guest (T-247 step V, T-253 F3).
func (e *VolumeLeftError) InUse() bool {
	return e.Err != nil && strings.Contains(e.Err.Error(), "still in use")
}

// SnapshotHint is the way out of a volume that Proxmox keeps because it is
// still in use (T-247 step V): a pvesm free there leaves the unused line,
// so the Issue would stay open, and a later snapshot delete can lock the
// guest.
const SnapshotHint = "A snapshot may hold the volume: remove the snapshot first (`qm delsnapshot <vmid> <name>`; `pct delsnapshot` for a container), then `qm set <vmid> --delete unusedN` (`pct set`; the unused line that names the volume). Do not `pvesm free` a volume that an unused line names: the line stays, and so does the Issue."

func (e *VolumeLeftError) Unwrap() error { return e.Err }
