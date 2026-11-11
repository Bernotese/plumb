package proxmox

// T-073: a failed guest create leaves no VM behind (test host A4: a disk
// smaller than the template's, and a pool list without the new name).

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
)

// The fake's template disk is 3584M: 3 GB is too small, 4 GB is enough.
func TestCreateRefusesSmallerDisk(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, err := d.FetchImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	spec := guest(ref)
	spec.DiskGB = 3
	_, err = d.Create(ctx, spec, driver.Bootstrap{Token: "tok-1"})
	if err == nil || !strings.Contains(err.Error(), "disk 3 GB is smaller than the 4 GB disk of image debian") ||
		!strings.Contains(err.Error(), "set disk to 4 or more") {
		t.Fatalf("err = %v", err)
	}
	if n := f.count("POST /nodes/pve/qemu/" + string(ref) + "/clone"); n != 0 {
		t.Errorf("clones: %d, want 0 (refuse before the clone)", n)
	}
	if len(f.vms) != 1 {
		t.Errorf("VMs: %d, want only the template", len(f.vms))
	}
	spec.DiskGB = 4
	if _, err := d.Create(ctx, spec, driver.Bootstrap{Token: "tok-1"}); err != nil {
		t.Errorf("disk 4 GB on a 3.5 GB template: %v", err)
	}
}

func TestSizeMB(t *testing.T) {
	for in, want := range map[string]int{"10G": 10240, "3584M": 3584, "1T": 1048576, "2049K": 3, "1.5G": 1536, "": 0, "10": 0, "xG": 0} {
		if got := sizeMB(in); got != want {
			t.Errorf("sizeMB(%q) = %d, want %d", in, got, want)
		}
	}
}

// Every failure after the clone removes the new VM, also when the pool
// list has no name for it yet (the cache of the real host). The error
// names the cause; the template stays.
func TestFailedCreateRemovesClone(t *testing.T) {
	for _, step := range []struct{ name, key, cause string }{
		{"config", "POST /nodes/pve/qemu/%d/config", "proxmox: configure"},
		{"resize", "PUT /nodes/pve/qemu/%d/resize", "proxmox: resize disk"},
		{"start", "POST /nodes/pve/qemu/%d/status/start", "proxmox: start"},
	} {
		t.Run(step.name, func(t *testing.T) {
			f := newFakePVE(t)
			d := f.driver(t)
			ctx := context.Background()
			ref, err := d.FetchImage(ctx, image)
			if err != nil {
				t.Fatal(err)
			}
			tpl, _ := vmid(string(ref))
			id := tpl + 1
			f.staleNames = true
			f.failNext[fmt.Sprintf(step.key, id)] = "TASK ERROR: " + step.name + " failed"
			_, err = d.Create(ctx, guest(ref), driver.Bootstrap{Token: "tok-1"})
			if err == nil || !strings.Contains(err.Error(), step.cause) || !strings.Contains(err.Error(), step.name+" failed") {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), "is left") || strings.Contains(err.Error(), "tok-1") {
				t.Errorf("err = %v", err)
			}
			if _, ok := f.vms[id]; ok {
				t.Errorf("the cloned VM %d is left", id)
			}
			if _, ok := f.vms[tpl]; !ok {
				t.Error("the template is gone")
			}
			if n := f.count("DELETE /nodes/pve/qemu/"); n != 1 || f.count(fmt.Sprintf("DELETE /nodes/pve/qemu/%d", id)) != 1 {
				t.Errorf("DELETEs: %v", f.log)
			}
		})
	}
}

// The removal fails: the error names the cause, the VMID and the
// qm destroy hint; the VM stays.
func TestFailedCreateRemovalFails(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	tpl, _ := vmid(string(ref))
	id := tpl + 1
	f.staleNames = true
	f.failNext[fmt.Sprintf("PUT /nodes/pve/qemu/%d/resize", id)] = "TASK ERROR: shrinking disks is not supported"
	f.failNext[fmt.Sprintf("DELETE /nodes/pve/qemu/%d", id)] = "storage is busy"
	_, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "tok-1"})
	want := []string{"resize disk", "shrinking disks", fmt.Sprintf("the half-made VM %d is left", id), "storage is busy", fmt.Sprintf("qm destroy %d", id)}
	for _, w := range want {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("err = %v; want %q", err, w)
		}
	}
	if _, ok := f.vms[id]; !ok {
		t.Error("the VM is gone although the DELETE failed")
	}
	// The next create goes on with a new VMID; the leftover is not touched.
	gid, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "tok-2"})
	if err != nil || gid == driver.GuestID(fmt.Sprint(id)) {
		t.Fatalf("next create: %s %v", gid, err)
	}
	if _, ok := f.vms[id]; !ok {
		t.Error("the next create removed the leftover")
	}
}

// After the clone the VM has another name (not proven ours): no DELETE,
// and the error says that it is left.
func TestFailedCreateForeignNameNotRemoved(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	tpl, _ := vmid(string(ref))
	id := tpl + 1
	f.cloneName = "someone-else"
	f.failNext[fmt.Sprintf("POST /nodes/pve/qemu/%d/status/start", id)] = "start failed"
	_, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "tok-1"})
	if err == nil || !strings.Contains(err.Error(), "start failed") || !strings.Contains(err.Error(), "not proven ours") ||
		!strings.Contains(err.Error(), fmt.Sprintf("the half-made VM %d is left", id)) {
		t.Fatalf("err = %v", err)
	}
	if n := f.count("DELETE /nodes/pve/qemu/"); n != 0 {
		t.Errorf("DELETEs: %d, want 0", n)
	}
	if _, ok := f.vms[id]; !ok {
		t.Error("a VM that is not proven ours was removed")
	}
}
