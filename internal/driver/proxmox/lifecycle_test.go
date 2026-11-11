package proxmox

// The lifecycle that TestHostLifecycle runs on the real host (make
// test-pve) and TestReplayLifecycle runs offline against the recorded
// answers in testdata/pve/lifecycle.

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// runLifecycle: import, create, observe, update, re-enroll, restart, delete.
func runLifecycle(t *testing.T, d *Driver, img driver.ImageSpec, bridge string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	ref, err := d.FetchImage(ctx, img)
	if err != nil {
		t.Fatal(err)
	}
	// Delete refuses templates, so the test removes its own template
	// through the client (review T-012 #10): no guest stays behind.
	t.Cleanup(func() {
		id, m, err := d.own(context.Background(), string(ref))
		if err != nil || m.Template != 1 {
			t.Errorf("cleanup of template %s: %v", ref, err)
			return
		}
		if err := d.c.Task(context.Background(), d.cfg.Node, "DELETE", vmPath(d, id), url.Values{"purge": {"1"}}); err != nil {
			t.Errorf("cleanup of template %s: %v (remove it by hand)", ref, err)
		}
	})
	spec := driver.GuestSpec{Name: "plumb-host-test", Kind: wire.KindVM, ImageName: img.Name, Image: ref, Cores: 1, MemoryMB: 1024, DiskGB: 8,
		Bridge: bridge, IP: "dhcp", User: "plumb"}
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
