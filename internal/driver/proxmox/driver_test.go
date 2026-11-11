package proxmox

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

const sum = "3f1c0a9e5d6b7c8a9f0e1d2c3b4a5968778695a4b3c2d1e0f9e8d7c6b5a49382"

var image = driver.ImageSpec{Name: "ubuntu-26.04", Kind: wire.KindVM,
	URL: "https://cloud-images.ubuntu.com/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64.img", SHA256: sum}

func TestClientConfig(t *testing.T) {
	f := newFakePVE(t)
	for name, cfg := range map[string]ClientConfig{
		"plain http": {URL: "http://pve:8006", TokenID: fakeToken, Secret: fakeSecret},
		"no token":   {URL: f.srv.URL, Secret: fakeSecret},
		"no secret":  {URL: f.srv.URL, TokenID: fakeToken},
		"bad CA":     {URL: f.srv.URL, TokenID: fakeToken, Secret: fakeSecret, CAFile: "/nonexistent"},
	} {
		if _, err := NewClient(cfg); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	// Without the host CA file the system roots do not trust the host:
	// the client never skips verification.
	c, err := NewClient(ClientConfig{URL: f.srv.URL, TokenID: fakeToken, Secret: fakeSecret})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Get(context.Background(), "/cluster/nextid", url.Values{"vmid": {"9000"}}, nil); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("system roots: %v", err)
	}
	// With it, it works; a wrong secret is an APIError, and the secret is
	// in no error text.
	if err := f.client(t).Get(context.Background(), "/cluster/nextid", url.Values{"vmid": {"9000"}}, nil); err != nil {
		t.Fatal(err)
	}
	bad, _ := NewClient(ClientConfig{URL: f.srv.URL, TokenID: fakeToken, Secret: "wrong-secret", CAFile: f.caFile})
	err = bad.Get(context.Background(), "/cluster/nextid", nil, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 401 || strings.Contains(err.Error(), "wrong-secret") {
		t.Errorf("wrong secret: %v", err)
	}
}

func TestTasks(t *testing.T) {
	f := newFakePVE(t)
	f.taskPolls = 4
	c := f.client(t)
	ctx := context.Background()
	f.addVM(9001, fakePool, map[string]string{"name": "x"}, false)
	if err := c.Task(ctx, fakeNode, "POST", "/nodes/pve/qemu/9001/status/start", nil); err != nil {
		t.Fatal(err)
	}
	if f.vms[9001].running != true {
		t.Error("start did not run")
	}
	// A failed task is a TaskError with the end of its log.
	err := c.Task(ctx, fakeNode, "POST", "/nodes/pve/qemu/9001/status/start", nil)
	var te *TaskError
	if !errors.As(err, &te) || !strings.Contains(te.ExitStatus, "already running") || len(te.Log) == 0 {
		t.Errorf("failed task: %v", err)
	}
	// Waiting respects the context.
	f.taskPolls = 1 << 30
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := c.Task(short, fakeNode, "POST", "/nodes/pve/qemu/9001/status/shutdown", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("endless task: %v", err)
	}
}

func TestFetchImage(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, err := d.FetchImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	if ref != "9000" {
		t.Errorf("ref = %s", ref)
	}
	dl := f.lastParams("POST /nodes/pve/storage/local/download-url")
	if dl.Get("checksum") != sum || dl.Get("checksum-algorithm") != "sha256" || dl.Get("content") != "import" ||
		dl.Get("url") != image.URL || dl.Get("filename") != "plumb-ubuntu-26.04-3f1c0a9e5d6b.qcow2" || dl.Get("verify-certificates") != "1" {
		t.Errorf("download-url params: %v", dl)
	}
	cr := f.lastParams("POST /nodes/pve/qemu")
	if cr.Get("scsi0") != "local-lvm:0,import-from=local:import/plumb-ubuntu-26.04-3f1c0a9e5d6b.qcow2" ||
		cr.Get("ide2") != "local-lvm:cloudinit" || cr.Get("pool") != fakePool || cr.Get("name") != "plumb-img-ubuntu-26.04-3f1c0a9e5d6b" {
		t.Errorf("create params: %v", cr)
	}
	if !f.vms[9000].template {
		t.Error("not a template")
	}
	// Idempotent: the same image is the same template, no second download.
	again, err := d.FetchImage(ctx, image)
	if err != nil || again != ref || f.count("POST /nodes/pve/storage/local/download-url") != 1 || f.countExact("POST /nodes/pve/qemu") != 1 {
		t.Errorf("second fetch: %s %v, downloads %d", again, err, f.count("POST /nodes/pve/storage/local/download-url"))
	}
	// A new checksum is a new template.
	other := image
	other.SHA256 = strings.Repeat("a", 64)
	if ref2, err := d.FetchImage(ctx, other); err != nil || ref2 == ref {
		t.Errorf("new checksum: %s %v", ref2, err)
	}
	// A failed download (checksum mismatch on the host) is an error, and no template.
	f.failNext["POST /nodes/pve/storage/local/download-url"] = "checksum mismatch"
	bad := image
	bad.SHA256 = strings.Repeat("b", 64)
	if _, err := d.FetchImage(ctx, bad); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("failed download: %v", err)
	}
	for name, spec := range map[string]driver.ImageSpec{
		"http":   {Name: "x", Kind: wire.KindVM, URL: "http://x", SHA256: sum},
		"no sum": {Name: "x", Kind: wire.KindVM, URL: "https://x"},
		"lxc":    {Name: "x", Kind: wire.KindLXC, URL: "https://x", SHA256: sum},
	} {
		if _, err := d.FetchImage(ctx, spec); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func guest(ref driver.ImageRef) driver.GuestSpec {
	return driver.GuestSpec{Name: "web1", Kind: wire.KindVM, Image: ref, Cores: 2, MemoryMB: 2048, DiskGB: 20, Bridge: "vmbr0",
		IP: "10.0.0.5/24", Gateway: "10.0.0.1", User: "plumb",
		SSHKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOm me@laptop", "ssh-rsa AAAAB3Nza+/= other key"}}
}

func TestCreateObserveRoundTrip(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, err := d.FetchImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	spec := guest(ref)
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "tok-1"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "9001" {
		t.Errorf("id = %s", id)
	}
	clone := f.lastParams("POST /nodes/pve/qemu/9000/clone")
	if clone.Get("pool") != fakePool || clone.Get("full") != "1" || clone.Get("newid") != "9001" {
		t.Errorf("clone params: %v", clone)
	}
	vm := f.vms[9001]
	cfg := vm.cfg
	if cfg["cicustom"] != "vendor=plumb-snippets:snippets/plumb-vendor.yaml" || cfg["ipconfig0"] != "ip=10.0.0.5/24,gw=10.0.0.1" ||
		cfg["ciuser"] != "plumb" || cfg["net0"] != "virtio,bridge=vmbr0" || cfg["cores"] != "2" || cfg["memory"] != "2048" || !vm.running {
		t.Errorf("config: %v", cfg)
	}
	// The token is the SMBIOS serial, base64; the uuid of the clone stays.
	sm := parseProps(cfg["smbios1"])
	serial, _ := base64.StdEncoding.DecodeString(sm["serial"])
	if string(serial) != "tok-1" || sm["base64"] != "1" || sm["uuid"] == "" {
		t.Errorf("smbios1 = %s", cfg["smbios1"])
	}
	// SSH keys: URL-encoded with %20, then form-encoded by the client.
	if raw := f.lastParams("POST /nodes/pve/qemu/9001/config").Get("sshkeys"); !strings.Contains(raw, "%20") || strings.Contains(raw, "+") || strings.Contains(raw, " ") {
		t.Errorf("sshkeys param = %q", raw)
	}
	// The driver writes no file on the host: no upload, no snippet write.
	for _, l := range f.log {
		if strings.Contains(l, "upload") || strings.Contains(l, "snippets") && strings.HasPrefix(l, "POST") {
			t.Errorf("the driver wrote to the host: %s", l)
		}
	}

	// Observe gives back what Create set: Diff is empty.
	st, err := d.Observe(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Power != driver.PowerRunning || st.Digest == "" || len(st.IPs) != 1 || st.IPs[0] != "10.0.0.5" {
		t.Errorf("state: %+v", st)
	}
	if changes, err := d.Diff(spec, st); err != nil || len(changes) != 0 {
		t.Errorf("round trip: %+v %v\nobserved %+v", changes, err, st.Spec)
	}
	// A DHCP guest has no address without the guest agent (D-079).
	f.addVM(9050, fakePool, map[string]string{"name": "dhcp", "ipconfig0": "ip=dhcp", "memory": "current=4096"}, true)
	st2, err := d.Observe(ctx, "9050")
	if err != nil || len(st2.IPs) != 0 || st2.Spec.IP != "dhcp" || st2.Spec.MemoryMB != 4096 {
		t.Errorf("dhcp guest: %+v %v", st2, err)
	}
	// The digest changes with the config (drift detection, §6).
	vm.cfg["cores"] = "8"
	st3, _ := d.Observe(ctx, id)
	if st3.Digest == st.Digest || st3.Spec.Cores != 8 {
		t.Errorf("outside change: %+v", st3)
	}
}

func TestCreateCleanup(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	f.failNext["POST /nodes/pve/qemu/9001/status/start"] = "start failed: no memory"
	if _, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "t"}); err == nil {
		t.Fatal("want an error")
	}
	if _, ok := f.vms[9001]; ok {
		t.Error("a half-made guest stayed behind")
	}
	if _, err := d.Create(ctx, guest(ref), driver.Bootstrap{}); err == nil {
		t.Error("create without a token")
	}
	if _, err := d.Create(ctx, guest("9999"), driver.Bootstrap{Token: "t"}); err == nil {
		t.Error("create from a missing template")
	}
}

func TestUpdate(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	id, _ := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "tok-1"})
	vm := f.vms[9001]
	vm.cfg["net0"] = "virtio=BC:24:11:AA:BB:CC,bridge=vmbr0,firewall=1"
	vm.cfg["smbios1"] = vm.cfg["smbios1"] + ",manufacturer=" + base64.StdEncoding.EncodeToString([]byte("ACME"))
	uuidBefore := parseProps(vm.cfg["smbios1"])["uuid"]

	st, _ := d.Observe(ctx, id)
	want := guest(ref)
	want.Cores, want.MemoryMB, want.Bridge = 4, 4096, "vmbr1"
	changes, err := d.Diff(want, st)
	if err != nil || len(changes) != 3 {
		t.Fatalf("diff: %+v %v", changes, err)
	}
	if err := d.Update(ctx, id, changes, nil); err != nil {
		t.Fatal(err)
	}
	if vm.cfg["cores"] != "4" || vm.cfg["memory"] != "4096" || vm.cfg["net0"] != "virtio=BC:24:11:AA:BB:CC,bridge=vmbr1,firewall=1" {
		t.Errorf("after update: %v", vm.cfg)
	}

	// Re-enrollment: a new serial; the uuid and the other fields stay.
	token := []wire.Change{{Attr: driver.AttrEnrollToken, Sensitive: true, Risk: wire.RiskDisruptive}}
	if err := d.Update(ctx, id, token, &driver.Bootstrap{Token: "tok-2"}); err != nil {
		t.Fatal(err)
	}
	sm := parseProps(vm.cfg["smbios1"])
	serial, _ := base64.StdEncoding.DecodeString(sm["serial"])
	maker, _ := base64.StdEncoding.DecodeString(sm["manufacturer"])
	if string(serial) != "tok-2" || sm["uuid"] != uuidBefore || string(maker) != "ACME" {
		t.Errorf("smbios1 after re-enroll: %s", vm.cfg["smbios1"])
	}
	if err := d.Update(ctx, id, token, nil); err == nil {
		t.Error("token change without Bootstrap")
	}
	var co *driver.CreateOnlyError
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrDisk, To: 40}}, nil); !errors.As(err, &co) {
		t.Errorf("create-only: %v", err)
	}
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrBridge, To: "vmbr0,firewall=0"}}, nil); err == nil {
		t.Error("bridge injection")
	}
	if _, err := d.Diff(guest("1"), st); !errors.As(err, &co) {
		t.Errorf("image change: %v", err)
	}
}

