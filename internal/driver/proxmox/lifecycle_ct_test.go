package proxmox

import (
	"context"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// The container test template of the host runs (T-079): the Proxmox
// Debian 13 system template, pinned by the SHA-512 of the signed index
// (host A2). A fixed test CA and agent URL keep the file name stable, so
// the replay finds the recorded file; no agent ever enrolls in this test.
var hostCTImage = driver.ImageSpec{Name: "t079-debian-13", Kind: wire.KindLXC,
	URL: "http://download.proxmox.com/images/system/debian-13-standard_13.6-1_amd64.tar.zst",
	// sha512sum of debian-13-standard 13.6-1 in aplinfo-pve-9.dat (pve-dev, 2026-11-12).
	SHA512: "4c0c27ca6ceab5ef0b84db57825a00f26157ef1854bafe97297813e1cbe8ecb8cc9c453cab6b3b0efe1ba193a50c47ece1e41d950e411b8730b835b71e9e754b"}

func testCTSource(dir string) CTSource {
	return CTSource{TempDir: dir, AgentURL: "https://192.0.2.5:8444",
		CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("plumb t079 host test, no real certificate")})}
}

// runCTLifecycle: the file exists (TestHostCTFetch made it); create,
// observe, live update, clear, re-enroll, delete, then H11: a new
// container with the freed VMID starts.
func runCTLifecycle(t *testing.T, d *Driver, img driver.ImageSpec, bridge string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	ref, err := d.FetchImage(ctx, img)
	if err != nil {
		t.Fatal(err)
	}
	spec := driver.GuestSpec{Name: "plumb-ct-test", Kind: wire.KindLXC, ImageName: img.Name, Image: ref, Cores: 1, MemoryMB: 512, DiskGB: 4,
		Bridge: bridge, IP: "dhcp", SSHKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPlumbHostTestKeyOnlyForT079xxxxxxxxxxxx t079"}}
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "host-test-ct-token-1"})
	if err != nil {
		t.Fatal(err)
	}
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			if err := d.Delete(context.Background(), id); err != nil {
				t.Errorf("cleanup: %v (remove CT %s by hand)", err, id)
			}
		}
	})
	st, err := d.Observe(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := d.Diff(spec, st); err != nil || len(c) != 0 {
		t.Errorf("round trip: %+v %v (observed %+v)", c, err, st.Spec)
	}
	if st.Power != driver.PowerRunning || !st.TokenInConfig || st.TokenPending {
		t.Errorf("after create: %+v", st)
	}
	spec.Cores, spec.MemoryMB = 2, 768
	changes, _ := d.Diff(spec, st)
	if err := d.Update(ctx, id, changes, nil); err != nil {
		t.Fatal(err)
	}
	live, err := d.Observe(ctx, id)
	if err != nil || live.Spec.Cores != 2 || live.Spec.MemoryMB != 768 || live.Pending != nil {
		t.Errorf("live update (host C5): %+v pending %+v %v", live.Spec, live.Pending, err)
	}
	if err := d.ClearBootstrap(ctx, id); err != nil {
		t.Fatal(err)
	}
	cleared, err := d.Observe(ctx, id)
	if err != nil || cleared.TokenInConfig || cleared.TokenPending {
		t.Errorf("after the clear (D-246): %+v %v", cleared, err)
	}
	token := []wire.Change{{Attr: driver.AttrEnrollToken, Sensitive: true, Risk: wire.RiskDisruptive}}
	if err := d.Update(ctx, id, token, &driver.Bootstrap{Token: "host-test-ct-token-2"}); err != nil {
		t.Fatal(err)
	}
	pend, err := d.Observe(ctx, id)
	if err != nil || !pend.TokenPending || !pend.TokenInConfig {
		t.Errorf("new token on a running CT: %+v %v", pend, err)
	}
	if err := d.Power(ctx, id, driver.PowerRestart); err != nil {
		t.Fatal(err)
	}
	after, err := d.Observe(ctx, id)
	if err != nil || after.TokenPending || !after.TokenInConfig || after.Power != driver.PowerRunning {
		t.Errorf("after the restart: %+v %v", after, err)
	}
	if err := d.ClearBootstrap(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	deleted = true
	if _, err := d.Observe(ctx, id); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("observe after delete: %v", err)
	}
	// H11: Proxmox keeps /var/lib/lxc/<id>/{passthrough,rules.seccomp}
	// (host C8); a new container with the same VMID must still start.
	again, err := d.Create(ctx, spec, driver.Bootstrap{Token: "host-test-ct-token-3"})
	if err != nil {
		t.Fatalf("H11: %v", err)
	}
	t.Logf("host fact: H11 – CT %s deleted, the next create got VMID %s and started", id, again)
	if err := d.Delete(ctx, again); err != nil {
		t.Errorf("cleanup: %v (remove CT %s by hand)", err, again)
	}
}
