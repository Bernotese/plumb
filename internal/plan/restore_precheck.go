package plan

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// T-241 (T-244 part 3): PVE refuses a restore whose backup has a NIC on a
// bridge or VLAN tag that the install does not name (no SDN.Use), and the
// safety backup of such a guest cannot be restored either while the right
// is missing (R4). Plumb checks it before the guest is stopped and before
// anything is revoked, so the refusal changes nothing.

// BackupConfigReader is the optional part of GuestRestorer that reads the
// guest config inside a backup volume of the guest ref (vzdump/extractconfig,
// T-244 R2): the keys of the current config. T-241 reads the networks from
// it, T-238 the data disks. Without it there is no pre-check; PVE's own
// refusal stays the guard.
type BackupConfigReader interface {
	BackupConfig(ctx context.Context, ref string, v RestoreVolume) (map[string]string, error)
}

// BackupNIC is one NIC of a backup's guest config.
type BackupNIC struct {
	NIC    int
	Bridge string // "" when the NIC names none
	Tag    int    // 0: untagged
	Trunks []int  // the VLANs of trunks= (PVE checks SDN.Use for each)
}

var reNetKey = regexp.MustCompile(`^net([0-9]{1,2})$`)

// backupNICs reads the netN keys of a guest config: a VM value is
// "virtio=<mac>,bridge=vmbr0,tag=20,…", a container value
// "name=eth0,bridge=vmbr0,hwaddr=…,tag=20,…". In NIC order.
func backupNICs(conf map[string]string) ([]BackupNIC, error) {
	var out []BackupNIC
	for k, val := range conf {
		m := reNetKey.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		nic := BackupNIC{NIC: n}
		for _, kv := range strings.Split(val, ",") {
			key, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			switch key {
			case "bridge":
				nic.Bridge = v
			case "tag":
				t, err := strconv.Atoi(v)
				if err != nil || t < 1 || t > 4094 {
					return nil, fmt.Errorf("net%d has a bad tag %q", n, v)
				}
				nic.Tag = t
			case "trunks":
				ts, err := parseTrunks(v)
				if err != nil {
					return nil, fmt.Errorf("net%d has bad trunks %q", n, v)
				}
				nic.Trunks = ts
			}
		}
		out = append(out, nic)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NIC < out[j].NIC })
	return out, nil
}

// parseTrunks reads PVE's trunks value: VLAN ids and ranges split by ";"
// ("20;30;40-42").
func parseTrunks(v string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(v, ";") {
		lo, hi, isRange := strings.Cut(p, "-")
		a, err := strconv.Atoi(lo)
		if err != nil || a < 1 || a > 4094 {
			return nil, fmt.Errorf("bad VLAN %q", p)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil || b < a || b > 4094 {
				return nil, fmt.Errorf("bad VLAN range %q", p)
			}
		}
		for t := a; t <= b; t++ {
			out = append(out, t)
		}
	}
	return out, nil
}

// checkBackupNetworks refuses the restore of v when a NIC of the backup is
// on a network that the install does not name (its bridge with its tag,
// and with each trunk VLAN). A config that cannot be read is no refusal
// (PVE guards it; the error is logged), but a config that Plumb reads and
// cannot parse is one (L1 of #369): the restore stops before the guest is
// stopped.
func (e *Engine) checkBackupNetworks(ctx context.Context, target, ref string, v RestoreVolume) error {
	rd, ok := e.cfg.GuestRestore.(BackupConfigReader)
	if !ok {
		return nil
	}
	conf, err := rd.BackupConfig(ctx, ref, v)
	if err != nil {
		e.log.Warn("restore pre-check: the backup's config cannot be read; PVE checks the networks itself", "target", target, "backup", v.ID, "err", err)
		return nil
	}
	nics, err := backupNICs(conf)
	if err != nil {
		return notRestorable(target, fmt.Sprintf("the network config of backup %s cannot be checked (%v); Plumb does not restore it. Nothing was changed", v.ID, err))
	}
	for _, n := range nics {
		if n.Bridge == "" {
			continue
		}
		for _, tag := range append([]int{n.Tag}, n.Trunks...) {
			if e.netAllowed(n.Bridge, tag) {
				continue
			}
			what, flag := e.netFlags(n.Bridge, tag)
			return notRestorable(target, fmt.Sprintf("NIC %d of backup %s is on %s, which the install does not name; Proxmox would refuse this restore. "+
				"To restore it, %s; or restore another backup. Nothing was changed", n.NIC, v.ID, what, repairHint(flag)))
		}
	}
	return nil
}
