package plan

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// templateFixture is an Engine with a fake driver that holds three
// template files of the lxc Image "ct": an old one, one that a container
// uses, and the current one (T-085).
type templateFixture struct {
	ctx                  context.Context
	drv                  *fake.Driver
	e                    *Engine
	old, used, current   driver.ImageSpec
	oldRef, usedRef, cur driver.ImageRef
}

func newTemplateFixture(t *testing.T) *templateFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	drv := fake.New()
	spec := func(pin string) driver.ImageSpec {
		return driver.ImageSpec{Name: "ct", Kind: wire.KindLXC, URL: "http://download.proxmox.com/a.tar.zst", SHA512: strings.Repeat(pin, 128)}
	}
	f := &templateFixture{ctx: ctx, drv: drv, old: spec("a"), used: spec("b"), current: spec("c"),
		e: &Engine{st: st, cfg: Config{Driver: drv}, busy: map[string]int{}, pinned: map[imageKey]int{}, removing: map[imageKey]bool{}}}
	for _, p := range []struct {
		spec driver.ImageSpec
		ref  *driver.ImageRef
	}{{f.old, &f.oldRef}, {f.used, &f.usedRef}, {f.current, &f.cur}} {
		ref, err := drv.FetchImage(ctx, p.spec)
		if err != nil {
			t.Fatal(err)
		}
		*p.ref = ref
	}
	if _, err := drv.Create(ctx, driver.GuestSpec{Name: "c1", Kind: wire.KindLXC, ImageName: "ct", Image: f.usedRef,
		Cores: 1, MemoryMB: 512, DiskGB: 4, Bridge: "vmbr0", IP: "dhcp"}, driver.Bootstrap{Token: "t"}); err != nil {
		t.Fatal(err)
	}
	return f
}

// An unused template file is listed with the strict pvesm line; the file
// of the current document and a file in use by a container are not
// (D-233, T-085).
func TestUnusedTemplateFiles(t *testing.T) {
	f := newTemplateFixture(t)
	got, err := f.e.UnusedTemplateFiles(f.ctx, "ct", f.current.URL, f.current.SHA512)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Volume != string(f.oldRef) || got[0].Command != "pvesm free "+string(f.oldRef) || got[0].SizeBytes <= 0 {
		t.Fatalf("unused = %+v, want only %s", got, f.oldRef)
	}
	// Another current document: the file of the old one becomes unused,
	// the file of the container stays in use.
	got, err = f.e.UnusedTemplateFiles(f.ctx, "ct", f.old.URL, f.old.SHA512)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Volume != string(f.cur) {
		t.Fatalf("unused after a pin change = %+v, want only %s", got, f.cur)
	}
	// A driver without template files gives none.
	f.drv.Fail(fake.OpListImages, driver.ErrNotImplemented)
	got, err = f.e.UnusedTemplateFiles(f.ctx, "ct", f.current.URL, f.current.SHA512)
	if err != nil || got != nil {
		t.Fatalf("no template files: %+v %v", got, err)
	}
}

// Without the current file Plumb cannot tell which file is in use: it
// lists nothing and says why.
func TestUnusedTemplateFilesNeedCurrent(t *testing.T) {
	f := newTemplateFixture(t)
	bad := f.current
	bad.SHA512 = "short"
	if got, err := f.e.UnusedTemplateFiles(f.ctx, "ct", bad.URL, bad.SHA512); err == nil || got != nil {
		t.Fatalf("no current file: %+v %v", got, err)
	}
	boom := errors.New("storage down")
	f.drv.Fail(fake.OpListImages, boom)
	if got, err := f.e.UnusedTemplateFiles(f.ctx, "ct", f.current.URL, f.current.SHA512); !errors.Is(err, boom) || got != nil {
		t.Fatalf("list error: %+v %v", got, err)
	}
}

func TestTemplateFreeCommand(t *testing.T) {
	good := "plumb-images:vztmpl/plumb-ct-debian-13-0123456789ab.tar.gz"
	if got := TemplateFreeCommand(good); got != "pvesm free "+good {
		t.Fatalf("good: %q", got)
	}
	for _, v := range []string{
		"plumb-images:import/plumb-ct-0123456789ab.qcow2",
		"plumb-images:vztmpl/debian-13-standard_13.1-2_amd64.tar.zst",
		"plumb-images:vztmpl/plumb-ct-x;rm -rf /-0123456789ab.tar.gz",
		"plumb-images:vztmpl/plumb-ct-$(id)-0123456789ab.tar.gz",
		"plumb-images:vztmpl/plumb-ct-a b-0123456789ab.tar.gz",
		"plumb-images:vztmpl/plumb-ct-A-0123456789ab.tar.gz",
		"plumb-images:vztmpl/plumb-ct-a.b-0123456789ab.tar.gz",
		"plumb-images:vztmpl/plumb-ct-a-0123456789ab.tar.gz; reboot",
		"plumb-images:vztmpl/plumb-ct-a-0123456789AB.tar.gz",
		"plumb-images:vztmpl/plumb-ct--0123456789ab.tar.gz",
		"plumb-images:vztmpl/../plumb-ct-a-0123456789ab.tar.gz",
		"9bad:vztmpl/plumb-ct-a-0123456789ab.tar.gz",
		"plumb images:vztmpl/plumb-ct-a-0123456789ab.tar.gz",
	} {
		if got := TemplateFreeCommand(v); got != "" {
			t.Errorf("%q: command %q, want none", v, got)
		}
	}
}

