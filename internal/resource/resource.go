// Package resource defines the smallest managed unit of a guest: a file, a
// package, a service and so on (docs/ARCHITECTURE.md §2, §3, §6).
//
// The agent runs resources. The controller uses only the type table and the
// JSON Schemas to validate Blocks. A resource type has one Go file and one
// schema file in schema/.
package resource

import (
	"context"
	"embed"
	"sort"
	"sync"

	"github.com/Bernotese/plumb/internal/wire"
)

// State is the observed state of one resource: attribute name → value.
// It uses the attribute names of the schema of the type.
type State map[string]any

// Resource is one desired resource on one guest.
//
// Rules for every implementation:
//   - A missing attribute means the default of the schema.
//   - Observe changes nothing.
//   - Diff is pure. It returns no change when the guest equals the desired
//     state, with the strictness of the resource applied. For an attribute in
//     wire.Resource.Sensitive it compares locally and returns only
//     {Attr, Sensitive: true, Risk}, never From or To (D-037, D-047).
//   - Apply is idempotent and applies only the given changes. It takes the
//     values from the desired attributes, never from Change.To, so it also
//     works for sensitive changes.
//   - A resource that is not on the guest is observed as state "absent"
//     (wire.AttrState, wire.StateAbsent), so wire.ActionOf can tell create,
//     update and delete apart.
//   - Attribute values are data, never options: every command gets "--"
//     before its operands, and no value is passed through a shell.
type Resource interface {
	// Observe reads the real state from the guest.
	Observe(ctx context.Context) (State, error)
	// Diff compares the desired attributes with an observed state.
	// Every change has a risk class.
	Diff(observed State) []wire.Change
	// Apply makes the changes on the guest.
	Apply(ctx context.Context, changes []wire.Change) error
	// Watch lists the paths whose change can mean drift of this resource.
	Watch() []string
}

// Runner runs a command on the guest. Tests replace it with a fake.
// Output is stdout. A command that exits non-zero returns an *ExitError.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (output []byte, err error)
}

// Env is what a resource may touch. Tests point Root at a temp directory.
// Build it with NewEnv.
type Env struct {
	Root string // file-system root of the guest, "/" in production
	Run  Runner
	// PackageLock serializes every package-manager call of one guest.
	PackageLock *sync.Mutex
	// AccountLock serializes useradd, usermod and userdel of one guest:
	// Blocks apply in parallel, and the shadow tools lock /etc/passwd.
	AccountLock *sync.Mutex
	// StateDir is the agent state directory; network keeps its confirmed
	// file there during a safe apply (D-349). "" outside the agent.
	StateDir string
	// Controller is the host of the agent URL (name or IP): network finds
	// the main NIC and address by the route to it (D-348).
	Controller string
}

// MaxOutput is the longest Item.Output in bytes.
const MaxOutput = 4096

// HiddenOutput is the Item.Output of a resource with sensitive attributes.
const HiddenOutput = "output hidden: the resource has sensitive attributes"

// SafeOutput is the only way to build wire.Item.Output for a resource
// (wire.Redact does not cover Output). Command output and error texts are
// untrusted: they can hold the desired secret and also the old value that
// is on the guest now, which the controller never knows. So a resource with
// sensitive attributes gets HiddenOutput instead of the text. For all other
// resources the text is cut to MaxOutput bytes.
func SafeOutput(r wire.Resource, text string) string {
	if len(r.Sensitive) > 0 {
		return HiddenOutput
	}
	return clip(text, MaxOutput)
}

// Factory builds a resource from its rendered form.
type Factory func(r wire.Resource, env Env) (Resource, error)

// Type describes one resource type.
type Type struct {
	Name  string      // "file"
	Key   string      // attribute that forms the key of the id ("path" → "file:<path>")
	Kinds []wire.Kind // target kinds that support the type
	New   Factory     // nil until the type is implemented
	// NoDrift: the agent never reports drift of this type; the drift
	// check skips it. A Plan still observes it (D-260).
	NoDrift bool
	// Destructive: every change of this type is destructive, also an
	// unverified create in a Plan, so only a human session approves it
	// (D-031, D-260).
	Destructive bool
	// NotInImage: an Image build must not hold this type; the Image is
	// refused at save and the build checks again at start (D-260).
	NotInImage bool
}

//go:embed schema/*.json
var schemas embed.FS

// types is the table of all resource types. Add a type here, add its
// schema file and its Go file. Nothing else registers types.
var types = map[string]Type{
	"file":           {Name: "file", Key: "path", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}, New: newFile},
	"directory":      {Name: "directory", Key: "path", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}, New: newDirectory},
	"package":        {Name: "package", Key: "name", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}, New: newPackage},
	"service":        {Name: "service", Key: "name", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}, New: newService},
	"user":           {Name: "user", Key: "name", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}, New: newUser},
	"symlink":        {Name: "symlink", Key: "path", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}, New: newSymlink},
	"apt_repository": {Name: "apt_repository", Key: "name", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}, New: newAptRepo},
	"unit":           {Name: "unit", Key: "name", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}, New: newUnit},
	// An LXC shares the host kernel (ARCHITECTURE §6).
	"sysctl": {Name: "sysctl", Key: "name", Kinds: []wire.Kind{wire.KindVM}, New: newSysctl},
	// Safe apply (wire.SafeApplyTypes, D-293); nftables in an unprivileged
	// LXC is proven by the guest tests (T-116).
	"firewall": {Name: "firewall", Key: "name", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}, New: newFirewall,
		// A firewall belongs to one Target: its boot file is an agent path
		// that seal removes, and a build guest has no safe apply partner.
		NotInImage: true},
	// Proxmox owns the network of a container (D-332).
	// Safe apply (wire.SafeApplyTypes, D-333, D-349). A network belongs
	// to one Target, like a firewall.
	"network": {Name: "network", Key: "name", Kinds: []wire.Kind{wire.KindVM}, New: newNetwork, NotInImage: true},
	"command": {Name: "command", Key: "name", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}, New: newCommand,
		NoDrift: true, Destructive: true, NotInImage: true},
}

// Lookup returns the type with the given name.
func Lookup(name string) (Type, bool) {
	t, ok := types[name]
	return t, ok
}

// Names returns all type names in alphabetical order.
func Names() []string {
	names := make([]string, 0, len(types))
	for name := range types {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Schema returns the JSON Schema (2020-12) of the attributes of the type.
func (t Type) Schema() []byte {
	b, err := schemas.ReadFile("schema/" + t.Name + ".json")
	if err != nil {
		// A type without a schema file is a build mistake; the tests catch it.
		panic("resource: no schema for type " + t.Name)
	}
	return b
}

// Supports reports whether the type works on the given target kind.
func (t Type) Supports(kind wire.Kind) bool {
	for _, k := range t.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}
