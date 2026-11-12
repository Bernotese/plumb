package proxmox

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

const (
	ctTemplate = "local:vztmpl/plumb-ct-debian-13-0123456789ab.tar.gz"
	ctToken    = "ct-token-0123456789abcdefghijklmnopqrstuvwxyz"
)

func ctSpec() driver.GuestSpec {
	return driver.GuestSpec{Name: "web1", Kind: wire.KindLXC, ImageName: "debian-13-ct", Image: ctTemplate,
		Cores: 1, MemoryMB: 512, DiskGB: 8, Bridge: "vmbr0", IP: "192.0.2.21/24", Gateway: "192.0.2.1",
		SSHKeys: []string{"ssh-ed25519 AAAAC3Nza one@example", "ssh-ed25519 AAAAC3Nzb two@example"}}
}

func newCT(t *testing.T) (*fakePVE, *Driver, driver.GuestID) {
	t.Helper()
	f := newFakePVE(t)
	f.files[ctTemplate] = true
	d := f.driver(t)
	id, err := d.Create(context.Background(), ctSpec(), driver.Bootstrap{Token: ctToken})
	if err != nil {
		t.Fatal(err)
	}
	return f, d, id
}

func TestCTCreate(t *testing.T) {
	f, d, id := newCT(t)
	p := f.lastParams("POST /nodes/pve/lxc")
	want := map[string]string{"ostemplate": ctTemplate, "pool": "plumb", "hostname": "web1", "unprivileged": "1",
		"features": "nesting=1", "rootfs": "local-lvm:8", "net0": "name=eth0,bridge=vmbr0,ip=192.0.2.21/24,gw=192.0.2.1",
		"nameserver": "192.0.2.53", "searchdomain": "plumb.test", "cores": "1", "memory": "512", "swap": "512", "onboot": "1",
		"env": "PLUMB_ENROLL_TOKEN=" + ctToken, "ssh-public-keys": "ssh-ed25519 AAAAC3Nza one@example\nssh-ed25519 AAAAC3Nzb two@example"}
	for k, v := range want {
		if p.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, p.Get(k), v)
		}
	}
	for _, k := range []string{"mp0", "dev0", "lxc", "password"} {
		if p.Has(k) {
			t.Errorf("create sends %s", k)
		}
	}
	if f.count("POST /nodes/pve/lxc/"+string(id)+"/status/start") != 1 {
		t.Error("not started")
	}
	st, err := d.Observe(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Power != driver.PowerRunning || !st.TokenInConfig || st.TokenPending || st.Pending != nil {
		t.Errorf("state %+v", st)
	}
	if ch, err := d.Diff(ctSpec(), st); err != nil || len(ch) != 0 {
		t.Errorf("diff after create: %+v %v", ch, err)
	}
	if len(st.IPs) != 1 || st.IPs[0] != "192.0.2.21" {
		t.Errorf("ips %v", st.IPs)
	}
}

// The token clear on a running container is pending: the next view has
// no token at once (D-246), the running one keeps it until the restart.
func TestCTClearBootstrap(t *testing.T) {
	f, d, id := newCT(t)
	ctx := context.Background()
	if err := d.ClearBootstrap(ctx, id); err != nil {
		t.Fatal(err)
	}
	p := f.lastParams("PUT /nodes/pve/lxc/" + string(id) + "/config")
	if p.Get("delete") != "env" || p.Get("digest") == "" || len(p) != 2 {
		t.Errorf("clear params %v", p)
	}
	st, _ := d.Observe(ctx, id)
	if st.TokenInConfig || st.TokenPending || st.Pending != nil {
		t.Errorf("after clear %+v", st)
	}
	if f.vms[9000].cfg["env"] == "" {
		t.Error("the running config lost env before the restart (host C4)")
	}
	// Nothing to clear: no write.
	before := f.count("PUT ")
	if err := d.ClearBootstrap(ctx, id); err != nil || f.count("PUT ") != before {
		t.Errorf("second clear: %v, writes %d → %d", err, before, f.count("PUT "))
	}
	if err := d.Power(ctx, id, driver.PowerRestart); err != nil {
		t.Fatal(err)
	}
	if f.vms[9000].cfg["env"] != "" {
		t.Error("env still active after the restart")
	}
}

