package proxmox

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The data disks of the PVE fake (T-238), as T-244 found them on pve-dev:
// a scsiN or mpN write "<storage>:<gb>,…" allocates vm-<id>-disk-<n>; a
// delete of scsiN or mpN leaves the volume as unusedK (a running container
// keeps the delete pending until the next start); a delete of unusedK
// frees the volume. A container whose mp path is in symlinks does not
// start (D13).

var (
	reFakeDataDisk = regexp.MustCompile(`^(scsi|mp)[1-3]$`)
	reFakeUnused   = regexp.MustCompile(`^unused[0-9]+$`)
	reFakeAlloc    = regexp.MustCompile(`^([a-z0-9-]+):([0-9]+)(,.*)?$`)
)

// diskState is the data disk part of fakePVE.
type diskState struct {
	freed     []string        // volumes freed by a delete of unusedK
	symlinks  map[string]bool // container mount paths that cross a symlink
	failFree  bool            // a delete of unusedK fails
	allocated int             // volumes allocated so far (the next disk number)
}

// diskWrite applies a write of a data disk key; false when k is none.
func (f *fakePVE) diskWrite(vm *fakeVM, id int, k, v string) bool {
	if !reFakeDataDisk.MatchString(k) {
		return false
	}
	if m := reFakeAlloc.FindStringSubmatch(v); m != nil {
		f.disks.allocated++
		v = fmt.Sprintf("%s:vm-%d-disk-%d%s,size=%sG", m[1], id, 10+f.disks.allocated, m[3], m[2])
	}
	vm.cfg[k] = v
	return true
}

// diskDelete applies a delete of a data disk or unused key; handled is
// false for other keys.
func (f *fakePVE) diskDelete(vm *fakeVM, k string) (handled bool, err error) {
	switch {
	case reFakeUnused.MatchString(k):
		if f.disks.failFree {
			return true, fmt.Errorf("unable to free %s", vm.cfg[k])
		}
		f.disks.freed = append(f.disks.freed, parseProps(vm.cfg[k])[""])
		delete(vm.cfg, k)
		return true, nil
	case reFakeDataDisk.MatchString(k):
		if vm.ct && vm.running {
			if vm.delete == nil {
				vm.delete = map[string]bool{}
			}
			vm.delete[k] = true // pending until the next start (T-244 D8)
			return true, nil
		}
		vm.detach(k)
		return true, nil
	}
	return false, nil
}

// detach moves the volume of a data disk key to the first free unusedK.
func (vm *fakeVM) detach(k string) {
	vol := parseProps(vm.cfg[k])[""]
	delete(vm.cfg, k)
	for i := 0; ; i++ {
		u := "unused" + strconv.Itoa(i)
		if _, taken := vm.cfg[u]; !taken {
			vm.cfg[u] = vol
			return
		}
	}
}

// ctStartBlocked reports a mount path of the container that crosses a
// symlink: Proxmox's pre-start hook refuses it (T-244 D13).
func (f *fakePVE) ctStartBlocked(vm *fakeVM) bool {
	for k, v := range vm.cfg {
		if strings.HasPrefix(k, "mp") && f.disks.symlinks[parseProps(v)["mp"]] {
			return true
		}
	}
	return false
}