// Retention and Image delete never call RemoveImage for a template file
// (D-233), also with keep 0 (the Image is deleted) and when the file is
// of another pin.
func TestRetentionKeepsTemplateFiles(t *testing.T) {
	f := newTemplateFixture(t)
	for _, keep := range []int{0, 1} {
		removed, left, err := f.e.retain(f.ctx, "ct", keep, "", 0)
		if err != nil || len(removed) != 0 || len(left) != 0 {
			t.Fatalf("keep %d: removed %v left %v err %v", keep, removed, left, err)
		}
	}
	if n := f.drv.Calls(fake.OpRemoveImage); n != 0 {
		t.Fatalf("RemoveImage calls = %d, want 0", n)
	}
	for _, built := range []bool{false, true} {
		rm, notes := f.e.planRemovals(f.ctx, "ct", nil, state.ImageInfo{Name: "ct", Type: wire.KindLXC, SHA512: f.current.SHA512, Built: built, Keep: 1}, built, false)
		if len(rm) != 0 || len(notes) != 0 {
			t.Fatalf("planned removals (built %v): %+v %v", built, rm, notes)
		}
	}
	objs, err := f.drv.ListImages(f.ctx, "ct")
	if err != nil || len(objs) != 3 {
		t.Fatalf("template files after retention: %+v %v", objs, err)
	}
	// The driver refuses one too.
	if err := f.drv.RemoveImage(f.ctx, objs[0]); err == nil || !strings.Contains(err.Error(), "D-233") {
		t.Fatalf("RemoveImage of a template file: %v", err)
	}
}

// Driver metadata is untrusted, including aliases in Name and Ref.
type templateListDriver struct {
	driver.Driver
	objects []driver.ImageObject
}

func (d templateListDriver) ListImages(context.Context, string) ([]driver.ImageObject, error) {
	return d.objects, nil
}

func TestUnusedTemplateFilesUntrustedMetadata(t *testing.T) {
	f := newTemplateFixture(t)
	hostile := "local:vztmpl/plumb-ct-x-0123456789ab.tar.gz\nreboot"
	f.e.cfg.Driver = templateListDriver{Driver: f.drv, objects: []driver.ImageObject{
		{Kind: driver.ImageTemplateFile, Name: string(f.cur)},
		{Kind: driver.ImageTemplateFile, Name: "alias", Ref: f.cur},
		{Kind: driver.ImageTemplateFile, Name: string(f.usedRef), Containers: 2},
		{Kind: driver.ImageImportFile, Name: string(f.oldRef)},
		{Kind: driver.ImageTemplateFile, Name: hostile},
		{Kind: driver.ImageTemplateFile, Name: string(f.oldRef)},
	}}
	got, err := f.e.UnusedTemplateFiles(f.ctx, "ct", f.current.URL, f.current.SHA512)
	if err != nil || len(got) != 2 {
		t.Fatalf("unused = %+v, %v", got, err)
	}
	commands := map[string]string{}
	for _, file := range got {
		commands[file.Volume] = file.Command
	}
	if cmd, ok := commands[hostile]; !ok || cmd != "" {
		t.Fatalf("hostile volume command = %q, present = %v", cmd, ok)
	}
	if commands[string(f.oldRef)] != "pvesm free "+string(f.oldRef) {
		t.Fatalf("safe volume missing: %+v", got)
	}
}

// Review S1 of PR #171: two lxc Images whose names differ only by "." and
// "-" share one file prefix. The status of one never lists the current
// file of the other; a file of no head Image is still listed.
func TestUnusedTemplateFilesOtherImages(t *testing.T) {
	f := newTemplateFixture(t)
	const url = "http://download.proxmox.com/a.tar.zst"
	pin := func(c string) string { return strings.Repeat(c, 128) }
	doc := func(name, sha512 string) store.DocRow {
		body, err := state.Canonical(state.Doc{Kind: state.KindImage, Name: name,
			Spec: json.RawMessage(`{"type":"lxc","url":"` + url + `","sha512":"` + sha512 + `"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return store.DocRow{Kind: state.KindImage, Name: name, Hash: state.Hash(body), Body: body}
	}
	p, err := f.e.st.CreatePlan(f.ctx, store.Plan{Origin: "proposal", Risk: "reversible", Reason: "r", Author: "user:a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.e.st.ApprovePlan(f.ctx, p.ID, "user:a", store.NewRevision{Author: "user:a",
		Docs: []store.DocRow{doc("deb-13.6", pin("d")), doc("deb-13-6", pin("e"))}}); err != nil {
		t.Fatal(err)
	}
	var refs []driver.ImageRef
	for _, s := range []driver.ImageSpec{
		{Name: "deb-13.6", Kind: wire.KindLXC, URL: url, SHA512: pin("d")}, // current of deb-13.6
		{Name: "deb-13-6", Kind: wire.KindLXC, URL: url, SHA512: pin("e")}, // current of deb-13-6
		{Name: "deb-13-6", Kind: wire.KindLXC, URL: url, SHA512: pin("f")}, // of no head Image
	} {
		ref, err := f.drv.FetchImage(f.ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, ref)
	}
	for _, name := range []string{"deb-13-6", "deb-13.6"} {
		info := map[string]string{"deb-13.6": pin("d"), "deb-13-6": pin("e")}[name]
		got, err := f.e.UnusedTemplateFiles(f.ctx, name, url, info)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Volume != string(refs[2]) {
			t.Fatalf("%s: unused = %+v, want only %s", name, got, refs[2])
		}
	}
	// No current file of a head Image known: nothing is listed.
	bad := f.current
	bad.SHA512 = "short"
	if got, err := f.e.UnusedTemplateFiles(f.ctx, "deb-13-6", bad.URL, bad.SHA512); err == nil || got != nil {
		t.Fatalf("unknown current file: %+v %v", got, err)
	}
}
