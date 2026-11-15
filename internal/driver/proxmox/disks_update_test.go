package proxmox

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

func diskChange(n int, from, to any) wire.Change {
	return wire.Change{Attr: driver.AttrDiskN(n), From: from, To: to}
}

// A VM data disk: a live hot-add with exactly Plumb's options, a grow of
// that disk, and a remove that frees the volume by its id (T-244 D1, D7).
func TestVMDataDiskLifecycle(t *testing.T) {
	f, d, id, _ := newRunningGuest(t)
	ctx := context.Background()
	if err := d.Update(ctx, id, []wire.Change{diskChange(2, nil, "4 GiB")}, nil); err != nil {
		t.Fatal(err)
	}
	p := f.lastParams("POST /nodes/pve/qemu/9001/config")
	if got := p.Get("scsi2"); got != "local-lvm:4,serial=plumb-2" || len(p) != 2 {
		t.Fatalf("add params %v", p)
	}
	if !f.vms[9001].running {
		t.Fatal("a VM add stopped the guest")
	}
	st, err := d.Observe(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	dk := st.Spec.DiskAt(2)
	if dk == nil || dk.Foreign || dk.SizeGB != 4 || dk.Volume == "" {
		t.Fatalf("observed %+v", st.Spec.Disks)
	}
	if err := d.Update(ctx, id, []wire.Change{diskChange(2, "4 GiB", "6 GiB")}, nil); err != nil {
		t.Fatal(err)
	}
	if last := f.resizes[len(f.resizes)-1]; last != "9001 scsi2 6G" {
		t.Fatalf("resize %s", last)
	}
	if err := d.Update(ctx, id, []wire.Change{diskChange(2, "6 GiB", "5 GiB")}, nil); err == nil {
		t.Fatal("a shrink passed")
	}
	if err := d.Update(ctx, id, []wire.Change{diskChange(2, "6 GiB", nil)}, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.disks.freed, []string{dk.Volume}) {
		t.Fatalf("freed %v, want %s", f.disks.freed, dk.Volume)
	}
	for k := range f.vms[9001].cfg {
		if k == "scsi2" || reFakeUnused.MatchString(k) {
			t.Fatalf("left %s", k)
		}
	}
}

// A foreign volume in a slot is only detached, never freed (D-436).
func TestVMForeignDiskDetachOnly(t *testing.T) {
	f, d, id, _ := newRunningGuest(t)
	ctx := context.Background()
	f.mu.Lock()
	f.vms[9001].cfg["scsi1"] = "local-lvm:vm-9001-disk-7,size=2G" // no Plumb serial
	f.mu.Unlock()
	if err := d.Update(ctx, id, []wire.Change{diskChange(1, "x", "2 GiB")}, nil); !errors.As(err, new(*driver.DiskForeignError)) {
		t.Fatalf("grow of a foreign volume: %v", err)
	}
	if err := d.Update(ctx, id, []wire.Change{diskChange(1, "x", nil)}, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.disks.freed) != 0 || f.vms[9001].cfg["unused0"] != "local-lvm:vm-9001-disk-7" {
		t.Fatalf("freed %v cfg %v", f.disks.freed, f.vms[9001].cfg)
	}
}

// A failed free names the volume (D-437 (4)).
func TestVMDiskFreeFails(t *testing.T) {
	f, d, id, _ := newRunningGuest(t)
	ctx := context.Background()
	if err := d.Update(ctx, id, []wire.Change{diskChange(1, nil, "2 GiB")}, nil); err != nil {
		t.Fatal(err)
	}
	f.disks.failFree = true
	err := d.Update(ctx, id, []wire.Change{diskChange(1, "2 GiB", nil)}, nil)
	var ve *driver.VolumeLeftError
	if !errors.As(err, &ve) || ve.Slot != 1 || ve.Volume == "" {
		t.Fatalf("free fails: %v", err)
	}
}

// D-437: a container disk is written while the container is stopped; a
// running one is stopped and started again.
func TestCTDataDiskColdAdd(t *testing.T) {
	f, d, id := newCT(t)
	ctx := context.Background()
	if err := d.Power(ctx, id, driver.PowerStart); err != nil {
		t.Fatal(err)
	}
	if err := d.Update(ctx, id, []wire.Change{diskChange(1, nil, "2 GiB at /srv/data")}, nil); err != nil {
		t.Fatal(err)
	}
	p := f.lastParams("PUT /nodes/pve/lxc/9000/config")
	if p.Get("mp1") != "local-lvm:2,mp=/srv/data,backup=1" || len(p) != 2 {
		t.Fatalf("add params %v", p)
	}
	if f.count("POST /nodes/pve/lxc/9000/status/shutdown") != 1 || !f.vms[9000].running {
		t.Fatal("the container was not stopped and started again")
	}
	// A remove on the running container: stopped, detached, freed, started.
	if err := d.Update(ctx, id, []wire.Change{diskChange(1, "2 GiB at /srv/data", nil)}, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.disks.freed) != 1 || !f.vms[9000].running {
		t.Fatalf("remove: freed %v running %v", f.disks.freed, f.vms[9000].running)
	}
	for k := range f.vms[9000].cfg {
		if k == "mp1" || reFakeUnused.MatchString(k) {
			t.Fatalf("left %s", k)
		}
	}
}

// D-437 (2): a start that fails with the new disk is undone: the disk is
// detached and freed, the container starts as before, and the step fails
// with Plumb's text. No volume leaks.
func TestCTDataDiskFailedStartUndo(t *testing.T) {
	f, d, id := newCT(t)
	ctx := context.Background()
	if err := d.Power(ctx, id, driver.PowerStart); err != nil {
		t.Fatal(err)
	}
	f.disks.symlinks = map[string]bool{"/srv/lnk": true}
	err := d.Update(ctx, id, []wire.Change{diskChange(1, nil, "2 GiB at /srv/lnk")}, nil)
	var se *driver.DiskStartError
	if !errors.As(err, &se) || se.Slot != 1 || se.Path != "/srv/lnk" {
		t.Fatalf("failed start: %v", err)
	}
	if !f.vms[9000].running || len(f.disks.freed) != 1 {
		t.Fatalf("undo: running %v freed %v", f.vms[9000].running, f.disks.freed)
	}
	if _, ok := f.vms[9000].cfg["mp1"]; ok {
		t.Fatal("mp1 left")
	}
	// The free fails too: the volume is named.
	f.disks.failFree = true
	err = d.Update(ctx, id, []wire.Change{diskChange(1, nil, "2 GiB at /srv/lnk")}, nil)
	var ve *driver.VolumeLeftError
	if !errors.As(err, &ve) || ve.Volume == "" || !f.vms[9000].running {
		t.Fatalf("free fails: %v running %v", err, f.vms[9000].running)
	}
}

// A stopped container gets the disk written and stays stopped (proven
// at its next start, low of the #365 re-check).
func TestCTDataDiskStoppedStaysStopped(t *testing.T) {
	f, d, id := newCT(t)
	ctx := context.Background()
	f.mu.Lock()
	f.vms[9000].running = false
	f.mu.Unlock()
	starts := f.count("POST /nodes/pve/lxc/9000/status/start")
	if err := d.Update(ctx, id, []wire.Change{diskChange(3, nil, "1 GiB at /srv/x")}, nil); err != nil {
		t.Fatal(err)
	}
	if f.vms[9000].running || f.count("POST /nodes/pve/lxc/9000/status/start") != starts {
		t.Fatal("a stopped container was started")
	}
}

// The create of a guest with data disks writes them in the same call.
func TestCreateWithDataDisks(t *testing.T) {
	f := newFakePVE(t)
	f.files[ctTemplate] = true
	d := f.driver(t)
	s := ctSpec()
	s.Disks = []driver.Disk{{Slot: 2, SizeGB: 3, Path: "/srv/b"}}
	if _, err := d.Create(context.Background(), s, driver.Bootstrap{Token: ctToken}); err != nil {
		t.Fatal(err)
	}
	if p := f.lastParams("POST /nodes/pve/lxc"); p.Get("mp2") != "local-lvm:3,mp=/srv/b,backup=1" {
		t.Fatalf("create %v", p)
	}
}

// M1 of #373: a remove frees exactly its own volume, also when the guest
// has other unused volumes (an old disk kept by a restore, T-244 D9).
func TestVMRemoveFreesOnlyItsVolume(t *testing.T) {
	f, d, id, _ := newRunningGuest(t)
	ctx := context.Background()
	f.mu.Lock()
	old := []string{"local-lvm:vm-9001-disk-5", "local-lvm:vm-9001-disk-6", "local-lvm:vm-9001-disk-7"}
	for i, v := range old {
		f.vms[9001].cfg["unused"+strconv.Itoa(i)] = v
	}
	f.mu.Unlock()
	for round := 0; round < 6; round++ {
		if err := d.Update(ctx, id, []wire.Change{diskChange(2, nil, "1 GiB")}, nil); err != nil {
			t.Fatal(err)
		}
		vol := parseProps(f.vms[9001].cfg["scsi2"])[""]
		if err := d.Update(ctx, id, []wire.Change{diskChange(2, "1 GiB", nil)}, nil); err != nil {
			t.Fatal(err)
		}
		if last := f.disks.freed[len(f.disks.freed)-1]; last != vol {
			t.Fatalf("round %d: freed %s, want %s", round, last, vol)
		}
	}
	left := guestVolumes(f.vms[9001].cfg)
	for _, v := range old {
		if !left[v] {
			t.Errorf("the old unused volume %s was freed", v)
		}
	}
}

// M1 of #373: a remove that Proxmox keeps (pending) frees nothing.
func TestVMRemoveStillAttached(t *testing.T) {
	f, d, id, _ := newRunningGuest(t)
	ctx := context.Background()
	if err := d.Update(ctx, id, []wire.Change{diskChange(1, nil, "1 GiB")}, nil); err != nil {
		t.Fatal(err)
	}
	f.disks.keepDisk = true
	err := d.Update(ctx, id, []wire.Change{diskChange(1, "1 GiB", nil)}, nil)
	if err == nil || !strings.Contains(err.Error(), "still attached") || len(f.disks.freed) != 0 {
		t.Fatalf("err %v freed %v", err, f.disks.freed)
	}
}

// M1 of #373: a free answered OK whose unused entry stays is a volume left.
func TestVMFreeReadBack(t *testing.T) {
	f, d, id, _ := newRunningGuest(t)
	ctx := context.Background()
	if err := d.Update(ctx, id, []wire.Change{diskChange(1, nil, "1 GiB")}, nil); err != nil {
		t.Fatal(err)
	}
	f.disks.freeNoop = true
	var ve *driver.VolumeLeftError
	if err := d.Update(ctx, id, []wire.Change{diskChange(1, "1 GiB", nil)}, nil); !errors.As(err, &ve) || !strings.Contains(err.Error(), "still in the config") {
		t.Fatalf("free no-op: %v", err)
	}
}

// H1 of #373: an add never writes over anything in the slot (a bind
// mount, a device), also when the caller skipped the diff.
func TestCTAddOccupiedSlot(t *testing.T) {
	for _, raw := range []string{"/srv/host,mp=/data", "/dev/sdb,mp=/data", "other:vm-9000-disk-3,mp=/data,size=1G"} {
		f, d, id := newCT(t)
		f.mu.Lock()
		f.vms[9000].cfg["mp1"] = raw
		f.mu.Unlock()
		var fe *driver.DiskForeignError
		if err := d.Update(context.Background(), id, []wire.Change{diskChange(1, nil, "1 GiB at /data")}, nil); !errors.As(err, &fe) {
			t.Errorf("%s: %v", raw, err)
		}
		if f.vms[9000].cfg["mp1"] != raw {
			t.Errorf("%s: overwritten with %s", raw, f.vms[9000].cfg["mp1"])
		}
	}
}

// H2 of #373: the undo frees only the volume that this add made. When
// the slot names an older volume of the guest at the failed start, it is
// detached only and named as a volume left.
func TestCTUndoFreesOnlyNewVolume(t *testing.T) {
	f, d, id := newCT(t)
	ctx := context.Background()
	f.mu.Lock()
	f.vms[9000].cfg["unused0"] = "local-lvm:vm-9000-disk-5"
	f.mu.Unlock()
	f.disks.symlinks = map[string]bool{"/srv/lnk": true}
	f.disks.onStart = func(vm *fakeVM) { // the slot changes in the window
		if strings.Contains(vm.cfg["mp1"], "/srv/lnk") {
			vm.cfg["mp1"] = "local-lvm:vm-9000-disk-5,mp=/srv/lnk,size=1G"
			delete(vm.cfg, "unused0")
		}
	}
	err := d.Update(ctx, id, []wire.Change{diskChange(1, nil, "1 GiB at /srv/lnk")}, nil)
	var ve *driver.VolumeLeftError
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), "not the volume this Run made") {
		t.Fatalf("undo: %v", err)
	}
	if len(f.disks.freed) != 0 || !f.vms[9000].running {
		t.Fatalf("freed %v running %v", f.disks.freed, f.vms[9000].running)
	}
}

