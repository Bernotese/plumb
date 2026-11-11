package proxmox

import (
	"context"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
)

// A reservation must protect the interval before PVE sees the new VM.
// Keep that interval open explicitly instead of depending on scheduling.
func TestNextIDPendingReservationQA(t *testing.T) {
	d := newFakePVE(t).driver(t)
	ctx := context.Background()
	first, err := d.nextID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.releaseID(first)
	second, err := d.nextID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.releaseID(second)
	if first == second {
		t.Fatalf("pending creates share VMID %d", first)
	}
	d.releaseID(first)
	again, err := d.nextID(ctx)
	if err != nil || again != first {
		t.Fatalf("released VMID: got %d, %v; want %d", again, err, first)
	}
}

// Combine template recovery with a subsequent clone, observe, and delete.
// The API's TestImageDeleteAndCreateAgain covers document deletion/reuse.
func TestImageRun3FakePVEQA(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	for round := range 2 {
		ref, templateID := fetched(t, d)
		spec := guest(ref)
		id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "tok"})
		if err != nil {
			t.Fatalf("round %d create: %v", round, err)
		}
		observed, err := d.Observe(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if changes, err := d.Diff(spec, observed); err != nil || len(changes) != 0 {
			t.Errorf("round %d guest: %+v, %v", round, changes, err)
		}
		if err := d.Delete(ctx, id); err != nil {
			t.Fatal(err)
		}
		// External cleanup destroys only the template; the import file stays.
		f.mu.Lock()
		delete(f.vms, templateID)
		f.mu.Unlock()
	}
	if n := f.countExact(dlKey); n != 1 {
		t.Errorf("downloads across two rounds: %d, want 1", n)
	}
	if n := f.countExact("POST /nodes/pve/qemu"); n != 2 {
		t.Errorf("template creates: %d, want 2", n)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.vms) != 0 {
		t.Errorf("VMs left after cleanup: %d", len(f.vms))
	}
}
