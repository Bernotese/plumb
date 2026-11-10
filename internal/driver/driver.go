// Package driver is the seam between Plumb and the hypervisor
// (docs/ARCHITECTURE.md §2, D-005, D-029). The core uses only this
// interface. driver/proxmox talks to Proxmox VE; driver/fake is for tests
// and for dev mode.
package driver

import (
	"context"
	"fmt"

	"github.com/Bernotese/plumb/internal/wire"
)

// Kind is the kind of a guest.
type Kind string

const (
	KindVM  Kind = "vm"
	KindLXC Kind = "lxc"
)

// GuestID identifies a guest inside the driver (for Proxmox: the VMID).
type GuestID string

// ImageRef identifies an imported image inside the driver
// (for Proxmox: the VMID of the template).
type ImageRef string

// ImageSpec is an Image document as the driver needs it.
type ImageSpec struct {
	Name   string
	Kind   Kind
	URL    string
	SHA256 string // pinned checksum; the driver must refuse any other file
}

// GuestSpec holds the managed settings of a guest (resource guest:config).
// The attribute names below are also the Attr values of the changes.
type GuestSpec struct {
	Name     string   // target name; create-only
	Kind     Kind     // create-only
	Image    ImageRef // create-only
	Cores    int      // AttrCores
	MemoryMB int      // AttrMemory
	DiskGB   int      // AttrDisk; create-only in the first slice
	Bridge   string   // AttrBridge
	IP       string   // AttrIP: "dhcp" or CIDR; create-only
	Gateway  string   // AttrGateway; create-only
	User     string   // AttrUser: bootstrap user; create-only
	SSHKeys  []string // AttrSSHKeys of the bootstrap user; create-only
}

// Attribute names of guest:config changes.
const (
	AttrCores   = "cores"
	AttrMemory  = "memory"
	AttrDisk    = "disk"
	AttrBridge  = "bridge"
	AttrIP      = "ip"
	AttrGateway = "gateway"
	AttrUser    = "user"
	AttrSSHKeys = "ssh_keys"
	// AttrEnrollToken is not part of GuestSpec. A change with this attribute
	// asks Update to set a new enrollment token (re-enrollment, D-038).
	// Change.To holds the token and Change.Changed is set; the token must
	// never be stored in a Plan or an event.
	AttrEnrollToken = "enroll_token"
)

// Bootstrap is what a new guest needs to enroll its agent. How the token
// reaches the guest is the business of the driver (ARCHITECTURE §5).
type Bootstrap struct {
	Token string // one-time enrollment token
}

// Power is the power state of a guest.
type Power string

const (
	PowerRunning Power = "running"
	PowerStopped Power = "stopped"
)

// Action is a power action.
type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"    // clean shutdown, then hard stop after a timeout
	ActionRestart Action = "restart" // stop, then start (a new token needs a cold start)
)

// GuestState is what Observe reads.
type GuestState struct {
	Spec   GuestSpec // the managed settings as they are now
	Digest string    // changes whenever the guest config changes
	Power  Power
	IPs    []string // addresses that the hypervisor reports
}

// Driver manages guests and images on one hypervisor.
// All methods except Diff wait until the hypervisor finished the task.
type Driver interface {
	// FetchImage downloads the image, verifies the checksum and makes it
	// usable for Create. It is idempotent for the same spec.
	FetchImage(ctx context.Context, spec ImageSpec) (ImageRef, error)
	// Create makes a new guest from the image in spec, gives it the
	// enrollment token and starts it.
	Create(ctx context.Context, spec GuestSpec, boot Bootstrap) (GuestID, error)
	// Observe reads the settings, the config digest, the power state and
	// the addresses. It returns ErrNotFound when the guest does not exist.
	Observe(ctx context.Context, id GuestID) (GuestState, error)
	// Diff compares desired settings with an observed state and sets the
	// risk of each change. It is pure. A change of a create-only setting
	// is not a change: Diff returns a *CreateOnlyError.
	Diff(desired GuestSpec, observed GuestState) ([]wire.Change, error)
	// Update applies changes that Diff returned, or an AttrEnrollToken change.
	Update(ctx context.Context, id GuestID, changes []wire.Change) error
	// Power runs a power action.
	Power(ctx context.Context, id GuestID, action Action) error
	// Delete stops and removes the guest and its disks.
	Delete(ctx context.Context, id GuestID) error
}

// ErrNotFound is returned when a guest or an image does not exist.
var ErrNotFound = fmt.Errorf("driver: not found")

// CreateOnlyError says that a setting cannot change after creation.
type CreateOnlyError struct {
	Attr string
}

func (e *CreateOnlyError) Error() string {
	return fmt.Sprintf("driver: %q can only be set when the guest is created", e.Attr)
}