func TestPowerAndDelete(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	id, _ := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "t"})
	starts := f.count("POST /nodes/pve/qemu/9001/status/start")
	if err := d.Power(ctx, id, driver.PowerStart); err != nil || f.count("POST /nodes/pve/qemu/9001/status/start") != starts {
		t.Errorf("start of a running guest: %v", err)
	}
	// Restart is a cold start: shutdown, then start; never a reboot.
	if err := d.Power(ctx, id, driver.PowerRestart); err != nil {
		t.Fatal(err)
	}
	if f.count("POST /nodes/pve/qemu/9001/status/shutdown") != 1 || f.count("POST /nodes/pve/qemu/9001/status/start") != starts+1 ||
		f.count("POST /nodes/pve/qemu/9001/status/reboot") != 0 {
		t.Errorf("restart calls: %v", f.log)
	}
	if p := f.lastParams("POST /nodes/pve/qemu/9001/status/shutdown"); p.Get("forceStop") != "1" || p.Get("timeout") == "" {
		t.Errorf("shutdown params: %v", p)
	}
	if err := d.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.vms[9001]; ok {
		t.Error("guest still there")
	}
	if p := f.lastParams("DELETE /nodes/pve/qemu/9001"); p.Get("purge") != "1" || p.Get("destroy-unreferenced-disks") != "1" {
		t.Errorf("delete params: %v", p)
	}
	if _, err := d.Observe(ctx, id); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("observe a deleted guest: %v", err)
	}
	if err := d.Delete(ctx, driver.GuestID(ref)); err == nil {
		t.Error("Delete removed a template")
	}
}

