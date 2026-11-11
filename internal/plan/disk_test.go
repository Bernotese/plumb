package plan

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
)

// diskDriver answers DiskSpace only.
type diskDriver struct {
	driver.Driver
	mu  sync.Mutex
	ds  driver.DiskSpace
	err error
}

func (d *diskDriver) DiskSpace(context.Context) (driver.DiskSpace, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ds, d.err
}

func TestDiskGateExactLimit(t *testing.T) {
	ctx := context.Background()
	drv := &diskDriver{ds: driver.DiskSpace{Storage: "local-lvm", TotalBytes: 100 * GiB, UsedBytes: 75 * GiB}}
	g := NewDiskGate(drv, 0)
	if g.Limit() != 85 {
		t.Fatalf("default limit %d", g.Limit())
	}
	if err := g.Check(ctx, 10*GiB, "x"); err != nil {
		t.Fatalf("85.0 %%: %v", err)
	}
	err := g.Check(ctx, 10*GiB+1, "the 10 GiB disk of web1")
	if !errors.Is(err, ErrDiskFull) {
		t.Fatalf("one byte over: %v", err)
	}
	for _, want := range []string{"local-lvm would be 85.1 % full", "the 10 GiB disk of web1", "75.0 GiB used", "0.0 GiB held", "100.0 GiB in total", "limit 85 %"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q misses %q", err, want)
		}
	}
	// A size of 0 is no room.
	drv.ds.TotalBytes = 0
	if err := g.Check(ctx, 1, "x"); !errors.Is(err, ErrDiskFull) {
		t.Errorf("total 0: %v", err)
	}
}

func TestDiskGateReservations(t *testing.T) {
	ctx := context.Background()
	drv := &diskDriver{ds: driver.DiskSpace{Storage: "s", TotalBytes: 100 * GiB, UsedBytes: 50 * GiB}}
	g := NewDiskGate(drv, 85)
	r1, err := g.Reserve(ctx, 20*GiB, "a")
	if err != nil {
		t.Fatal(err)
	}
	if g.Reserved() != 20*GiB {
		t.Fatalf("reserved %d", g.Reserved())
	}
	if err := g.Check(ctx, 16*GiB, "b"); !errors.Is(err, ErrDiskFull) || !strings.Contains(err.Error(), "20.0 GiB held") {
		t.Errorf("reservation not counted: %v", err)
	}
	if g.Reserved() != 20*GiB {
		t.Error("Check reserved")
	}
	r1()
	r1() // once
	if g.Reserved() != 0 {
		t.Fatalf("after release %d", g.Reserved())
	}
	if _, err := g.Reserve(ctx, 35*GiB, "c"); err != nil {
		t.Fatal(err)
	}

	// Parallel: 10 reservations of 5 GiB on 100 GiB with 50 used: exactly 7 fit.
	drv.ds.UsedBytes = 50 * GiB
	g = NewDiskGate(drv, 85)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := g.Reserve(ctx, 5*GiB, "p"); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 7 || g.Reserved() != 35*GiB {
		t.Fatalf("%d fit, %d reserved", ok, g.Reserved())
	}
}

func TestDiskGateMetadataAndErrors(t *testing.T) {
	ctx := context.Background()
	drv := &diskDriver{ds: driver.DiskSpace{Storage: "s", TotalBytes: 100 * GiB, UsedBytes: 10 * GiB}}
	g := NewDiskGate(drv, 85)
	// Unknown metadata use is no refusal (T-062 card).
	if err := g.Check(ctx, GiB, "x"); err != nil {
		t.Fatal(err)
	}
	drv.ds.MetaKnown, drv.ds.MetaTotalBytes, drv.ds.MetaUsedBytes = true, 100<<20, 85<<20
	if err := g.Check(ctx, GiB, "x"); err != nil {
		t.Fatalf("metadata at 85.0 %%: %v", err)
	}
	drv.ds.MetaUsedBytes++
	if err := g.Check(ctx, GiB, "x"); !errors.Is(err, ErrDiskFull) || !strings.Contains(err.Error(), "thin-pool metadata of s") {
		t.Fatalf("metadata over: %v", err)
	}
	// A storage that cannot be read refuses, but it is no ErrDiskFull.
	drv.err = errors.New("403")
	_, err := g.Reserve(ctx, GiB, "the 1 GiB disk of w")
	if err == nil || errors.Is(err, ErrDiskFull) || !strings.Contains(err.Error(), "cannot read the use") {
		t.Fatalf("read error: %v", err)
	}
	if g.Reserved() != 0 {
		t.Error("a refusal reserved")
	}
}
