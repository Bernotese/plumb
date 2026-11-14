package proxmox

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The install settings of slice 7 (D-401, D-405, D-406): the extra bridges
// and bridge/tag paths, and the guest backup storage, that install.sh named
// (--extra-bridge, --extra-vlan, --guest-backup-storage,
// --guest-backup-clear). install.sh writes them to plumb.env
// (env_slice7_lines); every run writes all three, so a repair run without
// the flags clears them. Each extra net is one PlumbBridge ACL line with
// propagate 0; the guest backup storage gives the lines PlumbGuestBackupVM
// and PlumbGuestBackup.

// The env names of the install settings of slice 7.
const (
	EnvExtraNets          = "PLUMB_PVE_EXTRA_NETS"
	EnvGuestBackupStorage = "PLUMB_PVE_GUEST_BACKUP_STORAGE"
	EnvGuestBackupClear   = "PLUMB_PVE_GUEST_BACKUP_CLEAR"
)

// ExtraNet is one network that the install named: a bridge for untagged
// NICs (Tag 0), or one VLAN tag on a bridge.
type ExtraNet struct {
	Bridge string
	Tag    int // 1 … 4094; 0: untagged
}

// ACLPath is the SDN path of its PlumbBridge line. PVE checks only the
// tag path for a tagged NIC (T-214), so a tag needs no line on the bridge.
func (n ExtraNet) ACLPath() string {
	if n.Tag == 0 {
		return "/sdn/zones/localnetwork/" + n.Bridge
	}
	return fmt.Sprintf("/sdn/zones/localnetwork/%s/%d", n.Bridge, n.Tag)
}

// InstallExtras are the install settings of slice 7.
type InstallExtras struct {
	// ExtraNets are the networks of --extra-bridge and --extra-vlan, in
	// the order of plumb.env, without duplicates.
	ExtraNets []ExtraNet
	// GuestBackupStorage is the storage of --guest-backup-storage; empty:
	// guest backups are off.
	GuestBackupStorage string
	// GuestBackupClear: --guest-backup-clear, backups in clear on a
	// storage without client-side encryption are wanted (D-406).
	GuestBackupClear bool
}

// NetAllowed reports whether the install named the bridge (tag 0) or the
// bridge/tag as an extra net. The main bridge of the install (Config.Bridge,
// untagged) is not in this list.
func (x InstallExtras) NetAllowed(bridge string, tag int) bool {
	for _, n := range x.ExtraNets {
		if n.Bridge == bridge && n.Tag == tag {
			return true
		}
	}
	return false
}

