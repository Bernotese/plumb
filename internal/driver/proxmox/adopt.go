package proxmox

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// Adoption (D-386, ARCHITECTURE §19): InspectGuest reads one pool guest for
// GET /guests/unmanaged and the adopt Plan. It only reads: the pool list,
// the config (with its digest) and the power state. The VMID range of
// Plumb does not apply: an adopted guest was made by a person.

// secretKeys are config keys whose values never leave InspectGuest (D-340,
// review R3 of #290): only their presence is kept.
var secretKeys = []string{"cipassword", "sshkeys", "description", "env"}

var (
	reDocName  = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$`)
	reVMDisk   = regexp.MustCompile(`^(scsi|virtio|sata|ide)[0-9]+$`)
	reVMOther  = regexp.MustCompile(`^(efidisk|tpmstate|unused)[0-9]+$`)
	reNIC      = regexp.MustCompile(`^net[0-9]+$`)
	reHostDev  = regexp.MustCompile(`^(hostpci|usb)[0-9]+$`)
	reDocDigit = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// InspectGuest implements driver.Adopter.
func (d *Driver) InspectGuest(ctx context.Context, gid driver.GuestID) (driver.Unmanaged, error) {
	id, err := vmid(string(gid))
	if err != nil {
		return driver.Unmanaged{}, err
	}
	ms, err := d.guests(ctx)
	if err != nil {
		return driver.Unmanaged{}, err
	}
	i := slices.IndexFunc(ms, func(m member) bool { return m.VMID == id })
	if i < 0 {
		return driver.Unmanaged{}, fmt.Errorf("VMID %d: %w", id, driver.ErrNotInPool)
	}
	kind, path := wire.KindVM, vmPath(d, id)
	if ms[i].Type == typeCT {
		kind, path = wire.KindLXC, ctPath(d, id)
	}
	cfg, err := d.readPath(ctx, path, false)
	if err != nil {
		return driver.Unmanaged{}, err
	}
	u := inspect(kind, cfg, d.cfg.DiskStorage, d.cfg.Bridge)
	u.ID, u.Node = gid, d.cfg.Node
	var st struct {
		Status string `json:"status"`
	}
	if err := d.c.Get(ctx, path+"/status/current", nil, &st); err == nil {
		switch st.Status {
		case "running":
			u.Power = driver.PowerRunning
		case "stopped":
			u.Power = driver.PowerStopped
		}
	}
	return u, nil
}

// inspect classifies one config. It drops the secret keys first, so
// nothing below (and nothing the caller gets) holds their values.
func inspect(kind wire.Kind, cfg map[string]string, diskStorage, bridge string) driver.Unmanaged {
	var u driver.Unmanaged
	u.Kind = kind
	for _, k := range secretKeys {
		v, ok := cfg[k]
		delete(cfg, k)
		if !ok || v == "" {
			continue
		}
		switch k {
		case "cipassword":
			u.Present.CIPassword = true
		case "sshkeys":
			u.Present.SSHKeys = true
		case "description":
			u.Present.Description = true
		case "env":
			u.Present.Env = true
		}
	}
	if d := cfg["digest"]; reDocDigit.MatchString(d) {
		u.Digest = d
	}
	reason := func(r string) {
		if !slices.Contains(u.Reasons, r) {
			u.Reasons = append(u.Reasons, r)
		}
	}
	if cfg["template"] == "1" {
		reason(driver.AdoptTemplate)
	}
	nics := 0
	for k, v := range cfg {
		switch {
		case reNIC.MatchString(k):
			nics++
			if k != "net0" {
				reason(driver.AdoptNICLayout)
			}
			p := parseProps(v)
			if p["bridge"] != bridge {
				reason(driver.AdoptBridge)
			}
			if p["tag"] != "" || p["trunks"] != "" {
				reason(driver.AdoptVLAN)
			}
		case reHostDev.MatchString(k):
			reason(driver.AdoptHostDevice)
		}
	}
	if nics != 1 {
		reason(driver.AdoptNICLayout)
	}
	var spec driver.GuestSpec
	if kind == wire.KindLXC {
		u.Name = cfg["hostname"]
		for k := range cfg {
			switch {
			case reMountKey.MatchString(k):
				reason(driver.AdoptDiskLayout)
			case reDeviceKey.MatchString(k), k == "lxc" && cfg[k] != "":
				reason(driver.AdoptHostDevice)
			}
		}
		if cfg["unprivileged"] != "1" {
			reason(driver.AdoptPrivileged)
		}
		if storageOf(cfg["rootfs"]) != diskStorage {
			reason(driver.AdoptDiskStorage)
		}
		feats := parseProps(cfg["features"])
		if feats["nesting"] == "1" {
			u.Warnings = append(u.Warnings, driver.WarnNesting)
		}
		if feats["keyctl"] == "1" {
			u.Warnings = append(u.Warnings, driver.WarnKeyctl)
		}
		spec = specOfCT(cfg)
	} else {
		u.Name = cfg["name"]
		disks := 0
		for k, v := range cfg {
			switch {
			case reVMOther.MatchString(k):
				reason(driver.AdoptDiskLayout)
			case reVMDisk.MatchString(k):
				p := parseProps(v)
				if p["media"] == "cdrom" || strings.Contains(v, "cloudinit") {
					continue // a CD-ROM or the cloud-init drive is no disk
				}
				disks++
				if k != "scsi0" {
					reason(driver.AdoptDiskLayout)
				} else if storageOf(v) != diskStorage {
					reason(driver.AdoptDiskStorage)
				}
			}
		}
		if disks != 1 {
			reason(driver.AdoptDiskLayout)
		}
		if cfg["ipconfig0"] == "" {
			u.Warnings = append(u.Warnings, driver.WarnNoCloudInit)
		}
		spec = specOf(u.Name, cfg)
	}
	if !reDocName.MatchString(u.Name) {
		reason(driver.AdoptName)
	}
	u.Warnings = append([]string{driver.WarnNotSealed}, u.Warnings...)
	u.Guest = guestFields(kind, spec)
	slices.Sort(u.Reasons)
	if u.Reasons == nil {
		u.Reasons = []string{}
	}
	return u
}

// storageOf is the storage of a disk property ("local-lvm:vm-9123-disk-0,size=20G").
func storageOf(v string) string {
	s, _, _ := strings.Cut(parseProps(v)[""], ":")
	return s
}

// guestFields are the Target guest fields of an adoption (D-386): never
// ssh_keys, and ip, gateway, user only when Proxmox has them.
func guestFields(kind wire.Kind, s driver.GuestSpec) map[string]any {
	g := map[string]any{"cores": s.Cores, "memory": s.MemoryMB, "disk": s.DiskGB, "bridge": s.Bridge}
	if s.IP != "" {
		g["ip"] = s.IP
	}
	if s.Gateway != "" {
		g["gateway"] = s.Gateway
	}
	if kind == wire.KindVM && s.User != "" {
		g["user"] = s.User
	}
	return g
}

var _ driver.Adopter = (*Driver)(nil)

// SetAdopted implements driver.Adopter: the guests of bound adopted
// Targets, VMID → Target name. It replaces the whole set.
func (d *Driver) SetAdopted(refs map[driver.GuestID]string) {
	m := make(map[int]string, len(refs))
	for ref, name := range refs {
		if id, err := vmid(string(ref)); err == nil {
			m[id] = name
		}
	}
	d.adoptMu.Lock()
	d.adopted = m
	d.adoptMu.Unlock()
}

func (d *Driver) adoptedName(id int) (string, bool) {
	d.adoptMu.Lock()
	defer d.adoptMu.Unlock()
	name, ok := d.adopted[id]
	return name, ok
}

// checkAdopted refuses an adopted VMID whose guest no longer has the
// Target's name: the guest was renamed, or the VMID now holds another
// guest (VMID reuse, D-320). Plumb then touches it no more.
func checkAdopted(id int, name, want string, adopted bool) error {
	if adopted && name != want {
		return fmt.Errorf("%w: VMID %d is named %q, not the adopted Target %q", ErrForeignGuest, id, name, want)
	}
	return nil
}
