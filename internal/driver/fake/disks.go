package fake

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// Data disks of the fake (D-436, D-437). A disk's volume is
// "fake:vm-<id>-disk-<slot>". Both views change at once: the fake models
// the end state of a Proxmox step (a VM hot-add, a container written while
// stopped); FailDiskStart and FailDiskFree model the D-437 failure paths.

// FailDiskStart makes the next container disk add fail its start: the
// add is undone and Update returns a *driver.DiskStartError.
func (d *Driver) FailDiskStart() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.diskStartFails = true
}

// FailDiskFree makes every free of a data disk volume fail: Update
// returns a *driver.VolumeLeftError and the volume stays (Leftover).
func (d *Driver) FailDiskFree(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.diskFreeFails = on
}

// Leftover lists the volumes that a failed free left on the storage.
func (d *Driver) Leftover() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.leftover)
}

// SetForeignDisk puts a volume that Plumb did not make into slot n of a
// guest (a person's disk on the host).
func (d *Driver) SetForeignDisk(id driver.GuestID, n int, raw string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	g := d.guests[id]
	dk := driver.Disk{Slot: n, Foreign: true, Raw: raw}
	g.spec.Disks, g.next.Disks = withDisk(g.spec.Disks, n, &dk), withDisk(g.next.Disks, n, &dk)
	g.gen++
}

func fakeVolume(id driver.GuestID, n int) string { return fmt.Sprintf("fake:vm-%s-disk-%d", id, n) }

// withDisk returns disks with slot n set to dk (nil: removed), sorted.
func withDisk(disks []driver.Disk, n int, dk *driver.Disk) []driver.Disk {
	out := slices.DeleteFunc(slices.Clone(disks), func(x driver.Disk) bool { return x.Slot == n })
	if dk != nil {
		out = append(out, *dk)
	}
	slices.SortFunc(out, func(a, b driver.Disk) int { return a.Slot - b.Slot })
	return out
}

// createDisks gives the data disks of a new guest their volumes.
func createDisks(id driver.GuestID, disks []driver.Disk) []driver.Disk {
	var out []driver.Disk
	for _, dk := range disks {
		dk.Volume, dk.Foreign, dk.Raw = fakeVolume(id, dk.Slot), false, ""
		out = withDisk(out, dk.Slot, &dk)
	}
	return out
}

// diskChange applies one data disk change to both views of g.
func (d *Driver) diskChange(id driver.GuestID, g *guest, run, next *driver.GuestSpec, c wire.Change) error {
	n := driver.DiskIndex(c.Attr)
	want, err := driver.ParseDiskValue(c.To)
	if err != nil {
		return err
	}
	have := run.DiskAt(n)
	if have == nil {
		have = next.DiskAt(n)
	}
	switch {
	case want == nil && have == nil:
		return nil
	case want == nil:
		run.Disks, next.Disks = withDisk(run.Disks, n, nil), withDisk(next.Disks, n, nil)
		// As Proxmox (T-244 D7, D8): a detached volume becomes an unusedN
		// entry of the guest. A foreign storage volume stays so (Plumb never
		// frees it); a Plumb disk stays so only when its free fails (T-245).
		if have.Foreign {
			if vol, _, _ := strings.Cut(have.Raw, ","); vol != "" && !strings.HasPrefix(vol, "/") {
				run.Unused, next.Unused = appendSorted(run.Unused, vol), appendSorted(next.Unused, vol)
			}
			return nil
		}
		if d.diskFreeFails {
			d.leftover = append(d.leftover, have.Volume)
			run.Unused, next.Unused = appendSorted(run.Unused, have.Volume), appendSorted(next.Unused, have.Volume)
			return &driver.VolumeLeftError{Slot: n, Volume: have.Volume, Err: fmt.Errorf("fake: free refused")}
		}
		return nil
	case have == nil:
		if g.lxc() && d.diskStartFails {
			d.diskStartFails = false
			return &driver.DiskStartError{Slot: n, Path: want.Path}
		}
		dk := driver.Disk{Slot: n, SizeGB: want.SizeGB, Path: want.Path, Volume: fakeVolume(id, n)}
		run.Disks, next.Disks = withDisk(run.Disks, n, &dk), withDisk(next.Disks, n, &dk)
		return nil
	case have.Foreign:
		return &driver.DiskForeignError{Slot: n, Raw: have.Raw}
	case want.Path != have.Path:
		return &driver.DiskPathError{Slot: n, Have: have.Path, Want: want.Path}
	case want.SizeGB < have.SizeGB:
		return &driver.ShrinkError{Slot: n, Have: have.SizeGB, Want: want.SizeGB}
	}
	dk := *have
	dk.SizeGB = want.SizeGB
	run.Disks, next.Disks = withDisk(run.Disks, n, &dk), withDisk(next.Disks, n, &dk)
	return nil
}

// GrowDisk makes the Plumb data disk in slot n of a guest gb GiB big, in
// both views (a person grew it in Proxmox).
func (d *Driver) GrowDisk(id driver.GuestID, n, gb int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	g := d.guests[id]
	for _, v := range []*driver.GuestSpec{&g.spec, &g.next} {
		if dk := v.DiskAt(n); dk != nil {
			x := *dk
			x.SizeGB = gb
			v.Disks = withDisk(v.Disks, n, &x)
		}
	}
	g.gen++
}

func appendSorted(list []string, v string) []string {
	out := append(slices.Clone(list), v)
	slices.Sort(out)
	return out
}

// FreeVolume frees an unused volume of a guest as a person does with
// pvesm free (T-245): it leaves the guest's unused entries and Leftover.
func (d *Driver) FreeVolume(id driver.GuestID, vol string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	drop := func(l []string) []string {
		return slices.DeleteFunc(slices.Clone(l), func(x string) bool { return x == vol })
	}
	if g := d.guests[id]; g != nil {
		g.spec.Unused, g.next.Unused = drop(g.spec.Unused), drop(g.next.Unused)
		g.gen++
	}
	d.leftover = drop(d.leftover)
}
