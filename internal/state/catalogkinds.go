package state

import (
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// TypeKinds returns the Target kinds that support the resource type. ok is
// false for an unknown type. The API uses it for the kinds of a catalogue
// Block (D-328); api must not import resource (ARCHITECTURE §2).
func TypeKinds(typ string) (kinds []wire.Kind, ok bool) {
	t, ok := resource.Lookup(typ)
	return append([]wire.Kind(nil), t.Kinds...), ok
}
