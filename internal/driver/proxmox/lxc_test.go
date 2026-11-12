package proxmox

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
		SSHKeys: []string{"ssh-ed25519 AAAAC3Nza one@example", "ssh-ed25519 AAAAC3Nzb two@example"}, Features: driver.CTFeatures}
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

// A concurrent config edit must reject the clear without deleting env.
func TestCTClearBootstrapStaleDigest(t *testing.T) {
	f, d, id := newCT(t)
	f.ctWriteBeforeConfig = true
	err := d.ClearBootstrap(context.Background(), id)
	if err == nil || !strings.Contains(err.Error(), "modified configuration") {
		t.Fatalf("stale digest: %v", err)
	}
	if f.count("PUT /nodes/pve/lxc/9000/config") != 1 || f.vms[9000].delete["env"] || f.vms[9000].cfg["env"] != tokenPrefix+ctToken {
		t.Fatal("stale clear retried or changed env")
	}
}

// Exercise the pending-delete view independently of ClearBootstrap.
func TestCTObserveTokenViews(t *testing.T) {
	for _, mode := range []string{"active", "set", "delete"} {
		t.Run(mode, func(t *testing.T) {
			f, d, id := newCT(t)
			f.mu.Lock()
			switch mode {
			case "set":
				delete(f.vms[9000].cfg, "env")
				f.vms[9000].pending = map[string]string{"env": tokenPrefix + ctToken}
			case "delete":
				f.vms[9000].delete = map[string]bool{"env": true}
			}
			f.mu.Unlock()
			st, err := d.Observe(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if st.TokenInConfig != (mode != "delete") || st.TokenPending != (mode == "set") || st.Pending != nil {
				t.Errorf("%s: in config=%v, pending token=%v, pending spec=%v", mode, st.TokenInConfig, st.TokenPending, st.Pending)
			}
		})
	}
}

func TestCTFailedCreateCleanupFailure(t *testing.T) {
	f := newFakePVE(t)
	f.files[ctTemplate] = true
	d := f.driver(t)
	f.failNext["POST /nodes/pve/lxc"] = "unpack failed " + ctToken
	f.failNext["DELETE /nodes/pve/lxc/9000"] = "storage busy " + ctToken
	id, err := d.Create(context.Background(), ctSpec(), driver.Bootstrap{Token: ctToken})
	if err == nil {
		t.Fatal("created")
	}
	for _, want := range []string{"unpack failed", "storage busy", "pct destroy 9000"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q: %v", want, err)
		}
	}
	if id != "" || strings.Contains(err.Error(), ctToken) || len(f.vms) != 1 || f.count("DELETE /nodes/pve/lxc/9000") != 1 || f.count("POST /nodes/pve/lxc/9000/status/start") != 0 {
		t.Fatal("wrong cleanup result or leaked token")
	}
}

func TestCTObserveOwnershipGuards(t *testing.T) {
	for _, mode := range []string{"privileged", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			f, d, id := newCT(t)
			f.mu.Lock()
			if mode == "privileged" {
				f.vms[9000].cfg["unprivileged"] = "0"
			} else {
				f.vms[9000].pool = "other"
			}
			f.mu.Unlock()
			_, err := d.Observe(context.Background(), id)
			if mode == "foreign" && !errors.Is(err, driver.ErrNotFound) {
				t.Fatalf("foreign: %v", err)
			}
			if mode == "privileged" && (err == nil || !strings.Contains(err.Error(), "only unprivileged containers")) {
				t.Fatalf("privileged: %v", err)
			}
		})
	}
}

func TestCTDescriptionByteLimit(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(string(rune('0'+extra)), func(t *testing.T) {
			f := newFakePVE(t)
			f.files[ctTemplate] = true
			d := f.driver(t)
			s := ctSpec()
			s.SSHKeys = []string{"ssh-ed25519 "}
			// Multibyte comments count as bytes, not characters.
			s.SSHKeys[0] += strings.Repeat("é", (8192-len(ctDescription(s)))/2)
			s.SSHKeys[0] += strings.Repeat("A", 8192+extra-len(ctDescription(s)))
			_, err := d.Create(context.Background(), s, driver.Bootstrap{Token: ctToken})
			if extra == 0 {
				if err != nil {
					t.Fatal(err)
				}
				st, err := d.Observe(context.Background(), "9000")
				if err != nil || len(st.Spec.SSHKeys) != 1 || st.Spec.SSHKeys[0] != s.SSHKeys[0] {
					t.Fatal("boundary key did not round trip")
				}
			} else if err == nil || !strings.Contains(err.Error(), "8193 bytes") || f.count("POST /nodes/pve/lxc") != 0 {
				t.Fatalf("oversized description: %v", err)
			}
		})
	}
}