// The driver touches only guests of its pool and its VMID range.
func TestPoolGuard(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	f.addVM(9010, "other", map[string]string{"name": "foreign"}, true) // in range, other pool
	f.addVM(100, "", map[string]string{"name": "the-router"}, true)    // outside the range
	for _, id := range []driver.GuestID{"9010", "100", "abc", "-1"} {
		before := len(f.log)
		if _, err := d.Observe(ctx, id); err == nil {
			t.Errorf("observe %s: want an error", id)
		}
		if err := d.Delete(ctx, id); err == nil {
			t.Errorf("delete %s: want an error", id)
		}
		if err := d.Power(ctx, id, driver.PowerStop); err == nil {
			t.Errorf("stop %s: want an error", id)
		}
		if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrCores, To: 1}}, nil); err == nil {
			t.Errorf("update %s: want an error", id)
		}
		for _, l := range f.log[before:] {
			if strings.Contains(l, "/qemu/") {
				t.Errorf("%s: the driver called %s", id, l)
			}
		}
	}
	if _, err := d.Observe(ctx, "100"); !errors.Is(err, ErrForeignGuest) {
		t.Errorf("outside the range: %v", err)
	}
	if !f.vms[9010].running || !f.vms[100].running {
		t.Error("a foreign guest changed")
	}
	// A template of another pool is not an image of Plumb.
	f.addVM(9011, "other", map[string]string{"name": "tpl"}, false)
	f.vms[9011].template = true
	if _, err := d.Create(ctx, guest("9011"), driver.Bootstrap{Token: "t"}); err == nil {
		t.Error("create from a foreign template")
	}
	// nextID skips taken ids, also foreign ones.
	ref, _ := d.FetchImage(ctx, image)
	id, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "t"})
	if err != nil || id == "9010" || id == "9011" || string(id) == string(ref) {
		t.Errorf("new id %s: %v", id, err)
	}
}

