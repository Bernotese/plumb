// Package driver is the seam between Plumb and the hypervisor
// (docs/ARCHITECTURE.md §2, D-005, D-029). The core uses only this
// interface. driver/proxmox talks to Proxmox VE; driver/fake is for tests
// and for dev mode.
package driver

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/Bernotese/plumb/internal/wire"
)

// GuestID identifies a guest inside the driver (for Proxmox: the VMID).
type GuestID string

// ImageRef identifies an imported image inside the driver
// (for Proxmox: the VMID of the template). It is only the clone source of
// Create: a template can be made again with a new ref (D-188), so no
// guest setting depends on it (D-189).
type ImageRef string

// ImageSpec is an Image document as the driver needs it.
type ImageSpec struct {
	Name   string
	Kind   wire.Kind
	URL    string
	SHA256 string // pinned checksum; the driver must refuse any other file
}

// GuestSpec holds the managed settings of a guest (resource guest:config).
// The attribute names below are also the Attr values of the changes.
type GuestSpec struct {
	Name string    // target name; create-only
	Kind wire.Kind // create-only
	// ImageName is the name of the Image document: the create-only
	// attribute "image" (D-189). Observe reports it.
	ImageName string
	// Image is the clone source for Create only. Diff and DriftGuest
	// ignore it, and Observe leaves it empty (D-189).
	Image    ImageRef
	Cores    int      // AttrCores
	MemoryMB int      // AttrMemory
	DiskGB   int      // AttrDisk; create-only in the first slice
	Bridge   string   // AttrBridge
	IP       string   // AttrIP: "dhcp" or CIDR; create-only
	Gateway  string   // AttrGateway; create-only
	User     string   // AttrUser: bootstrap user; create-only
	SSHKeys  []string // AttrSSHKeys of the bootstrap user; create-only
	// ImageVersion is the version of a built Image that Create cloned, 0
	// for a stock Image (D-200). Create writes it into the guest
	// description as "plumb-image-version: <N>", and Observe reads it. It
	// is information only: Diff and DriftGuest ignore it.
	ImageVersion int
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
	// AttrEnrollToken is not part of GuestSpec. A Plan for re-enrollment
	// (D-038) lists it as a sensitive change: {attr, sensitive, risk}.
	// The token itself is never in a Change. It goes to Update as Bootstrap.
	AttrEnrollToken = "enroll_token"
)

// Bootstrap is what a guest needs to enroll its agent. How the token
// reaches the guest is the business of the driver (ARCHITECTURE §5).
// A Bootstrap is a live secret: never log it, never store it, never put it
// into a Change, a Plan or an event.
type Bootstrap struct {
	Token string // one-time enrollment token
}

// Power is the power state of a guest.
type Power string

const (
	PowerRunning Power = "running"
	PowerStopped Power = "stopped"
)

// PowerAction is a power action.
type PowerAction string

const (
	PowerStart   PowerAction = "start"
	PowerStop    PowerAction = "stop"    // clean shutdown, then hard stop after a timeout
	PowerRestart PowerAction = "restart" // stop, then start (a new token needs a cold start)
)

// GuestState is what Observe reads.
type GuestState struct {
	Spec GuestSpec // the managed settings as the running guest uses them
	// Pending is the view after the next stop and start of the guest: a
	// value that is set in the hypervisor but not active yet (D-190). It
	// is nil when both views are equal.
	Pending *GuestSpec
	// TokenPending says that a new enrollment token is set but not active:
	// the guest has not had a cold start since it was written.
	TokenPending bool
	Digest       string // changes whenever the guest config changes; covers both views
	Power        Power
	IPs          []string // addresses that the hypervisor reports
}

// Next returns the settings after the next stop and start.
func (s GuestState) Next() GuestSpec {
	if s.Pending != nil {
		return *s.Pending
	}
	return s.Spec
}

// Driver manages guests and images on one hypervisor.
// All methods except Diff wait until the hypervisor finished the task.
type Driver interface {
	// FetchImage downloads the image, verifies the checksum and makes it
	// usable for Create. It is idempotent for the same spec, and it makes
	// the image again when it is gone. The engine calls it before every
	// Create and keeps no copy of the ref (D-188).
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
	// Update applies changes that Diff returned. boot is nil, except for
	// re-enrollment: then Update also gives the guest the new token. The
	// token takes effect at the next cold start (Power with PowerRestart).
	Update(ctx context.Context, id GuestID, changes []wire.Change, boot *Bootstrap) error
	// Power runs a power action.
	Power(ctx context.Context, id GuestID, action PowerAction) error
	// Delete stops and removes the guest and its disks. It never removes
	// a template (D-077); only RemoveImage does.
	Delete(ctx context.Context, id GuestID) error

	// CreateBuild makes the build guest of one image version as a full
	// clone of spec.Base, gives it the enrollment token (marked build by
	// the engine) and starts it (D-203).
	CreateBuild(ctx context.Context, spec BuildSpec, boot Bootstrap) (GuestID, error)
	// MakeImage turns a sealed, stopped build guest into the template of
	// img (D-208). Guard: the build marker of img and power stopped. One
	// config write with the digest clears the cloud-init values and the
	// serial, sets ciupgrade=0, the name and the description; then the
	// conversion. It never converts a guest that fails the guard.
	MakeImage(ctx context.Context, id GuestID, img BuiltImage) (ImageRef, error)
	// FindImage finds the template of a version by name plus the full
	// digest line, in the pool and the VMID range (D-198). ErrNotFound
	// when it is gone.
	FindImage(ctx context.Context, image string, version int, digest string) (ImageRef, error)
	// ListImages lists the templates and import files of an Image.
	ListImages(ctx context.Context, image string) ([]ImageObject, error)
	// RemoveImage deletes one template after the guard of D-209: range,
	// pool, template, no lock, exact name, a description that names the
	// Image with the full digest or checksum, and no linked clone in the
	// pool. It refuses an import file: Plumb never deletes one (D-209).
	RemoveImage(ctx context.Context, obj ImageObject) error
	// DiskSpace reads the use of the disk storage of guests.
	DiskSpace(ctx context.Context) (DiskSpace, error)
	// ListBuilds lists the build guests in the pool and the VMID range:
	// guests with the build marker and the exact build name (D-203).
	// Recover and every new build of an Image remove them; a converted one
	// has Template set and stays.
	ListBuilds(ctx context.Context) ([]BuildGuest, error)
	// RemoveBuild stops and deletes the build guest of image v<version>
	// after the full guard of T-073: in the range, in the pool, the exact
	// name plumb-build-<image>-v<version>, the build marker of that
	// version in its config, no template, no lock. It never converts. A
	// guest that is gone is ErrNotFound; a guest that fails the guard is
	// left, with an error that names it.
	RemoveBuild(ctx context.Context, id GuestID, image string, version int) error
}

