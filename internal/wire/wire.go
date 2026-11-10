// Package wire holds the types that the controller and the agent share:
// resources, changes, risk classes and the messages of the agent protocol
// (docs/ARCHITECTURE.md §3 and §5). It has no logic besides small helpers
// for ids and risk order, and it imports no other Plumb package.
package wire

import "strings"

// Risk is the risk class of one change (ARCHITECTURE §4).
type Risk string

const (
	RiskRead        Risk = "read"        // nothing changes
	RiskReversible  Risk = "reversible"  // can be undone without loss
	RiskDisruptive  Risk = "disruptive"  // restart or short outage
	RiskDestructive Risk = "destructive" // data loss is possible
)

// riskOrder lists the classes from lowest to highest.
var riskOrder = []Risk{RiskRead, RiskReversible, RiskDisruptive, RiskDestructive}

// Valid reports whether r is one of the four risk classes.
func (r Risk) Valid() bool { return r.level() >= 0 }

// Exceeds reports whether r is a higher risk than limit.
// An invalid risk exceeds every limit, so unknown values fail closed.
func (r Risk) Exceeds(limit Risk) bool {
	if !r.Valid() || !limit.Valid() {
		return true
	}
	return r.level() > limit.level()
}

// MaxRisk returns the highest risk of the changes, or RiskRead for none.
func MaxRisk(changes []Change) Risk {
	highest := RiskRead
	for _, c := range changes {
		if c.Risk.Exceeds(highest) {
			highest = c.Risk
		}
	}
	return highest
}

func (r Risk) level() int {
	for i, known := range riskOrder {
		if r == known {
			return i
		}
	}
	return -1
}

// Resource is one rendered resource of a target. The controller renders
// Blocks into a flat list of these and the agent applies them.
type Resource struct {
	// ID is "type:key", for example "file:/etc/motd". It is unique per target.
	ID string `json:"id"`
	// Type is the resource type, for example "file".
	Type string `json:"type"`
	// Block names the Block use that rendered the resource. It is unique per
	// use, also when a target uses the same Block twice. Resources with the
	// same Block apply in the listed order; different Blocks apply in parallel.
	Block string `json:"block"`
	// Attrs are the desired attributes. The JSON Schema of the type defines them.
	Attrs map[string]any `json:"attrs"`
	// Sensitive names the attributes whose value came from a secret.
	// The agent never reports a value or a hash for them (D-037).
	Sensitive []string `json:"sensitive,omitempty"`
}

// Change is one difference between the desired and the observed state of
// one attribute.
type Change struct {
	Attr string `json:"attr"`
	// From is the observed value and To the desired value.
	// Both are absent for a sensitive attribute.
	From any `json:"from,omitempty"`
	To   any `json:"to,omitempty"`
	// Changed is set for a sensitive attribute. It is the only information
	// that leaves the guest for such an attribute.
	Changed bool `json:"changed,omitempty"`
	Risk    Risk `json:"risk"`
}

// AttrRestart is the attribute of the change that the agent adds to a
// service when one of its restart_on resources changed (D-036).
const AttrRestart = "restart"

// GuestConfigID is the id of the resource that holds the Proxmox settings
// of a target. The driver handles it, not the agent.
const GuestConfigID = "guest:config"

// ID builds a resource id from a type and a key.
func ID(typ, key string) string { return typ + ":" + key }

// SplitID splits a resource id into type and key.
// ok is false when the id has no type or no key.
func SplitID(id string) (typ, key string, ok bool) {
	typ, key, found := strings.Cut(id, ":")
	return typ, key, found && typ != "" && key != ""
}