func TestCTKeyLineReplaced(t *testing.T) {
	f, d, id := newCT(t)
	posts := f.count("POST ")
	f.mu.Lock()
	f.vms[9000].cfg["description"] = strings.Replace(f.vms[9000].cfg["description"], ctSpec().SSHKeys[1], "ssh-ed25519 AAAAC3Nzc edited@example", 1)
	f.mu.Unlock()
	st, err := d.Observe(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Spec.SSHKeys) != 2 || st.Spec.SSHKeys[1] != "ssh-ed25519 AAAAC3Nzc edited@example" {
		t.Fatalf("edited keys: %v", st.Spec.SSHKeys)
	}
	if drift := driver.DriftGuest(ctSpec(), st); len(drift) != 1 || drift[0].Attr != driver.AttrSSHKeys {
		t.Errorf("drift: %+v", drift)
	}
	var co *driver.CreateOnlyError
	if _, err := d.Diff(ctSpec(), st); !errors.As(err, &co) || co.Attr != driver.AttrSSHKeys {
		t.Errorf("diff: %v", err)
	}
	if f.count("PUT ") != 0 || f.count("DELETE ") != 0 || f.count("POST ") != posts {
		t.Fatal("edited key caused a write")
	}
}

// Review S1 of #162: a key comes back byte-equal (D-249); one with white
// space at its ends or a control character is refused.
func TestCTKeyRoundTrip(t *testing.T) {
	f := newFakePVE(t)
	f.files[ctTemplate] = true
	d := f.driver(t)
	ctx := context.Background()
	s := ctSpec()
	s.SSHKeys = []string{"ssh-ed25519 AAAAC3Nza Jürgen's Schlüssel ✓", "ssh-rsa AAAAB3Nzb x"}
	id, err := d.Create(ctx, s, driver.Bootstrap{Token: ctToken})
	if err != nil {
		t.Fatal(err)
	}
	st, err := d.Observe(ctx, id)
	if err != nil || !reflect.DeepEqual(st.Spec.SSHKeys, s.SSHKeys) {
		t.Fatalf("keys %q %v", st.Spec.SSHKeys, err)
	}
	for _, k := range []string{"ssh-ed25519 AAAAC3Nza x ", " ssh-ed25519 AAAAC3Nza", "ssh-ed25519 AAAAC3Nza a\tb", "ssh-ed25519 AAAAC3Nza a\x00b", "ssh-ed25519 AAAAC3Nza a\x7fb"} {
		s.SSHKeys = []string{k}
		if _, err := d.Create(ctx, s, driver.Bootstrap{Token: ctToken}); err == nil {
			t.Errorf("%q accepted", k)
		}
	}
}

// T-038 §3 (review S6): what a person adds on the host is read and is
// drift; Diff refuses the additions Plumb cannot remove; Plumb writes the
// features back, and they wait for the next start.
func TestCTOutsideAdditions(t *testing.T) {
	f, d, id := newCT(t)
	ctx := context.Background()
	f.mu.Lock()
	c := f.vms[9000].cfg
	c["features"] = "nesting=1,keyctl=1"
	c["mp0"] = "/srv/data,mp=/data"
	c["mp1"] = "local-lvm:vm-9000-disk-1,mp=/var,size=2G"
	c["dev0"] = "/dev/fuse"
	c["lxc"] = "lxc.apparmor.profile: unconfined\nlxc.cap.drop: "
	f.mu.Unlock()
	st, err := d.Observe(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Spec
	if s.Features != "keyctl=1,nesting=1" || !reflect.DeepEqual(s.Mounts, []string{"mp0=/srv/data,mp=/data", "mp1=local-lvm:vm-9000-disk-1,mp=/var,size=2G"}) ||
		!reflect.DeepEqual(s.Devices, []string{"dev0=/dev/fuse"}) || !reflect.DeepEqual(s.RawLXC, []string{"lxc.apparmor.profile: unconfined", "lxc.cap.drop: "}) {
		t.Fatalf("observed %+v", s)
	}
	drift := driver.DriftGuest(ctSpec(), st)
	var attrs []string
	for _, ch := range drift {
		attrs = append(attrs, ch.Attr)
		if ch.Risk != wire.RiskDestructive {
			t.Errorf("%s risk %s", ch.Attr, ch.Risk)
		}
	}
	if strings.Join(attrs, ",") != "features,mounts,devices,raw_lxc" {
		t.Errorf("drift attrs %v", attrs)
	}
	var oe *driver.OutsideError
	if _, err := d.Diff(ctSpec(), st); !errors.As(err, &oe) || oe.Attr != driver.AttrMounts {
		t.Errorf("diff: %v", err)
	}
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrMounts, To: nil}}, nil); !errors.As(err, &oe) {
		t.Errorf("update of mounts: %v", err)
	}
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrFeatures, To: "nesting=1,fuse=1"}}, nil); err == nil {
		t.Error("features other than nesting written")
	}
	// The person removes the rest; Plumb writes the features back.
	f.mu.Lock()
	for _, k := range []string{"mp0", "mp1", "dev0", "lxc"} {
		delete(c, k)
	}
	f.mu.Unlock()
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrFeatures, To: driver.CTFeatures}}, nil); err != nil {
		t.Fatal(err)
	}
	if p := f.lastParams("PUT /nodes/pve/lxc/9000/config"); p.Get("features") != "nesting=1" || p.Get("digest") == "" {
		t.Errorf("params %v", p)
	}
	st, _ = d.Observe(ctx, id)
	if st.Spec.Features != "keyctl=1,nesting=1" || st.Next().Features != driver.CTFeatures {
		t.Errorf("features %q next %q", st.Spec.Features, st.Next().Features)
	}
	if drift := driver.DriftGuest(ctSpec(), st); len(drift) != 0 {
		t.Errorf("waiting features are drift: %+v", drift)
	}
}