func TestCTReenroll(t *testing.T) {
	f, d, id := newCT(t)
	ctx := context.Background()
	_ = d.ClearBootstrap(ctx, id)
	_ = d.Power(ctx, id, driver.PowerRestart)
	tok := "new-token-zyxwvutsrqponmlkjihgfedcba9876543210"
	err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrEnrollToken, Sensitive: true, Risk: wire.RiskDisruptive}}, &driver.Bootstrap{Token: tok})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := d.Observe(ctx, id)
	if !st.TokenPending || !st.TokenInConfig {
		t.Errorf("after the write %+v", st)
	}
	if err := d.Power(ctx, id, driver.PowerRestart); err != nil {
		t.Fatal(err)
	}
	st, _ = d.Observe(ctx, id)
	if st.TokenPending || !st.TokenInConfig || envToken(f.vms[9000].cfg["env"]) != tok {
		t.Errorf("after the restart %+v", st)
	}
	// No value in the Change, no Bootstrap: refused.
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrEnrollToken, Sensitive: true}}, nil); err == nil {
		t.Error("token change without Bootstrap")
	}
}

// Cores and memory apply live (host C5); the bridge keeps hwaddr and ip.
func TestCTUpdateLive(t *testing.T) {
	f, d, id := newCT(t)
	ctx := context.Background()
	d.cfg.Bridge = "vmbr1"
	err := d.Update(ctx, id, []wire.Change{
		{Attr: driver.AttrCores, To: 2}, {Attr: driver.AttrMemory, To: 1024}, {Attr: driver.AttrBridge, To: "vmbr1"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := d.Observe(ctx, id)
	if st.Spec.Cores != 2 || st.Spec.MemoryMB != 1024 || st.Spec.Bridge != "vmbr1" || st.Pending != nil {
		t.Errorf("state %+v pending %+v", st.Spec, st.Pending)
	}
	if n := f.vms[9000].cfg["net0"]; !strings.Contains(n, "hwaddr=BC:24:11:00:00:01") || !strings.Contains(n, "ip=192.0.2.21/24") {
		t.Errorf("net0 %q", n)
	}
	var co *driver.CreateOnlyError
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrDisk, To: 9}}, nil); !errors.As(err, &co) {
		t.Errorf("disk change: %v", err)
	}
}

func TestCTRefusals(t *testing.T) {
	f := newFakePVE(t)
	f.files[ctTemplate] = true
	d := f.driver(t)
	ctx := context.Background()
	bad := []func(*driver.GuestSpec){
		func(s *driver.GuestSpec) { s.User = "debian" },
		func(s *driver.GuestSpec) { s.Image = "local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst" },
		func(s *driver.GuestSpec) { s.Image = "other:vztmpl/plumb-ct-debian-13-0123456789ab.tar.gz" },
		func(s *driver.GuestSpec) { s.Bridge = "vmbr9" },
		func(s *driver.GuestSpec) { s.Name = "web1.example" },
		func(s *driver.GuestSpec) { s.SSHKeys = []string{"ssh-ed25519 A\nmp0: /"} },
		func(s *driver.GuestSpec) { s.SSHKeys = []string{"ssh-rsa " + strings.Repeat("A", 8200)} },
	}
	for i, mod := range bad {
		s := ctSpec()
		mod(&s)
		if _, err := d.Create(ctx, s, driver.Bootstrap{Token: ctToken}); err == nil {
			t.Errorf("case %d: created", i)
		}
	}
	if n := f.count("POST /nodes/pve/lxc"); n != 0 {
		t.Errorf("%d create calls for refused specs", n)
	}
}

