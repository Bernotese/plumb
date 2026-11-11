package proxmox

import (
	"context"
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
)

// A matching name must never substitute for a complete ownership line.
func TestFindImageRequiresFullBuildLineQA(t *testing.T) {
	for _, line := range []string{"", "build=" + tDigest[:12], "build=" + tDigest + "extra", "prefix-build=" + tDigest} {
		t.Run(line, func(t *testing.T) {
			f := newFakePVE(t)
			d := f.driver(t)
			f.addVM(9010, fakePool, map[string]string{
				"name": "plumb-img-web-v1", "template": "1",
				"description": "plumb-image: web\nversion=1\n" + line,
			}, false)
			ref, err := d.FindImage(context.Background(), "web", 1, tDigest)
			if !errors.Is(err, driver.ErrNotFound) || ref != "" {
				t.Fatalf("adopted without full build line: %q, %v", ref, err)
			}
		})
	}
}

// An unreadable storage status must not become apparent free capacity.
func TestDiskSpaceUnreadableQA(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	d.cfg.DiskStorage = "unreadable" // fake PVE returns an API error for this storage
	ds, err := d.DiskSpace(context.Background())
	if err == nil || ds != (driver.DiskSpace{}) {
		t.Fatalf("unknown use returned capacity: %+v, %v", ds, err)
	}
}
