package proxmox

// T-055 (D-188, D-189): FetchImage runs before every create. It finds the
// template again, makes it again when it is gone, waits for a lock, and
// refuses a template with the right name and another checksum.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
)

const dlKey = "POST /nodes/pve/storage/local/download-url"

// fetched makes the template of image and returns its VMID.
func fetched(t *testing.T, d *Driver) (driver.ImageRef, int) {
	t.Helper()
	ref, err := d.FetchImage(context.Background(), image)
	if err != nil {
		t.Fatal(err)
	}
	id, err := vmid(string(ref))
	if err != nil {
		t.Fatal(err)
	}
	return ref, id
}

// logSince returns the requests after the first n.
func (f *fakePVE) logSince(n int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log[n:]...)
}

func (f *fakePVE) logLen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.log)
}

// The template was destroyed on the host; the import file is there: a new
// template, no download.
func TestFetchImageTemplateGone(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	_, id := fetched(t, d)
	f.mu.Lock()
	delete(f.vms, id)
	f.mu.Unlock()
	again, err := d.FetchImage(context.Background(), image)
	if err != nil {
		t.Fatalf("fetch after destroy: %v", err)
	}
	// The VMID can be the old one again: it is free.
	if newID, _ := vmid(string(again)); f.vms[newID] == nil || !f.vms[newID].template {
		t.Errorf("no template behind %s", again)
	}
	if n := f.countExact(dlKey); n != 1 {
		t.Errorf("downloads: %d, want 1 (the import file is there)", n)
	}
	if n := f.countExact("POST /nodes/pve/qemu"); n != 2 {
		t.Errorf("template VMs created: %d, want 2", n)
	}
}

// Template and import file are gone: one more download with the checksum.
func TestFetchImageTemplateAndFileGone(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	_, id := fetched(t, d)
	f.mu.Lock()
	delete(f.vms, id)
	clear(f.files)
	f.mu.Unlock()
	if _, err := d.FetchImage(context.Background(), image); err != nil {
		t.Fatal(err)
	}
	if n := f.countExact(dlKey); n != 2 {
		t.Errorf("downloads: %d, want 2", n)
	}
	if dl := f.lastParams(dlKey); dl.Get("checksum") != sum || dl.Get("checksum-algorithm") != "sha256" {
		t.Errorf("download params: %v", dl)
	}
}

// The template left the pool: it is foreign now and gets no call; a new
// template is made in the pool.
func TestFetchImageTemplateLeftPool(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ref, id := fetched(t, d)
	f.mu.Lock()
	f.vms[id].pool = "other"
	f.mu.Unlock()
	n := f.logLen()
	again, err := d.FetchImage(context.Background(), image)
	if err != nil || again == ref {
		t.Fatalf("fetch: %s %v (old %s)", again, err, ref)
	}
	newID, _ := vmid(string(again))
	if f.vms[newID].pool != fakePool || !f.vms[newID].template {
		t.Errorf("new template: %+v", f.vms[newID])
	}
	for _, l := range f.logSince(n) {
		if strings.Contains(l, "/qemu/"+string(ref)) {
			t.Errorf("a call to the foreign VM: %s", l)
		}
	}
}

// The template has a lock for the first reads (a clone or conversion
// runs): FetchImage waits and returns the same template. No second
// template, no DELETE.
func TestFetchImageWaitsForLock(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ref, id := fetched(t, d)
	f.mu.Lock()
	f.lockReads[id] = 5
	f.mu.Unlock()
	again, err := d.FetchImage(context.Background(), image)
	if err != nil || again != ref {
		t.Fatalf("fetch during the lock: %s %v, want %s", again, err, ref)
	}
	if n := f.countExact("POST /nodes/pve/qemu"); n != 1 {
		t.Errorf("template VMs created: %d, want 1", n)
	}
	if n := f.count("DELETE /nodes/pve/qemu/"); n != 0 {
		t.Errorf("DELETEs: %d", n)
	}
	// A lock that stays: "busy" after the bound, nothing removed.
	f.mu.Lock()
	f.lockReads[id] = 1 << 30
	f.mu.Unlock()
	if _, err := d.FetchImage(context.Background(), image); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Errorf("lock that stays: %v", err)
	}
	if n := f.count("DELETE /nodes/pve/qemu/"); n != 0 {
		t.Errorf("DELETEs: %d", n)
	}
}