// A container that a person made privileged, or a VM, is refused by the
// methods of the other type.
func TestCTTypeChecks(t *testing.T) {
	f, d, id := newCT(t)
	ctx := context.Background()
	f.addCT(9050, "plumb", map[string]string{"hostname": "old", "unprivileged": "0", "cores": "1", "memory": "512"}, false)
	if _, err := d.Observe(ctx, "9050"); err == nil || !strings.Contains(err.Error(), "only unprivileged containers") {
		t.Errorf("privileged: %v", err)
	}
	if err := d.RemoveBuild(ctx, id, "img", 1); err == nil || !strings.Contains(err.Error(), "is a container") {
		t.Errorf("RemoveBuild on a container: %v", err)
	}
	if _, err := d.MakeImage(ctx, id, driver.BuiltImage{Image: "img", Version: 1, Digest: strings.Repeat("a", 64)}); err == nil {
		t.Error("MakeImage on a container")
	}
	f.addVM(9051, "plumb", map[string]string{"name": "vm1"}, false)
	if err := d.ClearBootstrap(ctx, "9051"); err == nil {
		t.Error("ClearBootstrap on a VM")
	}
}

func TestCTDelete(t *testing.T) {
	f, d, id := newCT(t)
	ctx := context.Background()
	if err := d.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if f.count("POST /nodes/pve/lxc/9000/status/stop") != 1 || len(f.vms) != 0 {
		t.Errorf("stop %d, guests %d", f.count("POST /nodes/pve/lxc/9000/status/stop"), len(f.vms))
	}
	if _, err := d.Observe(ctx, id); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("observe after delete: %v", err)
	}
}

// A failed start removes the half-made container; the token is in no error.
func TestCTCreateFailureCleansUp(t *testing.T) {
	f := newFakePVE(t)
	f.files[ctTemplate] = true
	d := f.driver(t)
	f.failNext["POST /nodes/pve/lxc/9000/status/start"] = "startup for container '9000' failed " + ctToken
	_, err := d.Create(context.Background(), ctSpec(), driver.Bootstrap{Token: ctToken})
	if err == nil {
		t.Fatal("created")
	}
	if strings.Contains(err.Error(), ctToken) {
		t.Errorf("token in the error: %v", err)
	}
	if len(f.vms) != 0 {
		t.Errorf("half-made container left: %d guests", len(f.vms))
	}
	// A create task that failed after Proxmox wrote the config.
	f.failNext["POST /nodes/pve/lxc"] = "unable to create CT 9000 - zstd: error"
	if _, err := d.Create(context.Background(), ctSpec(), driver.Bootstrap{Token: ctToken}); err == nil || len(f.vms) != 0 {
		t.Errorf("failed create: %v, guests %d", err, len(f.vms))
	}
}

// D-249: a person removes a key line from the description. Observe shows
// it, Diff refuses the create-only change (a Plan says "create a new
// target"), and nothing writes.
func TestCTKeyLineEdited(t *testing.T) {
	f, d, id := newCT(t)
	ctx := context.Background()
	f.mu.Lock()
	f.vms[9000].cfg["description"] = strings.Replace(f.vms[9000].cfg["description"], "plumb-ssh-key: ssh-ed25519 AAAAC3Nzb two@example\n", "", 1)
	f.mu.Unlock()
	st, err := d.Observe(ctx, id)
	if err != nil || len(st.Spec.SSHKeys) != 1 {
		t.Fatalf("observe %v %v", st.Spec.SSHKeys, err)
	}
	if drift := driver.DriftGuest(ctSpec(), st); len(drift) != 1 || drift[0].Attr != driver.AttrSSHKeys {
		t.Errorf("drift %+v", drift)
	}
	var co *driver.CreateOnlyError
	if _, err := d.Diff(ctSpec(), st); !errors.As(err, &co) || co.Attr != driver.AttrSSHKeys {
		t.Errorf("diff %v", err)
	}
}