var (
	reExtraBridge = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,14}$`)
	reExtraTag    = regexp.MustCompile(`^[1-9][0-9]{0,3}$`)
	reStorageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ParseExtraNets reads the value of PLUMB_PVE_EXTRA_NETS: a comma list of
// "bridge" or "bridge/tag". Empty is no extra net. A duplicate is an
// error: install.sh never writes one.
func ParseExtraNets(v string) ([]ExtraNet, error) {
	if v == "" {
		return nil, nil
	}
	var out []ExtraNet
	seen := map[ExtraNet]bool{}
	for _, item := range strings.Split(v, ",") {
		bridge, tagText, tagged := strings.Cut(item, "/")
		n := ExtraNet{Bridge: bridge}
		if !reExtraBridge.MatchString(bridge) {
			return nil, fmt.Errorf("bad bridge %s", strconv.Quote(cut(bridge)))
		}
		if tagged {
			t, err := strconv.Atoi(tagText)
			if !reExtraTag.MatchString(tagText) || err != nil || t > 4094 {
				return nil, fmt.Errorf("bad VLAN tag %s on bridge %s (1 … 4094)", strconv.Quote(cut(tagText)), bridge)
			}
			n.Tag = t
		}
		if seen[n] {
			return nil, fmt.Errorf("%s is named twice", item)
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
}

// cut shortens a bad value for a message.
func cut(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// InstallExtrasFromEnv reads the install settings of slice 7. All three
// may be missing (an install before T-218). The error names every bad
// variable; the values hold no secret.
func InstallExtrasFromEnv(getenv func(string) string) (InstallExtras, error) {
	var x InstallExtras
	var bad []string
	nets, err := ParseExtraNets(getenv(EnvExtraNets))
	if err != nil {
		bad = append(bad, fmt.Sprintf("%s must be a comma list of bridge or bridge/tag (install.sh --extra-bridge, --extra-vlan): %v", EnvExtraNets, err))
	}
	x.ExtraNets = nets
	if v := getenv(EnvGuestBackupStorage); v != "" && !reStorageName.MatchString(v) {
		bad = append(bad, fmt.Sprintf("%s must be a Proxmox storage name, not %s", EnvGuestBackupStorage, strconv.Quote(cut(v))))
	} else {
		x.GuestBackupStorage = v
	}
	switch v := getenv(EnvGuestBackupClear); v {
	case "", "0":
	case "1":
		x.GuestBackupClear = true
		if x.GuestBackupStorage == "" {
			bad = append(bad, fmt.Sprintf("%s=1 needs %s", EnvGuestBackupClear, EnvGuestBackupStorage))
		}
	default:
		bad = append(bad, fmt.Sprintf("%s must be 0 or 1, not %s", EnvGuestBackupClear, strconv.Quote(cut(v))))
	}
	if len(bad) > 0 {
		return InstallExtras{}, fmt.Errorf("%s", strings.Join(bad, "; "))
	}
	return x, nil
}

// Guest backup storage states of the settings view (D-406).
const (
	GuestBackupPBSKey       = "pbs-key"
	GuestBackupStatedClear  = "stated-clear"
	GuestBackupClearRefused = "clear-refused"
)

// GuestBackupView is the guest backup part of the settings view.
type GuestBackupView struct {
	Storage string
	State   string // GuestBackupPBSKey, GuestBackupStatedClear, GuestBackupClearRefused
	// Others are the other storages with a Plumb Datastore.AllocateSpace
	// right whose content holds "backup" (M2 of #321): the token could
	// write a vzdump there. It must be empty; install.sh refuses otherwise.
	Others []string
}

// guestBackupView builds the view from the GET /storage index (every
// storage on which the token has Datastore.Audit or AllocateSpace, with its
// config; FACT 2 of T-214) and the token's permissions. Encrypted is
// type "pbs" with "encryption-key" set: the key stays in /etc/pve/priv.
func guestBackupView(x InstallExtras, storages []map[string]any, perms map[string]map[string]int) *GuestBackupView {
	if x.GuestBackupStorage == "" {
		return nil
	}
	v := &GuestBackupView{Storage: x.GuestBackupStorage, State: GuestBackupClearRefused, Others: []string{}}
	if x.GuestBackupClear {
		v.State = GuestBackupStatedClear
	}
	for _, s := range storages {
		id, _ := s["storage"].(string)
		if id == "" {
			continue
		}
		if id == x.GuestBackupStorage {
			if typ, _ := s["type"].(string); typ == "pbs" && setValue(s["encryption-key"]) {
				v.State = GuestBackupPBSKey
			}
			continue
		}
		content, _ := s["content"].(string)
		if hasContent(content, "backup") && hasRight(perms, "/storage/"+id, "Datastore.AllocateSpace") {
			v.Others = append(v.Others, id)
		}
	}
	return v
}

// setValue reports a set config value: a non-empty string, or a number or
// bool that is not zero (PVE gives "1" or a fingerprint).
func setValue(v any) bool {
	switch t := v.(type) {
	case string:
		return t != "" && t != "0"
	case float64:
		return t != 0
	case bool:
		return t
	}
	return false
}

func hasContent(list, c string) bool {
	for _, x := range strings.Split(list, ",") {
		if strings.TrimSpace(x) == c {
			return true
		}
	}
	return false
}
