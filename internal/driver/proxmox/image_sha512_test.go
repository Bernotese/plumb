package proxmox

import (
	"context"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// D-345: a VM Image from a Debian list pins a SHA-512. Proxmox gets the
// algorithm, the template description holds the sha512 line, the listing
// reads it back, and a second fetch reuses the template.
func TestFetchImageSHA512(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	sum512 := strings.Repeat("ab", 64)
	spec := driver.ImageSpec{Name: "debian-13", Kind: wire.KindVM,
		URL: "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2", SHA512: sum512}
	ref, err := d.FetchImage(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if dl := f.lastParams(dlKey); dl.Get("checksum") != sum512 || dl.Get("checksum-algorithm") != "sha512" ||
		dl.Get("filename") != "plumb-debian-13-"+sum512[:12]+".qcow2" {
		t.Errorf("download params: %v", dl)
	}
	id, _ := vmid(string(ref))
	if desc := f.vms[id].cfg["description"]; !strings.Contains(desc, "sha512="+sum512) || strings.Contains(desc, "sha256=") {
		t.Errorf("description %q", desc)
	}
	again, err := d.FetchImage(context.Background(), spec)
	if err != nil || again != ref {
		t.Fatalf("second fetch: %s %v, want %s", again, err, ref)
	}
	objs, err := d.ListImages(context.Background(), "debian-13")
	if err != nil {
		t.Fatal(err)
	}
	var base, file bool
	for _, o := range objs {
		base = base || (o.Kind == driver.ImageBaseTemplate && o.Digest == sum512)
		file = file || (o.Kind == driver.ImageImportFile && o.Digest == sum512)
	}
	if !base || !file {
		t.Errorf("listing: %+v", objs)
	}
	// Both pins at once, or a short SHA-512, are refused.
	for _, bad := range []driver.ImageSpec{
		{Name: "x", Kind: wire.KindVM, URL: spec.URL, SHA256: sum, SHA512: sum512},
		{Name: "x", Kind: wire.KindVM, URL: spec.URL, SHA512: sum512[:120]},
	} {
		if _, err := d.FetchImage(context.Background(), bad); err == nil {
			t.Errorf("%+v: no error", bad)
		}
	}
}
