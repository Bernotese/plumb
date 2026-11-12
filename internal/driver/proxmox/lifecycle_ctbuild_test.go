package proxmox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// hostCTBuilt is the version of the host test build (T-082 H-B1).
var hostCTBuilt = driver.BuiltImage{Image: "t082-debian-13", Version: 1, Digest: strings.Repeat("e", 64)}

// runCTBuild (T-082 H-B1): the build container from the template file,
// stopped (no agent seals in this test), MakeImage with the env checks of
// review S8, a Target as a full clone without an inherited env, delete,
// RemoveImage of the version. The template file exists (TestHostCTFetch).
func runCTBuild(t *testing.T, d *Driver, img driver.ImageSpec, bridge string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	base, err := d.FetchImage(ctx, img)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := d.CreateBuild(ctx, driver.BuildSpec{Image: hostCTBuilt.Image, Version: 1, Base: base, Cores: 1, MemoryMB: 512, DiskGB: 4,
		Bridge: bridge, IP: "dhcp"}, driver.Bootstrap{Token: "host-test-build-token-1"})
	if err != nil {
		t.Fatal(err)
	}
	var ref driver.ImageRef
	t.Cleanup(func() {
		if ref == "" {
			if err := d.RemoveBuild(context.Background(), gid, hostCTBuilt.Image, 1); err != nil {
				t.Errorf("cleanup of build container %s: %v", gid, err)
			}
		}
	})
	if err := d.Power(ctx, gid, driver.PowerStop); err != nil {
		t.Fatal(err)
	}
	if ref, err = d.MakeImage(ctx, gid, hostCTBuilt); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		objs, err := d.ListImages(context.Background(), hostCTBuilt.Image)
		if err != nil {
			t.Errorf("cleanup list: %v", err)
			return
		}
		for _, o := range objs {
			if o.Kind == driver.ImageVersionTemplate {
				if err := d.RemoveImage(context.Background(), o); err != nil {
					t.Errorf("cleanup of version %s: %v", o.Name, err)
				}
			}
		}
	})
	if found, err := d.FindImage(ctx, hostCTBuilt.Image, 1, hostCTBuilt.Digest); err != nil || found != ref {
		t.Errorf("FindImage %s %v", found, err)
	}
	spec := driver.GuestSpec{Name: "plumb-ctb-test", Kind: wire.KindLXC, ImageName: hostCTBuilt.Image, ImageVersion: 1, Image: ref,
		Cores: 1, MemoryMB: 512, DiskGB: 5, Bridge: bridge, IP: "dhcp", Features: driver.CTFeatures}
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "host-test-build-token-2"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := d.Observe(ctx, id)
	if err != nil || st.Power != driver.PowerRunning || !st.TokenInConfig || st.Spec.DiskGB != 5 {
		t.Errorf("clone: %+v %v", st, err)
	}
	if c, err := d.Diff(spec, st); err != nil || len(c) != 0 {
		t.Errorf("round trip: %+v %v", c, err)
	}
	if err := d.Delete(ctx, id); err != nil {
		t.Errorf("delete: %v", err)
	}
}