// T-085: the container template files of an Image, with the containers
// made from each (description marker); RemoveImage never deletes one.
func TestCTTemplateFiles(t *testing.T) {
	f, d, _ := newCT(t)
	ctx := context.Background()
	if !strings.Contains(f.vms[9000].cfg["description"], "plumb-template: "+ctTemplate+"\n") {
		t.Errorf("description %q", f.vms[9000].cfg["description"])
	}
	other := "local:vztmpl/plumb-ct-debian-13-ffffffffffff.tar.gz"
	f.files[other] = true
	f.files["local:vztmpl/plumb-ct-debian-13-ct-ffffffffffff.tar.gz"] = true // another Image
	f.files["local:vztmpl/plumb-ct-alpine-0123456789ab.tar.gz"] = true
	f.files["local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst"] = true
	f.vms[9001] = &fakeVM{ct: true, pool: "plumb", cfg: map[string]string{"description": "plumb-template: " + ctTemplate + "\n"}}
	f.vms[9002] = &fakeVM{ct: true, pool: "plumb", cfg: map[string]string{"description": "plumb-template: " + other + "\n"}}
	f.vms[9003] = &fakeVM{ct: true, pool: "other", cfg: map[string]string{"description": "plumb-template: " + other + "\n"}}
	d.templates = false
	objs, _ := d.ListImages(ctx, "debian-13")
	for _, o := range objs {
		if o.Kind == driver.ImageTemplateFile {
			t.Error("template files listed without vztmpl content")
		}
	}
	d.templates = true
	objs, err := d.ListImages(ctx, "debian-13")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range objs {
		if o.Kind == driver.ImageTemplateFile {
			got = append(got, fmt.Sprintf("%s:%d", o.Name, o.Containers))
			if err := d.RemoveImage(ctx, o); err == nil || !strings.Contains(err.Error(), "never deletes") {
				t.Errorf("RemoveImage %s: %v", o.Name, err)
			}
		}
	}
	if strings.Join(got, " ") != ctTemplate+":2 "+other+":1" {
		t.Errorf("files %v", got)
	}
}

func TestCTOutsideAdditionsIndividually(t *testing.T) {
	for _, tc := range []struct{ key, value, attr string }{
		{"features", "nesting=1,fuse=1", driver.AttrFeatures},
		{"mp2", "/srv/data,mp=/data", driver.AttrMounts},
		{"dev3", "/dev/fuse", driver.AttrDevices},
		{"lxc", "lxc.apparmor.profile: unconfined", driver.AttrRawLXC},
	} {
		for _, pending := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/pending=%t", tc.attr, pending), func(t *testing.T) {
				f, d, id := newCT(t)
				f.mu.Lock()
				if pending {
					f.vms[9000].pending = map[string]string{tc.key: tc.value}
				} else {
					f.vms[9000].cfg[tc.key] = tc.value
				}
				f.mu.Unlock()
				st, err := d.Observe(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				changes := driver.DriftGuest(ctSpec(), st)
				if len(changes) != 1 || changes[0].Attr != tc.attr || changes[0].Risk != wire.RiskDestructive || changes[0].Pending != pending {
					t.Fatalf("drift: %+v", changes)
				}
				changes, err = d.Diff(ctSpec(), st)
				if tc.attr != driver.AttrFeatures {
					var outside *driver.OutsideError
					if !errors.As(err, &outside) || outside.Attr != tc.attr {
						t.Fatalf("diff: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := d.Update(context.Background(), id, changes, nil); err != nil {
					t.Fatal(err)
				}
				params := f.lastParams("PUT /nodes/pve/lxc/9000/config")
				if params.Get("features") != driver.CTFeatures || params.Get("digest") == "" || len(params) != 2 {
					t.Fatalf("revert must write only features and digest: %v", params)
				}
			})
		}
	}
}
