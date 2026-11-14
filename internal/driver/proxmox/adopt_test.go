package proxmox

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

const (
	digestOK   = "3f1c0d5e8a9b7c6d5e4f3a2b1c0d9e8f7a6b5c4d"
	secretMark = "S3CRET-MARK"
)

func vmCfg(extra map[string]string) map[string]string {
	c := map[string]string{
		"name": "wiki", "cores": "2", "memory": "2048", "digest": digestOK,
		"scsi0": "local-lvm:vm-9123-disk-0,size=20G", "net0": "virtio=BC:24:11:00:00:01,bridge=vmbr0",
		"ide2": "local-lvm:vm-9123-cloudinit,media=cdrom", "ipconfig0": "ip=192.0.2.20/24,gw=192.0.2.1",
		"ciuser": "admin",
	}
	for k, v := range extra {
		if v == "" {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

func ctCfg(extra map[string]string) map[string]string {
	c := map[string]string{
		"hostname": "old-db", "cores": "1", "memory": "512", "digest": digestOK, "unprivileged": "1",
		"rootfs": "local-lvm:vm-9124-disk-0,size=8G", "net0": "name=eth0,bridge=vmbr0,ip=dhcp",
	}
	for k, v := range extra {
		if v == "" {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

func TestInspectLayouts(t *testing.T) {
	for _, c := range []struct {
		name    string
		kind    wire.Kind
		cfg     map[string]string
		reasons []string
	}{
		{"vm ok", wire.KindVM, vmCfg(nil), nil},
		{"vm second disk", wire.KindVM, vmCfg(map[string]string{"scsi1": "local-lvm:x,size=1G"}), []string{driver.AdoptDiskLayout}},
		{"vm virtio disk", wire.KindVM, vmCfg(map[string]string{"scsi0": "", "virtio0": "local-lvm:x,size=20G"}), []string{driver.AdoptDiskLayout}},
		{"vm no disk", wire.KindVM, vmCfg(map[string]string{"scsi0": ""}), []string{driver.AdoptDiskLayout}},
		{"vm efi disk", wire.KindVM, vmCfg(map[string]string{"efidisk0": "local-lvm:x,size=4M"}), []string{driver.AdoptDiskLayout}},
		{"vm unused disk", wire.KindVM, vmCfg(map[string]string{"unused0": "local-lvm:y"}), []string{driver.AdoptDiskLayout}},
		{"vm other storage", wire.KindVM, vmCfg(map[string]string{"scsi0": "nas:9123/vm-9123-disk-0.qcow2,size=20G"}), []string{driver.AdoptDiskStorage}},
		{"vm cdrom with iso", wire.KindVM, vmCfg(map[string]string{"ide0": "local:iso/x.iso,media=cdrom"}), nil},
		{"vm second nic", wire.KindVM, vmCfg(map[string]string{"net1": "virtio,bridge=vmbr0"}), []string{driver.AdoptNICLayout}},
		{"vm no nic", wire.KindVM, vmCfg(map[string]string{"net0": ""}), []string{driver.AdoptNICLayout}},
		{"vm other bridge", wire.KindVM, vmCfg(map[string]string{"net0": "virtio,bridge=vmbr1"}), []string{driver.AdoptBridge}},
		{"vm vlan tag", wire.KindVM, vmCfg(map[string]string{"net0": "virtio,bridge=vmbr0,tag=20"}), []string{driver.AdoptVLAN}},
		{"vm pci", wire.KindVM, vmCfg(map[string]string{"hostpci0": "0000:01:00"}), []string{driver.AdoptHostDevice}},
		{"vm usb", wire.KindVM, vmCfg(map[string]string{"usb0": "host=1-1"}), []string{driver.AdoptHostDevice}},
		{"vm template", wire.KindVM, vmCfg(map[string]string{"template": "1"}), []string{driver.AdoptTemplate}},
		{"vm bad name", wire.KindVM, vmCfg(map[string]string{"name": "Wiki_Old"}), []string{driver.AdoptName}},
		{"ct ok", wire.KindLXC, ctCfg(nil), nil},
		{"ct privileged", wire.KindLXC, ctCfg(map[string]string{"unprivileged": ""}), []string{driver.AdoptPrivileged}},
		{"ct mount", wire.KindLXC, ctCfg(map[string]string{"mp0": "/srv/data,mp=/data"}), []string{driver.AdoptDiskLayout}},
		{"ct device", wire.KindLXC, ctCfg(map[string]string{"dev0": "/dev/ttyUSB0"}), []string{driver.AdoptHostDevice}},
		{"ct raw lxc", wire.KindLXC, ctCfg(map[string]string{"lxc": "lxc.mount.entry: /x y"}), []string{driver.AdoptHostDevice}},
		{"ct other storage", wire.KindLXC, ctCfg(map[string]string{"rootfs": "nas:subvol-9124-disk-0,size=8G"}), []string{driver.AdoptDiskStorage}},
	} {
		t.Run(c.name, func(t *testing.T) {
			u := inspect(c.kind, maps.Clone(c.cfg), "local-lvm", "vmbr0")
			want := c.reasons
			if want == nil {
				want = []string{}
			}
			if !slices.Equal(u.Reasons, want) {
				t.Fatalf("reasons %v, want %v", u.Reasons, want)
			}
			if u.Adoptable() != (len(want) == 0) || u.Warnings[0] != driver.WarnNotSealed || u.Digest != digestOK {
				t.Fatalf("%+v", u)
			}
		})
	}
}

func TestInspectGuestFields(t *testing.T) {
	u := inspect(wire.KindVM, vmCfg(nil), "local-lvm", "vmbr0")
	want := map[string]any{"cores": 2, "memory": 2048, "disk": 20, "bridge": "vmbr0", "ip": "192.0.2.20/24", "gateway": "192.0.2.1", "user": "admin"}
	if fmt.Sprint(u.Guest) != fmt.Sprint(want) || u.Name != "wiki" {
		t.Fatalf("%v", u.Guest)
	}
	// No cloud-init: no ip, gateway or user, and the warning.
	u = inspect(wire.KindVM, vmCfg(map[string]string{"ipconfig0": "", "ciuser": "", "ide2": ""}), "local-lvm", "vmbr0")
	if _, ok := u.Guest["ip"]; ok || !slices.Contains(u.Warnings, driver.WarnNoCloudInit) {
		t.Fatalf("%v %v", u.Guest, u.Warnings)
	}
	u = inspect(wire.KindLXC, ctCfg(map[string]string{"features": "nesting=1,keyctl=1"}), "local-lvm", "vmbr0")
	if u.Guest["ip"] != "dhcp" || slices.Contains(u.Reasons, driver.AdoptPrivileged) || !slices.Contains(u.Warnings, driver.WarnNesting) || !slices.Contains(u.Warnings, driver.WarnKeyctl) {
		t.Fatalf("%+v", u)
	}
	if _, ok := u.Guest["user"]; ok {
		t.Fatal("an LXC has no bootstrap user")
	}
}

// ARCH §19: cipassword, sshkeys, description and an LXC env are never
// stored or shown: nothing that InspectGuest returns holds their values,
// and the config map no longer holds them after the inspect.
func TestInspectRedactsSecrets(t *testing.T) {
	for _, kind := range []wire.Kind{wire.KindVM, wire.KindLXC} {
		cfg := vmCfg(nil)
		if kind == wire.KindLXC {
			cfg = ctCfg(nil)
		}
		cfg["cipassword"] = "$5$" + secretMark
		cfg["sshkeys"] = "ssh-ed25519%20AAAA" + secretMark
		cfg["description"] = "plumb-image: x\nroot pw " + secretMark + "\nplumb-ssh-key: ssh-ed25519 " + secretMark
		cfg["env"] = "DB_PASSWORD=" + secretMark
		u := inspect(kind, cfg, "local-lvm", "vmbr0")
		b, _ := json.Marshal(u)
		if strings.Contains(string(b), secretMark) || strings.Contains(fmt.Sprintf("%#v", u), secretMark) {
			t.Fatalf("%s: a secret value is in the result: %s", kind, b)
		}
		for k, v := range cfg {
			if strings.Contains(v, secretMark) {
				t.Fatalf("%s: %s still holds the secret after the inspect", kind, k)
			}
		}
		if !u.Present.CIPassword || !u.Present.SSHKeys || !u.Present.Description || !u.Present.Env {
			t.Fatalf("%s: present %+v", kind, u.Present)
		}
	}
}

// Review of T-193 item 2 (marked for pl-reviewer): own() accepts a VMID
// outside the Plumb range only when the engine passed it as the guest of a
// bound adopted Target, and at every call only while the guest is in the
// pool and has the Target's name.
func TestOwnAdopted(t *testing.T) {
	ctx := t.Context()
	f := newFakePVE(t)
	d := f.driver(t)
	const outside = 150 // the range of the fake driver is 9000–9099
	f.addVM(outside, fakePool, map[string]string{"name": "wiki", "scsi0": "local-lvm:vm-150-disk-0,size=4G", "net0": "virtio,bridge=vmbr0"}, true)
	gid := driver.GuestID("150")

	// Not named by the engine: foreign, as before.
	if _, err := d.Observe(ctx, gid); !errors.Is(err, ErrForeignGuest) {
		t.Fatalf("not adopted: %v", err)
	}
	d.SetAdopted(map[driver.GuestID]string{gid: "wiki"})
	if _, err := d.Observe(ctx, gid); err != nil {
		t.Fatalf("adopted: %v", err)
	}

	// Renamed in Proxmox: refused, nothing written.
	f.mu.Lock()
	f.vms[outside].cfg["name"] = "wiki-old"
	f.mu.Unlock()
	writes := f.count("POST ") + f.count("PUT ") + f.count("DELETE ")
	if _, err := d.Observe(ctx, gid); !errors.Is(err, ErrForeignGuest) {
		t.Fatalf("renamed: %v", err)
	}
	if err := d.Delete(ctx, gid); !errors.Is(err, ErrForeignGuest) {
		t.Fatalf("delete of a renamed guest: %v", err)
	}
	if n := f.count("POST ") + f.count("PUT ") + f.count("DELETE "); n != writes {
		t.Fatalf("a write to a renamed guest: %d", n-writes)
	}

	// VMID reused: the old guest is gone and another guest (another name)
	// has the VMID now.
	f.mu.Lock()
	delete(f.vms, outside)
	f.mu.Unlock()
	f.addVM(outside, fakePool, map[string]string{"name": "someone-else"}, true)
	if _, err := d.Observe(ctx, gid); !errors.Is(err, ErrForeignGuest) {
		t.Fatalf("reused VMID: %v", err)
	}

	// Left the pool (with its name): not found, never touched.
	f.mu.Lock()
	f.vms[outside].cfg["name"] = "wiki"
	f.vms[outside].pool = ""
	f.mu.Unlock()
	if _, err := d.Observe(ctx, gid); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("left the pool: %v", err)
	}

	// The engine drops the Target: foreign again.
	f.mu.Lock()
	f.vms[outside].pool = fakePool
	f.mu.Unlock()
	d.SetAdopted(nil)
	if _, err := d.Observe(ctx, gid); !errors.Is(err, ErrForeignGuest) {
		t.Fatalf("after SetAdopted(nil): %v", err)
	}

	// An adopted guest inside the range gets the name check too.
	f.addVM(9050, fakePool, map[string]string{"name": "other"}, true)
	d.SetAdopted(map[driver.GuestID]string{"9050": "inside"})
	if _, err := d.Observe(ctx, "9050"); !errors.Is(err, ErrForeignGuest) {
		t.Fatalf("inside the range, wrong name: %v", err)
	}
}

// InspectGuest reads a pool guest outside the range (it only reads), and
// refuses a guest outside the pool.
func TestInspectGuestPool(t *testing.T) {
	ctx := t.Context()
	f := newFakePVE(t)
	d := f.driver(t)
	f.addVM(150, fakePool, map[string]string{"name": "wiki", "cores": "2", "memory": "1024", "scsi0": "local-lvm:vm-150-disk-0,size=4G", "net0": "virtio,bridge=vmbr0", "cipassword": secretMark}, true)
	f.addVM(151, "other", map[string]string{"name": "x"}, false)
	u, err := d.InspectGuest(ctx, "150")
	if err != nil || !u.Adoptable() || u.Name != "wiki" || u.Power != driver.PowerRunning || len(u.Digest) != 40 || !u.Present.CIPassword {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := d.InspectGuest(ctx, "151"); !errors.Is(err, driver.ErrNotInPool) {
		t.Fatalf("other pool: %v", err)
	}
	writes := f.count("POST ") + f.count("PUT ") + f.count("DELETE ")
	if writes != 0 {
		t.Fatalf("InspectGuest wrote: %d", writes)
	}
}
