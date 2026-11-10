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
//     wire.Resource.Sensitive it sets only Change.Changed, never From or To.
//   - Apply is idempotent and applies only the given changes.
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
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (output []byte, err error)
}

// Env is what a resource may touch. Tests point Root at a temp directory.
type Env struct {
	Root string // file-system root of the guest, "/" in production
	Run  Runner
}

// Factory builds a resource from its rendered form.
type Factory func(r wire.Resource, env Env) (Resource, error)

// Type describes one resource type.
type Type struct {
	Name  string      // "file"
	Key   string      // attribute that forms the key of the id ("path" → "file:<path>")
	Kinds []wire.Kind // target kinds that support the type
	New   Factory     // nil until the type is implemented
}

//go:embed schema/*.json
var schemas embed.FS

// types is the table of all resource types. Add a type here, add its
// schema file and its Go file. Nothing else registers types.
var types = map[string]Type{
	"file":      {Name: "file", Key: "path", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}},
	"directory": {Name: "directory", Key: "path", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}},
	"package":   {Name: "package", Key: "name", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}},
	"service":   {Name: "service", Key: "name", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}},
	"user":      {Name: "user", Key: "name", Kinds: []wire.Kind{wire.KindVM, wire.KindLXC}},
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