// A concurrent change of the config (digest) makes Update fail instead of
// overwriting it.
func TestUpdateDigestGuard(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	f.addVM(9020, fakePool, map[string]string{"name": "g", "cores": "2", "memory": "1024"}, true)
	// Change the config between the read and the write of Update.
	f.mu.Lock()
	f.vms[9020].cfg["description"] = "x"
	f.mu.Unlock()
	err := d.c.Task(ctx, fakeNode, "POST", "/nodes/pve/qemu/9020/config", url.Values{"digest": {"stale"}, "cores": {"4"}})
	var ae *APIError
	if !errors.As(err, &ae) || !strings.Contains(ae.Message, "modified configuration") || f.vms[9020].cfg["cores"] != "2" {
		t.Errorf("stale digest: %v", err)
	}
}

func TestParsers(t *testing.T) {
	for in, want := range map[string]int{"20G": 20, "3584M": 3, "1T": 1024, "": 0, "x": 0} {
		if got := sizeGB(in); got != want {
			t.Errorf("sizeGB(%q) = %d", in, got)
		}
	}
	if got := replaceProp("virtio=AA,bridge=vmbr0,tag=5", "bridge", "vmbr2"); got != "virtio=AA,bridge=vmbr2,tag=5" {
		t.Errorf("replaceProp: %s", got)
	}
	// smbios keeps fields; plain old values become base64 once.
	got := parseProps(smbios("uuid=U,manufacturer=ACME", "tok"))
	maker, _ := base64.StdEncoding.DecodeString(got["manufacturer"])
	if got["uuid"] != "U" || string(maker) != "ACME" || got["base64"] != "1" {
		t.Errorf("smbios: %v", got)
	}
	if templateName(image) != "plumb-img-ubuntu-26.04-3f1c0a9e5d6b" {
		t.Error(templateName(image))
	}
}