// Two Targets with one Image: FetchImage and Create in parallel give one
// template and two guests.
func TestFetchAndCreateInParallel(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	var wg sync.WaitGroup
	ids := make([]driver.GuestID, 2)
	errs := make([]error, 2)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ref, err := d.FetchImage(context.Background(), image)
			if err != nil {
				errs[i] = err
				return
			}
			g := guest(ref)
			g.Name = []string{"web1", "web2"}[i]
			g.IP = []string{"10.0.0.5/24", "10.0.0.6/24"}[i]
			ids[i], errs[i] = d.Create(context.Background(), g, driver.Bootstrap{Token: "tok"})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("target %d: %v", i, err)
		}
	}
	if ids[0] == ids[1] {
		t.Errorf("one guest id for two targets: %s", ids[0])
	}
	if n := f.countExact("POST /nodes/pve/qemu"); n != 1 {
		t.Errorf("template VMs created: %d, want 1", n)
	}
	if n := f.count("DELETE /nodes/pve/qemu/"); n != 0 {
		t.Errorf("DELETEs: %d", n)
	}
}

// A template with the right name and another checksum in its description:
// an error that names the VMID; no DELETE, no clone, no new template.
func TestFetchImageWrongChecksum(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	f.addVM(9007, fakePool, map[string]string{"name": templateName(image), "template": "1",
		"description": "Plumb image " + image.Name + "\nsha256=" + strings.Repeat("0", 64)}, false)
	f.vms[9007].template = true
	_, err := d.FetchImage(context.Background(), image)
	if err == nil || !strings.Contains(err.Error(), "9007") || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v", err)
	}
	if n := f.count("DELETE /nodes/pve/qemu/"); n != 0 {
		t.Errorf("DELETEs: %d", n)
	}
	if n := f.count("POST /nodes/pve/qemu"); n != 0 {
		t.Errorf("creates or clones: %d", n)
	}
	// Without a description it is not ours either.
	f.vms[9007].cfg["description"] = ""
	if _, err := d.FetchImage(context.Background(), image); err == nil {
		t.Error("a template without a checksum was used")
	}
}

// The guest keeps the Image name (D-189): Create writes it, Observe reads
// it, the ref is not reported. A guest without the marker has no name.
func TestImageNameRoundTrip(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := fetched(t, d)
	spec := guest(ref)
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := vmid(string(id))
	if desc := f.vms[gid].cfg["description"]; !strings.HasPrefix(desc, "plumb-image: "+spec.ImageName+"\n") {
		t.Errorf("description = %q", desc)
	}
	st, err := d.Observe(ctx, id)
	if err != nil || st.Spec.ImageName != spec.ImageName || st.Spec.Image != "" {
		t.Errorf("observed: %+v %v", st.Spec, err)
	}
	f.addVM(9050, fakePool, map[string]string{"name": "old", "description": "Managed by Plumb."}, false)
	if st, err := d.Observe(ctx, "9050"); err != nil || st.Spec.ImageName != "" {
		t.Errorf("no marker: %+v %v", st.Spec, err)
	}
	// The name goes into one description line: no empty name, no break.
	for _, n := range []string{"", "a\nb", "a\r", " a"} {
		bad := guest(ref)
		bad.Name, bad.ImageName = "web9", n
		if _, err := d.Create(ctx, bad, driver.Bootstrap{Token: "tok"}); err == nil || !strings.Contains(err.Error(), "image name") {
			t.Errorf("image name %q: %v", n, err)
		}
	}
}