// pl-lead before the merge of #373: the ownership check of the undo is
// pinned on its own. A NEW volume in the slot at the failed start that is
// not Plumb's (another storage, or another owner's name) is detached only,
// never freed, and named as a volume left.
func TestCTUndoNeverFreesForeignNewVolume(t *testing.T) {
	for _, foreign := range []string{"other:vm-9000-disk-77", "local-lvm:vm-1234-disk-77"} {
		f, d, id := newCT(t)
		ctx := context.Background()
		f.disks.symlinks = map[string]bool{"/srv/lnk": true}
		f.disks.onStart = func(vm *fakeVM) {
			if strings.Contains(vm.cfg["mp1"], "/srv/lnk") && !strings.HasPrefix(vm.cfg["mp1"], foreign) {
				vm.cfg["mp1"] = foreign + ",mp=/srv/lnk,size=1G"
			}
		}
		err := d.Update(ctx, id, []wire.Change{diskChange(1, nil, "1 GiB at /srv/lnk")}, nil)
		var ve *driver.VolumeLeftError
		if !errors.As(err, &ve) || ve.Volume != foreign || !strings.Contains(err.Error(), "not the volume this Run made") {
			t.Fatalf("%s: %v", foreign, err)
		}
		if len(f.disks.freed) != 0 {
			t.Fatalf("%s: freed %v", foreign, f.disks.freed)
		}
		if _, ok := f.vms[9000].cfg["mp1"]; ok || !f.vms[9000].running {
			t.Fatalf("%s: not detached or not running: %v", foreign, f.vms[9000].cfg)
		}
	}
}
