//go:build pve

// Real-host tests (make test-pve). They need a Proxmox VE host prepared
// by the installer (T-013) and these variables:
//
//	PLUMB_PVE_URL, PLUMB_PVE_TOKEN_ID, PLUMB_PVE_SECRET, PLUMB_PVE_CA,
//	PLUMB_PVE_NODE, PLUMB_PVE_POOL (plumb-test), PLUMB_PVE_DISK,
//	PLUMB_PVE_IMAGES, PLUMB_PVE_SNIPPETS, PLUMB_PVE_SNIPPET,
//	PLUMB_PVE_BRIDGE, PLUMB_PVE_IMAGE_URL, PLUMB_PVE_IMAGE_SHA256
//
// The VMID range is 9000–9099 (ARCHITECTURE §8). Every test removes
// what it made.
package proxmox

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

func hostDriver(t *testing.T) *Driver {
	t.Helper()
	env := func(k string) string {
		v := os.Getenv(k)
		if v == "" {
			t.Fatalf("%s is not set", k)
		}
		return v
	}
	c, err := NewClient(ClientConfig{URL: env("PLUMB_PVE_URL"), TokenID: env("PLUMB_PVE_TOKEN_ID"),
		Secret: env("PLUMB_PVE_SECRET"), CAFile: os.Getenv("PLUMB_PVE_CA")})
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(Config{Client: c, Node: env("PLUMB_PVE_NODE"), Pool: env("PLUMB_PVE_POOL"),
		DiskStorage: env("PLUMB_PVE_DISK"), ImageStorage: env("PLUMB_PVE_IMAGES"),
		SnippetStorage: env("PLUMB_PVE_SNIPPETS"), Snippet: env("PLUMB_PVE_SNIPPET"), VMIDMin: 9000, VMIDMax: 9099})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestHostLifecycle: import, create, observe, update, re-enroll, delete.
func TestHostLifecycle(t *testing.T) {
	d := hostDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	ref, err := d.FetchImage(ctx, driver.ImageSpec{Name: "test-image", Kind: wire.KindVM,
		URL: os.Getenv("PLUMB_PVE_IMAGE_URL"), SHA256: os.Getenv("PLUMB_PVE_IMAGE_SHA256")})
	if err != nil {
		t.Fatal(err)
	}
	spec := driver.GuestSpec{Name: "plumb-host-test", Kind: wire.KindVM, Image: ref, Cores: 1, MemoryMB: 1024, DiskGB: 8,
		Bridge: os.Getenv("PLUMB_PVE_BRIDGE"), IP: "dhcp", User: "plumb"}
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "host-test-token-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Delete(context.Background(), id); err != nil {
			t.Errorf("cleanup: %v (remove VMID %s by hand)", err, id)
		}
	})
	st, err := d.Observe(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := d.Diff(spec, st); err != nil || len(c) != 0 {
		t.Errorf("round trip on the host: %+v %v (observed %+v)", c, err, st.Spec)
	}
	spec.Cores, spec.MemoryMB = 2, 1536
	changes, _ := d.Diff(spec, st)
	if err := d.Update(ctx, id, changes, nil); err != nil {
		t.Fatal(err)
	}
	token := []wire.Change{{Attr: driver.AttrEnrollToken, Sensitive: true, Risk: wire.RiskDisruptive}}
	if err := d.Update(ctx, id, token, &driver.Bootstrap{Token: "host-test-token-2"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Power(ctx, id, driver.PowerRestart); err != nil {
		t.Fatal(err)
	}
	after, err := d.Observe(ctx, id)
	if err != nil || after.Power != driver.PowerRunning || after.Digest == st.Digest || after.Spec.Cores != 2 {
		t.Errorf("after update: %+v %v", after, err)
	}
}
