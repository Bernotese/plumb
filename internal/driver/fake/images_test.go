package fake

import (
	"context"
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

const buildDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestImageBuildLifecycle(t *testing.T) {
	ctx := context.Background()
	d := New()
	d.SetDiskSpace(100*GiB, 10*GiB, true)
	base, err := d.FetchImage(ctx, driver.ImageSpec{Name: "web", Kind: wire.KindVM, URL: "https://x/i.img", SHA256: sum})
	if err != nil {
		t.Fatal(err)
	}
	bs := driver.BuildSpec{Image: "web", Version: 1, Base: base, Upgrade: true, Cores: 1, MemoryMB: 1024, DiskGB: 10, Bridge: "vmbr0", IP: "dhcp"}
	gid, err := d.CreateBuild(ctx, bs, driver.Bootstrap{Token: "tok-b"})
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := d.BuildOf(gid); !ok || b.Version != 1 {
		t.Fatalf("BuildOf %v %v", b, ok)
	}
	if _, err := d.CreateBuild(ctx, bs, driver.Bootstrap{Token: "tok-c"}); err == nil {
		t.Error("two build guests of one version")
	}
	ds, _ := d.DiskSpace(ctx)
	if ds.UsedBytes != 10*GiB+3*GiB+10*GiB || !ds.MetaKnown {
		t.Fatalf("disk %+v", ds)
	}
	img := driver.BuiltImage{Image: "web", Version: 1, Digest: buildDigest}
	if _, err := d.MakeImage(ctx, gid, img); err == nil {
		t.Error("MakeImage of a running guest")
	}
	if err := d.Power(ctx, gid, driver.PowerStop); err != nil {
		t.Fatal(err)
	}
	if _, err := d.MakeImage(ctx, gid, driver.BuiltImage{Image: "web", Version: 2, Digest: buildDigest}); err == nil {
		t.Error("MakeImage of another version")
	}
	ref, err := d.MakeImage(ctx, gid, img)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Observe(ctx, gid); !errors.Is(err, driver.ErrNotFound) {
		t.Error("the build guest is still a guest")
	}
	if got, err := d.FindImage(ctx, "web", 1, buildDigest); err != nil || got != ref {
		t.Fatalf("find %s %v", got, err)
	}
	if _, err := d.FindImage(ctx, "web", 1, sum); !errors.Is(err, driver.ErrNotFound) {
		t.Error("found with another digest")
	}

	// A Target of the version carries the version; Diff ignores it.
	spec := driver.GuestSpec{Name: "t1", Kind: wire.KindVM, ImageName: "web", Image: ref, ImageVersion: 1, Cores: 1, MemoryMB: 512, DiskGB: 10, Bridge: "vmbr0", IP: "dhcp", User: "ops"}
	tid, err := d.Create(ctx, spec, driver.Bootstrap{Token: "tok-t"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := d.Observe(ctx, tid)
	if err != nil || st.Spec.ImageVersion != 1 {
		t.Fatalf("observe %v %v", st.Spec.ImageVersion, err)
	}
	spec.ImageVersion = 2
	if ch, err := d.Diff(spec, st); err != nil || len(ch) != 0 {
		t.Errorf("diff %v %v", ch, err)
	}

	objs, err := d.ListImages(ctx, "web")
	if err != nil || len(objs) != 3 {
		t.Fatalf("list %+v %v", objs, err)
	}
	if objs[0].Kind != driver.ImageBaseTemplate || objs[1].Kind != driver.ImageVersionTemplate || objs[2].Kind != driver.ImageImportFile {
		t.Fatalf("order %+v", objs)
	}
	if objs[1].Name != "plumb-img-web-v1" || objs[1].Image != "web" {
		t.Errorf("version %+v", objs[1])
	}

	// Guard: import file, a wrong digest, a linked clone.
	if err := d.RemoveImage(ctx, objs[2]); err == nil {
		t.Error("removed an import file")
	}
	wrong := objs[1]
	wrong.Digest = sum
	if err := d.RemoveImage(ctx, wrong); err == nil || errors.Is(err, driver.ErrImageInUse) {
		t.Errorf("another digest: %v", err)
	}
	d.LinkClone(ref)
	if err := d.RemoveImage(ctx, objs[1]); !errors.Is(err, driver.ErrImageInUse) {
		t.Errorf("linked clone: %v, want ErrImageInUse (D-232)", err)
	}
	d.UnlinkClone(ref)
	if err := d.RemoveImage(ctx, objs[1]); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveImage(ctx, objs[1]); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("gone: %v", err)
	}
	// The full clone keeps running.
	if st, err := d.Observe(ctx, tid); err != nil || st.Power != driver.PowerRunning {
		t.Errorf("target after remove: %v %v", st.Power, err)
	}
	if err := d.RemoveImage(ctx, objs[0]); err != nil {
		t.Fatal(err)
	}
	if objs, _ := d.ListImages(ctx, "web"); len(objs) != 1 || objs[0].Kind != driver.ImageImportFile {
		t.Errorf("after remove %+v", objs)
	}
	// FetchImage makes the base template again; the import file stays one.
	if _, err := d.FetchImage(ctx, driver.ImageSpec{Name: "web", Kind: wire.KindVM, URL: "https://x/i.img", SHA256: sum}); err != nil {
		t.Fatal(err)
	}
	if objs, _ := d.ListImages(ctx, "web"); len(objs) != 2 {
		t.Errorf("after fetch %+v", objs)
	}
}

func TestImageOpsInjectFailures(t *testing.T) {
	d := New()
	boom := errors.New("boom")
	for _, op := range []Op{OpCreateBuild, OpMakeImage, OpFindImage, OpListImages, OpRemoveImage, OpDiskSpace} {
		d.Fail(op, boom)
	}
	ctx := context.Background()
	_, e1 := d.CreateBuild(ctx, driver.BuildSpec{}, driver.Bootstrap{})
	_, e2 := d.MakeImage(ctx, "1", driver.BuiltImage{})
	_, e3 := d.FindImage(ctx, "x", 1, buildDigest)
	_, e4 := d.ListImages(ctx, "x")
	e5 := d.RemoveImage(ctx, driver.ImageObject{})
	_, e6 := d.DiskSpace(ctx)
	for i, err := range []error{e1, e2, e3, e4, e5, e6} {
		if !errors.Is(err, boom) {
			t.Errorf("op %d: %v", i, err)
		}
	}
}
