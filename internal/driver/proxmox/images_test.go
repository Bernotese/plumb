package proxmox

// Fake PVE tests of the image build methods (T-061, D-203, D-208, D-209).

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

const (
	tDigest  = "d1d2d3d4d5d6d7d8d9d0e1e2e3e4e5e6e7e8e9e0f1f2f3f4f5f6f7f8f9f0a1a2"
	tDigest2 = "0000000000000000000000000000000000000000000000000000000000000001"
)

func buildImageSpec(name string) driver.ImageSpec {
	return driver.ImageSpec{Name: name, Kind: wire.KindVM, URL: "https://example.org/base.qcow2",
		SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"}
}

func buildSpec(image string, version int, base driver.ImageRef) driver.BuildSpec {
	return driver.BuildSpec{Image: image, Version: version, Base: base, Upgrade: true,
		Cores: 1, MemoryMB: 1024, DiskGB: 10, Bridge: "vmbr0", IP: "dhcp"}
}

// built makes a base template, a build guest, stops it and returns the
// guest id (the steps before MakeImage).
func built(t *testing.T, f *fakePVE, d *Driver, image string, version int) (driver.ImageRef, driver.GuestID) {
	t.Helper()
	ctx := context.Background()
	base, err := d.FetchImage(ctx, buildImageSpec(image))
	if err != nil {
		t.Fatal(err)
	}
	gid, err := d.CreateBuild(ctx, buildSpec(image, version, base), driver.Bootstrap{Token: "tok-build"})
	if err != nil {
		t.Fatal(err)
	}
	f.stop(t, gid)
	return base, gid
}

func (f *fakePVE) stop(t *testing.T, gid driver.GuestID) {
	t.Helper()
	id, _ := strconv.Atoi(string(gid))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vms[id].running = false
}

func (f *fakePVE) cfgOf(gid any) map[string]string {
	id, _ := strconv.Atoi(strings.TrimSpace(strings.Trim(toString(gid), "\"")))
	f.mu.Lock()
	defer f.mu.Unlock()
	vm, ok := f.vms[id]
	if !ok {
		return nil
	}
	out := map[string]string{}
	for k, v := range vm.cfg {
		out[k] = v
	}
	return out
}

func toString(v any) string {
	switch x := v.(type) {
	case driver.GuestID:
		return string(x)
	case driver.ImageRef:
		return string(x)
	case int:
		return strconv.Itoa(x)
	}
	return ""
}

// linkedClone makes a linked clone of a template as a person would (qm
// clone --full 0), in the pool.
func linkedClone(t *testing.T, d *Driver, tpl driver.ImageRef, newid int) {
	t.Helper()
	id, _ := strconv.Atoi(string(tpl))
	if err := d.c.Task(context.Background(), d.cfg.Node, "POST", vmPath(d, id)+"/clone", url.Values{
		"newid": {strconv.Itoa(newid)}, "name": {"by-hand"}, "pool": {d.cfg.Pool}, "full": {"0"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateBuild(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	base, err := d.FetchImage(ctx, buildImageSpec("web"))
	if err != nil {
		t.Fatal(err)
	}
	gid, err := d.CreateBuild(ctx, buildSpec("web", 3, base), driver.Bootstrap{Token: "tok-build"})
	if err != nil {
		t.Fatal(err)
	}
	clone := f.lastParams("POST /nodes/pve/qemu/" + string(base) + "/clone")
	if clone.Get("full") != "1" || clone.Get("name") != "plumb-build-web-v3" || clone.Get("pool") != "plumb" {
		t.Fatalf("clone %v", clone)
	}
	cfg := f.cfgOf(gid)
	for k, want := range map[string]string{"name": "plumb-build-web-v3", "onboot": "0", "ciuser": "plumb-build", "ciupgrade": "1", "ipconfig0": "ip=dhcp"} {
		if cfg[k] != want {
			t.Errorf("%s = %q, want %q", k, cfg[k], want)
		}
	}
	if _, ok := cfg["sshkeys"]; ok {
		t.Error("a build guest has no SSH key")
	}
	if !hasLine(cfg["description"], "plumb-build: web v3") || strings.Contains(cfg["description"], imageMarker) {
		t.Errorf("description %q: build marker, and no Target marker", cfg["description"])
	}
	if !strings.Contains(cfg["smbios1"], "serial=") {
		t.Error("no token")
	}
	st, err := d.Observe(ctx, gid)
	if err != nil || st.Power != driver.PowerRunning {
		t.Fatalf("observe %v %v", st.Power, err)
	}

	// Without upgrade ciupgrade is 0; the name of a running build is refused.
	if _, err := d.CreateBuild(ctx, buildSpec("web", 3, base), driver.Bootstrap{Token: "x"}); err == nil || !strings.Contains(err.Error(), "already has the name") {
		t.Fatalf("second build of the same version: %v", err)
	}
	s := buildSpec("web", 4, base)
	s.Upgrade = false
	gid4, err := d.CreateBuild(ctx, s, driver.Bootstrap{Token: "tok-4"})
	if err != nil {
		t.Fatal(err)
	}
	if f.cfgOf(gid4)["ciupgrade"] != "0" {
		t.Error("ciupgrade without upgrade")
	}
}

func TestCreateBuildRefusesBadInput(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	base, _ := d.FetchImage(ctx, buildImageSpec("web"))
	for name, s := range map[string]driver.BuildSpec{
		"image name": buildSpec("Web;rm", 1, base),
		"version":    buildSpec("web", 0, base),
		"bridge":     func() driver.BuildSpec { s := buildSpec("web", 1, base); s.Bridge = "vmbr9"; return s }(),
		"no template": func() driver.BuildSpec {
			s := buildSpec("web", 1, base)
			s.Base = "9050"
			return s
		}(),
	} {
		if _, err := d.CreateBuild(ctx, s, driver.Bootstrap{Token: "t"}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := d.CreateBuild(ctx, buildSpec("web", 1, base), driver.Bootstrap{}); err == nil {
		t.Error("no token: no error")
	}
	if n := f.count("POST /nodes/pve/qemu/" + string(base) + "/clone"); n != 0 {
		t.Errorf("%d clones", n)
	}
}

func TestMakeImage(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	_, gid := built(t, f, d, "web", 2)
	before := f.cfgOf(gid)
	uuidBefore := parseProps(before["smbios1"])["uuid"]

	ref, err := d.MakeImage(ctx, gid, driver.BuiltImage{Image: "web", Version: 2, Digest: tDigest})
	if err != nil {
		t.Fatal(err)
	}
	if string(ref) != string(gid) {
		t.Fatalf("ref %s, guest %s", ref, gid)
	}
	key := "POST /nodes/pve/qemu/" + string(gid) + "/config"
	if n := f.countExact(key); n != 2 { // configure of CreateBuild, then the one write of MakeImage
		t.Fatalf("%d config writes", n)
	}
	p := f.lastParams(key)
	if p.Get("digest") == "" {
		t.Error("the write has no digest")
	}
	cfg := f.cfgOf(gid)
	for _, k := range []string{"sshkeys", "ciuser", "ipconfig0", "cicustom", "nameserver", "searchdomain", "onboot"} {
		if _, ok := cfg[k]; ok {
			t.Errorf("%s is still set: %q", k, cfg[k])
		}
	}
	if cfg["ciupgrade"] != "0" || cfg["name"] != "plumb-img-web-v2" || cfg["template"] != "1" {
		t.Errorf("config %v", cfg)
	}
	if cfg["smbios1"] != "uuid="+uuidBefore {
		t.Errorf("smbios1 %q: the uuid only", cfg["smbios1"])
	}
	if !isVersionOf(cfg["description"], "web", 2, tDigest) || strings.Contains(cfg["description"], "plumb-build") {
		t.Errorf("description %q", cfg["description"])
	}
	if !strings.Contains(cfg["scsi0"], ":base-"+string(gid)+"-disk-0") {
		t.Errorf("scsi0 %q", cfg["scsi0"])
	}
	// A second call of the same version is a success (review N1); the
	// template is not written again.
	if again, err := d.MakeImage(ctx, gid, driver.BuiltImage{Image: "web", Version: 2, Digest: tDigest}); err != nil || again != ref {
		t.Errorf("again: %s %v", again, err)
	}
	if n := f.countExact(key); n != 2 {
		t.Errorf("%d config writes after the second call", n)
	}
}

func TestMakeImageGuard(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	base, err := d.FetchImage(ctx, buildImageSpec("web"))
	if err != nil {
		t.Fatal(err)
	}
	gid, err := d.CreateBuild(ctx, buildSpec("web", 1, base), driver.Bootstrap{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	img := driver.BuiltImage{Image: "web", Version: 1, Digest: tDigest}
	if _, err := d.MakeImage(ctx, gid, img); err == nil || !strings.Contains(err.Error(), "not stopped") {
		t.Fatalf("running: %v", err)
	}
	f.stop(t, gid)
	for name, bad := range map[string]driver.BuiltImage{
		"other version": {Image: "web", Version: 2, Digest: tDigest},
		"other image":   {Image: "web2", Version: 1, Digest: tDigest},
		"short digest":  {Image: "web", Version: 1, Digest: tDigest[:12]},
	} {
		if _, err := d.MakeImage(ctx, gid, bad); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// A Target guest (no build marker) is never converted.
	f.addVM(9050, fakePool, map[string]string{"name": "plumb-build-web-v1", "description": "plumb-image: web"}, false)
	if _, err := d.MakeImage(ctx, "9050", img); err == nil || !strings.Contains(err.Error(), "no build marker") {
		t.Errorf("no marker: %v", err)
	}
	// Outside the pool.
	f.addVM(9051, "other", map[string]string{"name": "plumb-build-web-v1", "description": "plumb-build: web v1"}, false)
	if _, err := d.MakeImage(ctx, "9051", img); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("foreign: %v", err)
	}
	if f.count("POST /nodes/pve/qemu/9050/template")+f.count("POST /nodes/pve/qemu/9051/template")+f.count("POST /nodes/pve/qemu/"+string(gid)+"/template") != 0 {
		t.Error("a guest that failed the guard was converted")
	}
}

// A config write between the guard and the write: Proxmox refuses the
// stale digest, and nothing is converted.
func TestMakeImageStaleDigest(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	_, gid := built(t, f, d, "web", 1)
	f.mu.Lock()
	f.writeOnStatus = 1
	f.mu.Unlock()
	_, err := d.MakeImage(context.Background(), gid, driver.BuiltImage{Image: "web", Version: 1, Digest: tDigest})
	if err == nil || !strings.Contains(err.Error(), "changed after the check") {
		t.Fatalf("stale digest: %v", err)
	}
	cfg := f.cfgOf(gid)
	if cfg["template"] == "1" || cfg["name"] != "plumb-build-web-v1" || cfg["ciuser"] != "plumb-build" {
		t.Errorf("the guest changed: %v", cfg)
	}
	if f.count("POST /nodes/pve/qemu/"+string(gid)+"/template") != 0 {
		t.Error("converted after a refused write")
	}
}

// A failed conversion gives the guest its build name and marker back, so
// the engine's cleanup of build guests finds it (D-203).
func TestMakeImageConversionFails(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	_, gid := built(t, f, d, "web", 1)
	f.mu.Lock()
	f.failTemplate = true
	f.mu.Unlock()
	if _, err := d.MakeImage(context.Background(), gid, driver.BuiltImage{Image: "web", Version: 1, Digest: tDigest}); err == nil {
		t.Fatal("no error")
	}
	cfg := f.cfgOf(gid)
	if cfg["name"] != "plumb-build-web-v1" || !hasLine(cfg["description"], "plumb-build: web v1") || cfg["template"] == "1" {
		t.Errorf("not restored: %v", cfg)
	}
}

func TestFindImage(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	_, gid := built(t, f, d, "web", 1)
	if _, err := d.MakeImage(ctx, gid, driver.BuiltImage{Image: "web", Version: 1, Digest: tDigest}); err != nil {
		t.Fatal(err)
	}
	ref, err := d.FindImage(ctx, "web", 1, tDigest)
	if err != nil || string(ref) != string(gid) {
		t.Fatalf("find: %s %v", ref, err)
	}
	// Right after the rename the pool list can still have no name.
	f.mu.Lock()
	f.staleNames = true
	f.mu.Unlock()
	if ref, err := d.FindImage(ctx, "web", 1, tDigest); err != nil || string(ref) != string(gid) {
		t.Fatalf("stale names: %s %v", ref, err)
	}
	f.mu.Lock()
	f.staleNames = false
	f.mu.Unlock()
	for name, c := range map[string]struct {
		image   string
		version int
		digest  string
	}{
		"other digest":  {"web", 1, tDigest2},
		"other version": {"web", 2, tDigest},
		"other image":   {"web2", 1, tDigest},
	} {
		if _, err := d.FindImage(ctx, c.image, c.version, c.digest); !errors.Is(err, driver.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The same name and digest outside the pool or the range is not found.
	f.addVM(9098, "other", map[string]string{"name": "plumb-img-web-v5", "template": "1", "description": versionDescription(driver.BuiltImage{Image: "web", Version: 5, Digest: tDigest})}, false)
	f.addVM(8000, fakePool, map[string]string{"name": "plumb-img-web-v5", "template": "1", "description": versionDescription(driver.BuiltImage{Image: "web", Version: 5, Digest: tDigest})}, false)
	if _, err := d.FindImage(ctx, "web", 5, tDigest); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("outside: %v", err)
	}
}

func TestListImages(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	base, gid := built(t, f, d, "a.b", 1)
	if _, err := d.MakeImage(ctx, gid, driver.BuiltImage{Image: "a.b", Version: 1, Digest: tDigest}); err != nil {
		t.Fatal(err)
	}
	linkedClone(t, d, base, 9090)
	// Look-alikes: "." of the name must not match any character, and a
	// name match without the description proof is not listed.
	f.addVM(9080, fakePool, map[string]string{"name": "plumb-img-aXb-v7", "template": "1", "description": versionDescription(driver.BuiltImage{Image: "a.b", Version: 7, Digest: tDigest})}, false)
	f.addVM(9081, fakePool, map[string]string{"name": "plumb-img-a.b-v8", "template": "1", "description": "plumb-image: a.b\nversion=8"}, false)
	f.addVM(9082, fakePool, map[string]string{"name": "plumb-img-a.b-v9", "template": "1", "description": versionDescription(driver.BuiltImage{Image: "other", Version: 9, Digest: tDigest})}, false)
	f.mu.Lock()
	f.files["local:import/plumb-a.b-0123456789ab.qcow2"] = true
	f.files["local:import/plumb-aXb-0123456789ab.qcow2"] = true
	f.files["local:import/notes.txt"] = true
	f.mu.Unlock()

	objs, err := d.ListImages(ctx, "a.b")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range objs {
		got = append(got, string(o.Kind)+" "+o.Name+" "+o.Digest[:min(len(o.Digest), 6)]+" "+strconv.Itoa(o.LinkedClones))
		if o.Image != "a.b" {
			t.Errorf("%s: image %q", o.Name, o.Image)
		}
	}
	want := []string{
		"base plumb-img-a.b-abcdef012345 abcdef 1",
		"version plumb-img-a.b-v1 d1d2d3 0",
		"import local:import/plumb-a.b-0123456789ab.qcow2  0",
		"import local:import/plumb-a.b-abcdef012345.qcow2 abcdef 0",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if objs[1].Version != 1 || objs[1].SizeBytes != 10<<30 || objs[2].SizeBytes == 0 {
		t.Errorf("version %d size %d import size %d", objs[1].Version, objs[1].SizeBytes, objs[2].SizeBytes)
	}
}

func TestRemoveImage(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	base, gid := built(t, f, d, "web", 1)
	if _, err := d.MakeImage(ctx, gid, driver.BuiltImage{Image: "web", Version: 1, Digest: tDigest}); err != nil {
		t.Fatal(err)
	}
	// A full clone (a Target) does not block the delete (host B2c).
	if _, err := d.Create(ctx, driver.GuestSpec{Name: "t1", Kind: wire.KindVM, ImageName: "web", Image: driver.ImageRef(gid), ImageVersion: 1,
		Cores: 1, MemoryMB: 512, DiskGB: 10, Bridge: "vmbr0", IP: "dhcp", User: "ops"}, driver.Bootstrap{Token: "t"}); err != nil {
		t.Fatal(err)
	}
	objs, err := d.ListImages(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	var version, baseObj, file driver.ImageObject
	for _, o := range objs {
		switch o.Kind {
		case driver.ImageVersionTemplate:
			version = o
		case driver.ImageBaseTemplate:
			baseObj = o
		case driver.ImageImportFile:
			file = o
		}
	}
	if err := d.RemoveImage(ctx, file); err == nil || !strings.Contains(err.Error(), "never deletes") {
		t.Errorf("import file: %v", err)
	}
	if err := d.RemoveImage(ctx, version); err != nil {
		t.Fatal(err)
	}
	p := f.lastParams("DELETE /nodes/pve/qemu/" + string(gid))
	if p.Get("purge") != "1" || p.Get("destroy-unreferenced-disks") != "1" {
		t.Errorf("delete params %v", p)
	}
	if f.cfgOf(gid) != nil {
		t.Error("the template is still there")
	}
	// Gone: the pool list says so; no other request for the VMID (Proxmox
	// answers 403 for it).
	reads := f.count("GET /nodes/pve/qemu/" + string(gid))
	if err := d.RemoveImage(ctx, version); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("gone: %v", err)
	}
	if n := f.count("GET /nodes/pve/qemu/" + string(gid)); n != reads {
		t.Errorf("%d reads of a gone VMID", n-reads)
	}
	if err := d.RemoveImage(ctx, baseObj); err != nil {
		t.Fatalf("base template: %v", err)
	}
	if f.cfgOf(base) != nil {
		t.Error("base template is still there")
	}
}

// The guard of D-209, one rule per case. Nothing is deleted.
func TestRemoveImageGuard(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	desc := versionDescription(driver.BuiltImage{Image: "a.b", Version: 1, Digest: tDigest})
	obj := func(ref string) driver.ImageObject {
		return driver.ImageObject{Kind: driver.ImageVersionTemplate, Image: "a.b", Ref: driver.ImageRef(ref), Name: "plumb-img-a.b-v1", Version: 1, Digest: tDigest}
	}
	f.addVM(9010, fakePool, map[string]string{"name": "plumb-img-aXb-v1", "template": "1", "description": desc}, false)
	f.addVM(9011, fakePool, map[string]string{"name": "plumb-img-a.b-v1", "description": desc}, false) // no template
	f.addVM(9012, fakePool, map[string]string{"name": "plumb-img-a.b-v1", "template": "1", "lock": "clone", "description": desc}, false)
	f.addVM(9013, fakePool, map[string]string{"name": "plumb-img-a.b-v1", "template": "1", "description": "plumb-image: a.b\nversion=1"}, false)
	f.addVM(9014, fakePool, map[string]string{"name": "plumb-img-a.b-v1", "template": "1", "description": versionDescription(driver.BuiltImage{Image: "a.b", Version: 1, Digest: tDigest2})}, false)
	f.addVM(9015, fakePool, map[string]string{"name": "plumb-img-a.b-v1", "template": "1", "description": versionDescription(driver.BuiltImage{Image: "aXb", Version: 1, Digest: tDigest})}, false)
	f.addVM(9016, "other", map[string]string{"name": "plumb-img-a.b-v1", "template": "1", "description": desc}, false)
	f.addVM(8000, fakePool, map[string]string{"name": "plumb-img-a.b-v1", "template": "1", "description": desc}, false)
	for ref, want := range map[string]string{
		"9010": "not plumb-img-a.b-v1", // regex characters: "." is no wildcard
		"9011": "no template",
		"9012": "locked",
		"9013": "does not name",
		"9014": "does not name",
		"9015": "does not name",
		"9016": "not found",
		"8000": "outside",
	} {
		err := d.RemoveImage(ctx, obj(ref))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", ref, err, want)
		}
	}
	// The object itself must be consistent: name, kind and digest.
	bad := []driver.ImageObject{
		{Kind: driver.ImageVersionTemplate, Image: "a.b", Ref: "9010", Name: "plumb-img-aXb-v1", Version: 1, Digest: tDigest},
		{Kind: driver.ImageVersionTemplate, Image: "a.b", Ref: "9010", Name: "plumb-img-a.b-v1", Version: 1, Digest: ""},
		{Kind: driver.ImageVersionTemplate, Image: "a.*", Ref: "9010", Name: "plumb-img-a.*-v1", Version: 1, Digest: tDigest},
		{Kind: driver.ImageBaseTemplate, Image: "a.b", Ref: "9010", Name: "plumb-img-a.b-v1", Digest: tDigest},
		{Kind: "other", Image: "a.b", Ref: "9010", Name: "plumb-img-a.b-v1", Digest: tDigest},
	}
	for i, o := range bad {
		if err := d.RemoveImage(ctx, o); err == nil {
			t.Errorf("bad object %d: no error", i)
		}
	}
	if n := f.count("DELETE /nodes/pve/qemu/"); n != 0 {
		t.Errorf("%d deletes", n)
	}
}

// Review S3: a linked clone in the pool blocks the delete, seen in its
// disk line or in the parent of its volume.
func TestRemoveImageLinkedClone(t *testing.T) {
	for _, c := range []struct {
		name             string
		inConfig, parent bool
		blocked          bool
	}{
		{"disk line", true, false, true},
		{"volume parent", false, true, true},
		{"both", true, true, true},
		{"neither (documented limit)", false, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakePVE(t)
			f.linkInConfig, f.parentInContent = c.inConfig, c.parent
			d := f.driver(t)
			ctx := context.Background()
			_, gid := built(t, f, d, "web", 1)
			ref, err := d.MakeImage(ctx, gid, driver.BuiltImage{Image: "web", Version: 1, Digest: tDigest})
			if err != nil {
				t.Fatal(err)
			}
			linkedClone(t, d, ref, 9090)
			o := driver.ImageObject{Kind: driver.ImageVersionTemplate, Image: "web", Ref: ref, Name: "plumb-img-web-v1", Version: 1, Digest: tDigest}
			err = d.RemoveImage(ctx, o)
			if c.blocked {
				if err == nil || !strings.Contains(err.Error(), "linked clones") || !strings.Contains(err.Error(), "9090") {
					t.Fatalf("not blocked: %v", err)
				}
				if f.cfgOf(ref) == nil {
					t.Fatal("deleted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A volume list that the token cannot read: the check cannot run, so the
// delete is refused.
func TestRemoveImageNeedsVolumeList(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	_, gid := built(t, f, d, "web", 1)
	ref, err := d.MakeImage(ctx, gid, driver.BuiltImage{Image: "web", Version: 1, Digest: tDigest})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.contentStatus = 403
	f.mu.Unlock()
	err = d.RemoveImage(ctx, driver.ImageObject{Kind: driver.ImageVersionTemplate, Image: "web", Ref: ref, Name: "plumb-img-web-v1", Version: 1, Digest: tDigest})
	if err == nil || !strings.Contains(err.Error(), "linked-clone check") {
		t.Fatalf("%v", err)
	}
	if f.cfgOf(ref) == nil {
		t.Fatal("deleted")
	}
}

func TestDeleteNeverRemovesAVersionTemplate(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	_, gid := built(t, f, d, "web", 1)
	if _, err := d.MakeImage(ctx, gid, driver.BuiltImage{Image: "web", Version: 1, Digest: tDigest}); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(ctx, gid); err == nil || !strings.Contains(err.Error(), "template") {
		t.Fatalf("%v", err)
	}
}

func TestImageVersionMarker(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	_, gid := built(t, f, d, "web", 4)
	ref, err := d.MakeImage(ctx, gid, driver.BuiltImage{Image: "web", Version: 4, Digest: tDigest})
	if err != nil {
		t.Fatal(err)
	}
	spec := driver.GuestSpec{Name: "t1", Kind: wire.KindVM, ImageName: "web", Image: ref, ImageVersion: 4,
		Cores: 1, MemoryMB: 512, DiskGB: 10, Bridge: "vmbr0", IP: "dhcp", User: "ops"}
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	clone := f.lastParams("POST /nodes/pve/qemu/" + string(ref) + "/clone")
	if clone.Get("full") != "1" {
		t.Error("every clone is full (D-208)")
	}
	cfg := f.cfgOf(id)
	if !hasLine(cfg["description"], "plumb-image-version: 4") || !hasLine(cfg["description"], "plumb-image: web") {
		t.Errorf("description %q", cfg["description"])
	}
	for _, k := range []string{"build=" + tDigest, "version=4"} {
		if hasLine(cfg["description"], k) {
			t.Errorf("the guest keeps the template line %q", k)
		}
	}
	st, err := d.Observe(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Spec.ImageVersion != 4 || st.Pending != nil {
		t.Fatalf("observe: version %d pending %v", st.Spec.ImageVersion, st.Pending)
	}
	// Information only: another version is no change.
	want := spec
	want.ImageVersion = 7
	if ch, err := d.Diff(want, st); err != nil || len(ch) != 0 {
		t.Errorf("diff %v %v", ch, err)
	}
}

func TestDiskSpace(t *testing.T) {
	ctx := context.Background()
	f := newFakePVE(t)
	d := f.driver(t)
	ds, err := d.DiskSpace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Storage != "local-lvm" || ds.TotalBytes != 42<<30 || ds.UsedBytes != 1<<30 || ds.MetaKnown {
		t.Fatalf("%+v", ds)
	}
	// The token can read the thin pools: the pool of the same size.
	f.mu.Lock()
	f.thinPools = []map[string]any{
		{"lv": "other", "vg": "x", "lv_size": int64(10 << 30), "used": 0, "metadata_size": 1 << 20, "metadata_used": 1 << 10},
		{"lv": "data", "vg": "pve", "lv_size": int64(42 << 30), "used": 1 << 30, "metadata_size": 432 << 20, "metadata_used": 20 << 20},
	}
	f.mu.Unlock()
	ds, err = d.DiskSpace(ctx)
	if err != nil || !ds.MetaKnown || ds.MetaTotalBytes != 432<<20 || ds.MetaUsedBytes != 20<<20 {
		t.Fatalf("%+v %v", ds, err)
	}
	// Two pools of that size: unknown, not a guess.
	f.mu.Lock()
	f.thinPools = append(f.thinPools, map[string]any{"lv": "data2", "lv_size": int64(42 << 30), "metadata_size": 1 << 20, "metadata_used": 0})
	f.mu.Unlock()
	if ds, err = d.DiskSpace(ctx); err != nil || ds.MetaKnown || ds.MetaUsedBytes != 0 {
		t.Fatalf("%+v %v", ds, err)
	}
	// Not lvmthin: no thin-pool read.
	f.mu.Lock()
	f.diskType = "dir"
	f.mu.Unlock()
	before := f.count("GET /nodes/pve/disks/lvmthin")
	if ds, err = d.DiskSpace(ctx); err != nil || ds.MetaKnown || f.count("GET /nodes/pve/disks/lvmthin") != before {
		t.Fatalf("%+v %v", ds, err)
	}
	// An inactive storage has no size: an error, never 0 % used.
	f.mu.Lock()
	f.diskTotal = 0
	f.mu.Unlock()
	if _, err = d.DiskSpace(ctx); err == nil {
		t.Fatal("no size: no error")
	}
}

// Review N1 of PR #125: a conversion that fails on our side but ends on
// the host. MakeImage waits for the lock and reports success; a template
// that got the build name back is finished with the version name.
func TestMakeImageLateConversion(t *testing.T) {
	for _, mode := range []string{"lock", "afterWrite"} {
		t.Run(mode, func(t *testing.T) {
			f := newFakePVE(t)
			d := f.driver(t)
			_, gid := built(t, f, d, "web", 1)
			f.mu.Lock()
			f.lateConvert = mode
			f.mu.Unlock()
			img := driver.BuiltImage{Image: "web", Version: 1, Digest: tDigest}
			ref, err := d.MakeImage(context.Background(), gid, img)
			if err != nil || string(ref) != string(gid) {
				t.Fatalf("%s %v", ref, err)
			}
			cfg := f.cfgOf(gid)
			if cfg["template"] != "1" || cfg["name"] != "plumb-img-web-v1" || !isVersionOf(cfg["description"], "web", 1, tDigest) {
				t.Fatalf("config %v", cfg)
			}
			if got, err := d.FindImage(context.Background(), "web", 1, tDigest); err != nil || got != ref {
				t.Fatalf("find %s %v", got, err)
			}
		})
	}
}

// MakeImage again: a done version is a success; a template with the build
// name and marker (a rest of the race) is finished; anything else is refused.
func TestMakeImageAgain(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	_, gid := built(t, f, d, "web", 1)
	img := driver.BuiltImage{Image: "web", Version: 1, Digest: tDigest}
	ref, err := d.MakeImage(ctx, gid, img)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := d.MakeImage(ctx, gid, img); err != nil || again != ref {
		t.Fatalf("again: %s %v", again, err)
	}
	if _, err := d.MakeImage(ctx, gid, driver.BuiltImage{Image: "web", Version: 1, Digest: tDigest2}); err == nil {
		t.Error("another digest on a done version")
	}
	f.addVM(9060, fakePool, map[string]string{"name": "plumb-build-web-v2", "template": "1", "description": "plumb-build: web v2\nPlumb image build."}, false)
	f.mu.Lock()
	f.vms[9060].template = true
	f.mu.Unlock()
	img2 := driver.BuiltImage{Image: "web", Version: 2, Digest: tDigest}
	if ref, err := d.MakeImage(ctx, "9060", img2); err != nil || ref != "9060" {
		t.Fatalf("finish: %s %v", ref, err)
	}
	if cfg := f.cfgOf(9060); cfg["name"] != "plumb-img-web-v2" || !isVersionOf(cfg["description"], "web", 2, tDigest) {
		t.Fatalf("not finished: %v", cfg)
	}
	f.addVM(9061, fakePool, map[string]string{"name": "plumb-build-web-v3", "template": "1", "description": "plumb-image: web"}, false)
	if _, err := d.MakeImage(ctx, "9061", driver.BuiltImage{Image: "web", Version: 3, Digest: tDigest}); err == nil {
		t.Error("a template without the build marker was renamed")
	}
}
