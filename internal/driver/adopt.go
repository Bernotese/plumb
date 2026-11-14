package driver

import (
	"context"
	"errors"

	"github.com/Bernotese/plumb/internal/wire"
)

// Adoption (D-373, D-378, D-386, ARCHITECTURE §19): a guest that a person
// put into the pool can become a Target. Plumb never moves a guest.

// Reasons why a pool guest cannot be adopted (UnmanagedGuest.reasons).
const (
	AdoptDiskLayout  = "disk-layout"
	AdoptDiskStorage = "disk-storage"
	AdoptNICLayout   = "nic-layout"
	AdoptBridge      = "bridge"
	AdoptVLAN        = "vlan"
	AdoptPrivileged  = "privileged"
	AdoptHostDevice  = "host-device"
	AdoptTemplate    = "template"
	AdoptName        = "name"
)

// Warnings of an adoptable guest (UnmanagedGuest.warnings).
const (
	WarnNotSealed   = "not-sealed"
	WarnNesting     = "nesting"
	WarnKeyctl      = "keyctl"
	WarnNoCloudInit = "no-cloud-init"
)

// Present says which secret config keys exist. Their values are never read
// past the inspect (D-340, review R3 of #290).
type Present struct {
	CIPassword  bool `json:"cipassword"`
	SSHKeys     bool `json:"sshkeys"`
	Description bool `json:"description"`
	Env         bool `json:"env"`
}

// Unmanaged is one pool guest as the adoption sees it.
type Unmanaged struct {
	ID       GuestID
	Node     string
	Name     string
	Kind     wire.Kind
	Power    Power // PowerRunning, PowerStopped or "" (unknown)
	Digest   string
	Reasons  []string
	Warnings []string
	// Guest holds the Target guest fields that an adoption writes:
	// cores, memory, disk, bridge, and ip, gateway, user only when
	// Proxmox has them; never ssh_keys.
	Guest   map[string]any
	Present Present
}

// Adoptable reports whether the guest has no reason against an adoption.
func (u Unmanaged) Adoptable() bool { return len(u.Reasons) == 0 }

// ErrNotInPool is returned by InspectGuest for a VMID that is not in the
// pool (or gone): the token cannot see guests outside it (D-378).
var ErrNotInPool = errors.New("driver: the guest is not in the pool")

// Adopter is the optional driver part of the adoption. It only reads.
type Adopter interface {
	// InspectGuest reads one pool guest: config (with digest) and power.
	// cipassword, sshkeys, description and an LXC env are dropped right
	// after the read; only Present says that they exist.
	InspectGuest(ctx context.Context, id GuestID) (Unmanaged, error)
	// SetAdopted names the guests of bound adopted Targets (VMID → Target
	// name), the whole set each time. The driver accepts such a VMID
	// outside its own range, and only while the guest is in the pool and
	// has that name (checked at every call).
	SetAdopted(refs map[GuestID]string)
}