// --- Image builds (T-039, D-203, D-208, D-209) ---

// BuildSpec is the temporary guest of one image build (D-203). The driver
// names it "plumb-build-<Image>-v<Version>", marks its description with
// "plumb-build: <Image> v<Version>", sets onboot=0 and the bootstrap user
// "plumb-build" without a key. The clone is always full (D-208).
type BuildSpec struct {
	Image    string   // Image document name
	Version  int      // the new version number (image_version)
	Base     ImageRef // the stock template of the base image, from FetchImage
	Upgrade  bool     // ciupgrade=1: cloud-init upgrades all packages at the first boot (D-204)
	Cores    int
	MemoryMB int
	DiskGB   int // the template gets this size
	Bridge   string
	IP       string // "dhcp" or CIDR
	Gateway  string
}

// BuiltImage names the template that MakeImage makes from a sealed build
// guest: "plumb-img-<Image>-v<Version>", with the description lines
// "plumb-image: <Image>", "version=<Version>" and "build=<Digest>" (D-198).
type BuiltImage struct {
	Image   string
	Version int
	// Digest is the hex SHA-256 over image name, version, recipe, base
	// checksum and Run id. With the name it is the proof of ownership for
	// FindImage and RemoveImage (D-188, D-209).
	Digest string
}

// BuildGuest is a build guest that ListBuilds found.
type BuildGuest struct {
	ID      GuestID
	Image   string
	Version int
	// Template: the guest was converted but never named as a version (a
	// conversion that ended after its error, T-061 review N1). RemoveBuild
	// refuses it; it is shown, not adopted and not removed.
	Template bool
}

// ImageObjectKind says what an ImageObject is.
type ImageObjectKind string

const (
	ImageVersionTemplate ImageObjectKind = "version" // a built version, plumb-img-<name>-v<N>
	ImageBaseTemplate    ImageObjectKind = "base"    // a stock template, plumb-img-<name>-<sha12>
	ImageImportFile      ImageObjectKind = "import"  // a downloaded base image in the image storage
)

// ImageObject is one hypervisor object of an Image, as ListImages sees it.
type ImageObject struct {
	Kind ImageObjectKind
	// Image is the Image document the object belongs to. RemoveImage
	// checks that the description names it (D-209).
	Image string
	Ref   ImageRef // template VMID; empty for an import file
	Name  string   // template name, or the volume id of an import file
	// Version is set for ImageVersionTemplate, else 0.
	Version int
	// Digest is the build digest of a version, or the SHA-256 of the base
	// image for a base template or an import file ("" when unknown).
	Digest    string
	SizeBytes int64
	// LinkedClones is the number of pool guests that are linked clones
	// of this template (review S3). RemoveImage refuses a template with one.
	LinkedClones int
}

// DiskSpace is the real use of the disk storage of guests (D-203). The
// engine refuses a build when it would fill more than the limit.
type DiskSpace struct {
	Storage    string
	TotalBytes int64
	UsedBytes  int64 // real use (thin pool: data), not the sum of virtual sizes
	// MetaKnown: the token can read the thin-pool metadata use; then the
	// same limit holds for it (T-061 finds out on the host).
	MetaKnown      bool
	MetaTotalBytes int64
	MetaUsedBytes  int64
}

// ErrNotImplemented is returned by the image build methods until T-061.
var ErrNotImplemented = errors.New("driver: image builds are not implemented yet (T-061)")

// ErrNotFound is returned when a guest or an image does not exist.
var ErrNotFound = errors.New("driver: not found")

// CreateOnlyError says that a setting cannot change after creation.
type CreateOnlyError struct {
	Attr string
}

func (e *CreateOnlyError) Error() string {
	return fmt.Sprintf("driver: %q can only be set when the guest is created", e.Attr)
}

// OverLimit is the exact disk rule of D-203 and T-074: need is over the
// limit when need × 100 > limit × total, in integer bytes with no
// rounding. A total of 0 or less is always over (an unknown size is no
// room). The web (overLimit in web/src/lib/images.ts) uses the same rule.
func OverLimit(need, total int64, limitPercent int) bool {
	if total <= 0 {
		return true
	}
	n := new(big.Int).Mul(big.NewInt(need), big.NewInt(100))
	l := new(big.Int).Mul(big.NewInt(int64(limitPercent)), big.NewInt(total))
	return n.Cmp(l) > 0
}