// Test host (T-012): the pool member list says template 0 for a few seconds
// after the conversion. The driver reads the flag from the config, so a
// fresh template is used, never removed as half-made, and Delete refuses it.
func TestStaleTemplateFlagInPoolList(t *testing.T) {
	f := newFakePVE(t)
	f.staleTemplate = true
	d := f.driver(t)
	ctx := context.Background()
	ref, err := d.FetchImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "tok-1"}); err != nil {
		t.Fatalf("create from a fresh template: %v", err)
	}
	again, err := d.FetchImage(ctx, image)
	if err != nil || again != ref {
		t.Fatalf("second FetchImage = %s %v, want %s", again, err, ref)
	}
	if n := f.count("DELETE /nodes/pve/qemu/" + string(ref)); n != 0 {
		t.Fatalf("the fresh template was removed as half-made (%d deletes)", n)
	}
	if err := d.Delete(ctx, driver.GuestID(ref)); err == nil || !strings.Contains(err.Error(), "is a template") {
		t.Fatalf("Delete of the template: %v", err)
	}
}

// D-185: a bridge change writes only net0 (with the old MAC) and the
// digest; Update never writes a cloud-init input.
func TestUpdateWritesNoCloudInitInput(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	id, _ := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "tok-1"})
	f.vms[9001].cfg["net0"] = "virtio=BC:24:11:AA:BB:CC,bridge=vmbr0"
	st, _ := d.Observe(ctx, id)
	want := guest(ref)
	want.Bridge = "vmbr1"
	changes, err := d.Diff(want, st)
	if err != nil || len(changes) != 1 {
		t.Fatalf("diff: %+v %v", changes, err)
	}
	if err := d.Update(ctx, id, changes, nil); err != nil {
		t.Fatal(err)
	}
	p := f.lastParams("POST /nodes/pve/qemu/9001/config")
	var keys []string
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "digest,net0" || p.Get("net0") != "virtio=BC:24:11:AA:BB:CC,bridge=vmbr1" {
		t.Errorf("bridge update sent %v", p)
	}
	// Every create-only attribute is refused before any write.
	for _, attr := range driver.CreateOnly {
		if err := d.Update(ctx, id, []wire.Change{{Attr: attr, To: "x"}}, nil); err == nil {
			t.Errorf("Update accepted create-only %s", attr)
		}
	}
}

func TestCheckUpdateParams(t *testing.T) {
	const old = "virtio=BC:24:11:AA:BB:CC,bridge=vmbr0,firewall=1"
	for name, tc := range map[string]struct {
		params url.Values
		ok     bool
	}{
		"cores, memory, smbios1": {url.Values{"digest": {"d"}, "cores": {"2"}, "memory": {"2048"}, "smbios1": {"uuid=u"}}, true},
		"bridge, same MAC":       {url.Values{"net0": {"virtio=bc:24:11:aa:bb:cc,bridge=vmbr1,firewall=1"}}, true},
		"searchdomain":           {url.Values{"searchdomain": {"x"}}, false},
		"sshkeys":                {url.Values{"sshkeys": {"x"}}, false},
		"ipconfig0":              {url.Values{"ipconfig0": {"ip=dhcp"}}, false},
		"ciupgrade":              {url.Values{"ciupgrade": {"0"}}, false},
		"cicustom":               {url.Values{"cicustom": {"vendor=x"}}, false},
		"name":                   {url.Values{"name": {"x"}}, false},
		"net0 without MAC":       {url.Values{"net0": {"virtio,bridge=vmbr1"}}, false},
		"net0 with a new MAC":    {url.Values{"net0": {"virtio=BC:24:11:00:00:01,bridge=vmbr1"}}, false},
	} {
		if err := checkUpdateParams(old, tc.params); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := checkUpdateParams("virtio,bridge=vmbr0", url.Values{"net0": {"virtio,bridge=vmbr1"}}); err == nil {
		t.Error("a net0 without a MAC was changed")
	}
}

// D-186: Create sets ciupgrade=0, so a new VM upgrades no package at its
// first boot.
func TestCreateSetsNoUpgrade(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	if _, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "tok-1"}); err != nil {
		t.Fatal(err)
	}
	if got := f.lastParams("POST /nodes/pve/qemu/9001/config").Get("ciupgrade"); got != "0" {
		t.Errorf("ciupgrade = %q, want 0", got)
	}
}
