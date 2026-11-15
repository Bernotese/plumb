package fake

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-238: the fake keeps the data disks, diffs them by slot and models the
// D-437 failure paths.
func TestFakeDisks(t *testing.T) {
	d := New()
	ctx := context.Background()
	ref, err := d.FetchImage(ctx, driver.ImageSpec{Name: "deb", Kind: wire.KindVM, URL: "https://x/i.img", SHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	spec := driver.GuestSpec{Name: "web1", Kind: wire.KindVM, ImageName: "deb", Image: ref, Cores: 1, MemoryMB: 512, DiskGB: 4, Bridge: "vmbr0",
		Disks: []driver.Disk{{Slot: 2, SizeGB: 3}}, DisksSet: true}
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := d.Observe(ctx, id)
	if ch, err := d.Diff(spec, st); err != nil || len(ch) != 0 {
		t.Fatalf("diff after create: %+v %v", ch, err)
	}
	if dk := st.Spec.DiskAt(2); dk == nil || dk.Volume == "" {
		t.Fatalf("observed %+v", st.Spec.Disks)
	}
	used, _ := d.DiskSpace(ctx)
	next := spec
	next.Disks = []driver.Disk{{Slot: 1, SizeGB: 2}, {Slot: 2, SizeGB: 5}}
	ch, err := d.Diff(next, st)
	if err != nil || len(ch) != 2 {
		t.Fatalf("add+grow: %+v %v", ch, err)
	}
	if err := d.Update(ctx, id, ch, nil); err != nil {
		t.Fatal(err)
	}
	if after, _ := d.DiskSpace(ctx); after.UsedBytes-used.UsedBytes != 4*GiB {
		t.Fatalf("disk space counts the data disks: %d", after.UsedBytes-used.UsedBytes)
	}
	st, _ = d.Observe(ctx, id)
	small := next
	small.Disks = []driver.Disk{{Slot: 1, SizeGB: 2}, {Slot: 2, SizeGB: 1}}
	var se *driver.ShrinkError
	if _, err := d.Diff(small, st); !errors.As(err, &se) || se.Slot != 2 {
		t.Fatalf("shrink: %v", err)
	}
	// A failed free: the disk is detached, the volume is left and named.
	d.FailDiskFree(true)
	rm := next
	rm.Disks = []driver.Disk{{Slot: 2, SizeGB: 5}}
	ch, _ = d.Diff(rm, st)
	var ve *driver.VolumeLeftError
	if err := d.Update(ctx, id, ch, nil); !errors.As(err, &ve) || ve.Slot != 1 || len(d.Leftover()) != 1 {
		t.Fatalf("free fails: %v %v", err, d.Leftover())
	}
	st, _ = d.Observe(ctx, id)
	if st.Spec.DiskAt(1) != nil {
		t.Fatal("slot 1 still attached")
	}
	// A foreign volume: a named slot is refused.
	d.SetForeignDisk(id, 3, "other:vm-1-disk-9")
	st, _ = d.Observe(ctx, id)
	named := rm
	named.Disks = append(named.Disks, driver.Disk{Slot: 3, SizeGB: 1})
	if _, err := d.Diff(named, st); !errors.As(err, new(*driver.DiskForeignError)) {
		t.Fatalf("foreign: %v", err)
	}
}
