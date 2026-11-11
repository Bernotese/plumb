package proxmox

// Review S1 and S2 of PR #20: the cleanup of a half-made VM reports its
// failure, waits for the Proxmox lock, and parallel FetchImage calls of
// the same image make one template.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
)

// A failed start, then a failed DELETE: the error names the VMID that is
// left, and the VM is still there (nothing claims a clean state).
func TestHalfMadeLeftNamesVMID(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	f.failNext["POST /nodes/pve/qemu/9001/status/start"] = "start failed: no memory"
	f.failNext["DELETE /nodes/pve/qemu/9001"] = "storage is busy"
	_, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "tok-1"})
	if err == nil || !strings.Contains(err.Error(), "start failed") ||
		!strings.Contains(err.Error(), "the half-made VM 9001 is left") || !strings.Contains(err.Error(), "qm destroy 9001") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "tok-1") {
		t.Error("the token is in the error")
	}
	if _, ok := f.vms[9001]; !ok {
		t.Error("the VM is gone although the DELETE failed")
	}
}

// A stopped clone leaves "lock: create" for a while: the cleanup waits for
// it, then removes the VM and returns the original error only.
func TestHalfMadeWaitsForLock(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	f.failNext["POST /nodes/pve/qemu/9001/status/start"] = "start failed"
	f.lockReads[9001] = 3
	_, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "tok-1"})
	if err == nil || strings.Contains(err.Error(), "is left") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := f.vms[9001]; ok {
		t.Error("the half-made VM stayed")
	}
}

// FetchImage: a half-made image VM that cannot be removed stops the call;
// no second VM with the same name is made.
func TestFetchImageStopsWhenCleanupFails(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	f.addVM(9005, fakePool, map[string]string{"name": templateName(image), "description": imageDescription(image)}, false)
	f.failNext["DELETE /nodes/pve/qemu/9005"] = "storage is busy"
	_, err := d.FetchImage(ctx, image)
	if err == nil || !strings.Contains(err.Error(), "the half-made VM 9005 is left") {
		t.Fatalf("err = %v", err)
	}
	if n := f.countExact("POST /nodes/pve/qemu"); n != 0 {
		t.Errorf("a second image VM was created (%d)", n)
	}
}

// Review S2: a VM with the template name and a lock is busy, never
// half-made: no DELETE.
func TestFetchImageLockedVMIsBusy(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	f.addVM(9005, fakePool, map[string]string{"name": templateName(image)}, false)
	f.lockReads[9005] = 1 << 30 // a lock that stays: busy after ImageLockWait
	_, err := d.FetchImage(context.Background(), image)
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("err = %v", err)
	}
	if n := f.count("DELETE /nodes/pve/qemu/9005"); n != 0 {
		t.Errorf("a locked VM was deleted (%d)", n)
	}
}

// Review S2: two Targets with the same Image fetch it at the same time:
// one template, no DELETE.
func TestFetchImageInParallel(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	var wg sync.WaitGroup
	refs := make([]driver.ImageRef, 4)
	errs := make([]error, 4)
	for i := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			refs[i], errs[i] = d.FetchImage(context.Background(), image)
		}()
	}
	wg.Wait()
	for i := range refs {
		if errs[i] != nil || refs[i] != refs[0] {
			t.Fatalf("call %d: %s %v (first %s)", i, refs[i], errs[i], refs[0])
		}
	}
	if n := f.countExact("POST /nodes/pve/qemu"); n != 1 {
		t.Errorf("image VMs created: %d, want 1", n)
	}
	if n := f.count("DELETE /nodes/pve/qemu/"); n != 0 {
		t.Errorf("DELETEs: %d, want 0", n)
	}
}

// scrub keeps the context of a wrapped or joined error (it dropped it
// before: only the first typed part was left), removes the token, and the
// typed error is still found.
func TestScrubKeepsContext(t *testing.T) {
	te := &TaskError{UPID: "UPID:x", ExitStatus: "smbios1 tok-9 rejected"}
	err := errors.Join(fmt.Errorf("proxmox: start: %w", te),
		fmt.Errorf("proxmox: the half-made VM 9001 is left: %w; remove it with qm destroy 9001", te))
	got := scrub(err, "tok-9")
	msg := got.Error()
	if strings.Contains(msg, "tok-9") || !strings.Contains(msg, "proxmox: start:") || !strings.Contains(msg, "the half-made VM 9001 is left") {
		t.Errorf("scrubbed: %q", msg)
	}
	var back *TaskError
	if !errors.As(got, &back) || strings.Contains(back.ExitStatus, "tok-9") {
		t.Errorf("typed error: %v %+v", errors.As(got, &back), back)
	}
}
