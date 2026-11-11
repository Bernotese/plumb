package proxmox

// The image build flow that TestHostImageBuild runs on the real host (make
// test-pve, T-061 host step) and TestReplayImageBuild runs offline against
// the recorded answers in testdata/pve/images. The flow also logs the
// host facts that T-061 must record: the storage and metadata use that the
// token reads, and which of the two linked-clone signs it sees.

import (
	"context"
	"errors"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// flowDigest is the build digest of the flow's version (no real recipe).
const flowDigest = "f10f10f10f10f10f10f10f10f10f10f10f10f10f10f10f10f10f10f10f10f10f"

func runImageBuild(t *testing.T, d *Driver, img driver.ImageSpec, bridge string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	// Whatever the flow leaves is removed by name, through the client:
	// the build guest, the linked clone, the Target and both templates.
	t.Cleanup(func() { cleanupImageFlow(t, d, img.Name) })

	ds, err := d.DiskSpace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("host fact: DiskSpace %s total=%d used=%d metaKnown=%v metaTotal=%d metaUsed=%d",
		ds.Storage, ds.TotalBytes, ds.UsedBytes, ds.MetaKnown, ds.MetaTotalBytes, ds.MetaUsedBytes)

	base, err := d.FetchImage(ctx, img)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := d.CreateBuild(ctx, driver.BuildSpec{Image: img.Name, Version: 1, Base: base, Upgrade: false,
		Cores: 1, MemoryMB: 1024, DiskGB: 10, Bridge: bridge, IP: "dhcp"}, driver.Bootstrap{Token: "host-test-build-token"})
	if err != nil {
		t.Fatal(err)
	}
	// The flow does not wait for an agent or a seal: a hard stop is enough
	// for the driver steps.
	id, _ := strconv.Atoi(string(gid))
	if err := d.c.Task(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/status/stop", nil); err != nil {
		t.Fatal(err)
	}
	built := driver.BuiltImage{Image: img.Name, Version: 1, Digest: flowDigest}
	ref, err := d.MakeImage(ctx, gid, built)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := d.FindImage(ctx, img.Name, 1, flowDigest); err != nil || got != ref {
		t.Fatalf("FindImage: %s %v", got, err)
	}
	objs, err := d.ListImages(ctx, img.Name)
	if err != nil {
		t.Fatal(err)
	}
	var version, baseObj driver.ImageObject
	for _, o := range objs {
		t.Logf("ListImages: %s %s digest=%.12s size=%d linked=%d", o.Kind, o.Name, o.Digest, o.SizeBytes, o.LinkedClones)
		switch o.Kind {
		case driver.ImageVersionTemplate:
			version = o
		case driver.ImageBaseTemplate:
			baseObj = o
		}
	}
	if version.Ref != ref || baseObj.Ref != base {
		t.Fatalf("ListImages: version %q base %q", version.Ref, baseObj.Ref)
	}

	// A Target of the version: full clone, version marker.
	tid, err := d.Create(ctx, driver.GuestSpec{Name: "plumb-host-test-img", Kind: wire.KindVM, ImageName: img.Name, Image: ref, ImageVersion: 1,
		Cores: 1, MemoryMB: 1024, DiskGB: 10, Bridge: bridge, IP: "dhcp", User: "plumb"}, driver.Bootstrap{Token: "host-test-token-img"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := d.Observe(ctx, tid)
	if err != nil || st.Spec.ImageVersion != 1 {
		t.Fatalf("Observe: version %d %v", st.Spec.ImageVersion, err)
	}
	if err := d.Delete(ctx, tid); err != nil {
		t.Fatal(err)
	}

	// A linked clone made by hand (review S3) blocks RemoveImage.
	lid, err := d.nextID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d.releaseID(lid)
	if err := d.c.Task(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/clone", url.Values{
		"newid": {strconv.Itoa(lid)}, "name": {"plumb-host-test-linked"}, "pool": {d.cfg.Pool}, "full": {"0"},
	}); err != nil {
		t.Fatal(err)
	}
	ms, err := d.members(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := d.scanPool(ctx, ms)
	if err != nil {
		t.Fatal(err)
	}
	inConfig := slices.ContainsFunc(diskVolumes(scan.configs[lid]), func(v string) bool { return strings.Contains(v, "/") })
	inParent := slices.ContainsFunc(scan.volumes, func(v storageVolume) bool { return v.vmid() == lid && v.Parent != "" })
	t.Logf("host fact: linked clone %d: disk lines %v (base in line: %v), volume parent seen: %v",
		lid, diskVolumes(scan.configs[lid]), inConfig, inParent)
	err = d.RemoveImage(ctx, version)
	if err == nil || !strings.Contains(err.Error(), "linked clones") {
		t.Errorf("RemoveImage under a linked clone: %v (if neither sign is seen, the limit goes into the docs)", err)
	}
	if err := d.Delete(ctx, driver.GuestID(strconv.Itoa(lid))); err != nil {
		t.Fatal(err)
	}

	if err := d.RemoveImage(ctx, version); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveImage(ctx, baseObj); err != nil {
		t.Fatal(err)
	}
	if _, err := d.FindImage(ctx, img.Name, 1, flowDigest); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("after RemoveImage: %v", err)
	}
	if err := d.RemoveImage(ctx, version); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("RemoveImage of a gone template: %v", err)
	}
}

// cleanupImageFlow removes the flow's guests and templates by exact name,
// in the pool and the range. Import files stay (D-209): the host step
// frees them by hand with pvesm free.
func cleanupImageFlow(t *testing.T, d *Driver, image string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	names := map[string]bool{
		buildName(image, 1): true, versionName(image, 1): true, "plumb-host-test-img": true, "plumb-host-test-linked": true,
	}
	ms, err := d.members(ctx)
	if err != nil {
		t.Errorf("cleanup: %v", err)
		return
	}
	// Clones first, then templates.
	slices.SortFunc(ms, func(a, b member) int { return b.Template - a.Template })
	for _, m := range ms {
		if m.VMID < d.cfg.VMIDMin || m.VMID > d.cfg.VMIDMax {
			continue
		}
		cfg, err := d.config(ctx, m.VMID)
		if err != nil {
			continue
		}
		name := cfg["name"]
		if !names[name] && !strings.HasPrefix(name, "plumb-img-"+image+"-") {
			continue
		}
		if st, _ := d.status(ctx, m.VMID); st == "running" {
			_ = d.c.Task(ctx, d.cfg.Node, "POST", vmPath(d, m.VMID)+"/status/stop", nil)
		}
		if err := d.c.Task(ctx, d.cfg.Node, "DELETE", vmPath(d, m.VMID), url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}}); err != nil {
			t.Errorf("cleanup of VMID %d (%s): %v; remove it by hand", m.VMID, name, err)
		} else {
			t.Logf("cleanup: removed VMID %d (%s)", m.VMID, name)
		}
	}
}

// TestReplayImageBuild runs the flow against the recording of the T-061
// host step. It skips until pl-backend adds testdata/pve/images.
func TestReplayImageBuild(t *testing.T) {
	if _, err := os.Stat("testdata/pve/images"); errors.Is(err, os.ErrNotExist) {
		t.Skip("no recording yet: T-061 host step (make test-pve, PLUMB_PVE_RECORD)")
	}
	rs, d := newReplay(t, "testdata/pve/images")
	t.Cleanup(func() {
		if len(rs.missed) > 0 {
			t.Errorf("requests without a recording: %v", rs.missed)
		}
	})
	runImageBuild(t, d, replayImage, "vmbr0")
}

// TestImageBuildFlowFakePVE runs the host flow against the fake PVE, so the
// flow itself is tested before the host step; nothing is left at the end.
func TestImageBuildFlowFakePVE(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	runImageBuild(t, d, buildImageSpec("flow"), "vmbr0")
	t.Cleanup(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.vms) != 0 {
			t.Errorf("left: %d VMs", len(f.vms))
		}
	})
}
