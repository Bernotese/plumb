// Package wire holds the types that the controller and the agent share:
// resources, changes, risk classes and the messages of the agent protocol
// (docs/ARCHITECTURE.md §3 and §5). It has no logic besides the rules that
// both sides must share (ids, risk order, ActionOf, Covers, Order), and it
// imports no other Plumb package.
package wire

import (
	"encoding/json"
	"slices"
	"strings"
)

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
	// Block is the id of the Block use that rendered the resource: the "id"
	// of the entry in the Target, which defaults to the Block name. Two uses
	// of the same Block need different ids. Resources with the same Block
	// apply in the listed order; different Blocks apply in parallel, unless
	// After orders them.
	Block string `json:"block"`
	// After names resources of the same target that must have ended before
	// this one starts (D-167). state.Render sets it on the first resource of
	// a Block use whose Block has spec.after; the rest of the use follows by
	// the listed order. The resource runs only when each named resource
	// ended unchanged or applied; else it is skipped (D-170). After is order
	// only: it is no attribute, has no diff and an override cannot set it.
	// An agent from before D-169 ignores the field. See Order.
	After []string `json:"after,omitempty"`
	// Attrs are the desired attributes. The JSON Schema of the type defines them.
	Attrs map[string]any `json:"attrs"`
	// Sensitive names the attributes whose value came from a secret.
	// The agent never reports a value or a hash for them (D-037); see Redact.
	Sensitive []string `json:"sensitive,omitempty"`
	// SecretValues are the clear secret values inside Attrs. They never go
	// on the wire (json "-"); the controller keeps them in memory only, to
	// mask them in every agent output and error (D-062, T-029 QA).
	SecretValues []string `json:"-"`
}

// Kind is the kind of a target.
type Kind string

const (
	KindVM  Kind = "vm"
	KindLXC Kind = "lxc"
)

// Change is one difference between the observed and the desired state of
// one attribute. It has the same meaning and the same JSON form in agent
// reports, Plans, run steps, Issues and the public API:
// From is what the guest has now, To is the desired state.
type Change struct {
	Attr string `json:"attr"`
	// From is the observed value and To the desired value. A missing value
	// means "not there". Both are always missing for a sensitive attribute.
	From any `json:"from,omitempty"`
	To   any `json:"to,omitempty"`
	// Sensitive marks a change of a secret value. The entry itself says
	// "changed"; no value and no hash of it leaves the guest (D-037).
	Sensitive bool `json:"sensitive,omitempty"`
	Risk      Risk `json:"risk"`
	// Ignored is set only by the controller, in a Plan: a Rule ignores this
	// drift (D-092). The Plan still applies it. An agent never sets it.
	Ignored bool `json:"ignored,omitempty"`
	// The three fields below are set only on guest:config changes (D-190,
	// D-195). No risk rule reads them; the restart rule of the controller
	// reads Pending (a removal of a pending value needs no restart).
	//
	// Pending: From is set in the hypervisor but not active; the guest
	// still runs with To (or with AlsoPending's partner, see there).
	Pending bool `json:"pending,omitempty"`
	// AlsoPending: From is the running value, and this other value is set
	// and waits for the next stop and start.
	AlsoPending any `json:"also_pending,omitempty"`
	// WaitsForRestart: To is (or will be) set in the hypervisor and becomes
	// active at the next stop and start of the guest.
	WaitsForRestart bool `json:"waits_for_restart,omitempty"`
}

// Redact enforces the sensitive rule on changes that come from outside the
// controller. It returns a copy in which every change that is marked
// sensitive, or whose attribute is in the sensitive list, has no From and
// no To. The controller must call it on every agent report before it
// stores or forwards the changes: agent reports are untrusted input.
func Redact(sensitive []string, changes []Change) []Change {
	if changes == nil {
		return nil
	}
	out := make([]Change, len(changes))
	for i, c := range changes {
		if c.Sensitive || contains(sensitive, c.Attr) {
			c = Change{Attr: c.Attr, Sensitive: true, Risk: c.Risk}
		}
		c.Ignored = false // only the controller marks a change as ignored (D-092)
		out[i] = c
	}
	return out
}

// Action says what a set of changes does to one resource.
type Action string

const (
	ActionNone    Action = ""        // no change
	ActionCreate  Action = "create"  // the resource does not exist yet
	ActionUpdate  Action = "update"  // the resource exists and changes
	ActionDelete  Action = "delete"  // the resource goes away
	ActionRestart Action = "restart" // nothing changes besides a restart
)

// Attribute and values that every resource uses to say whether it exists.
// A resource that is not on the guest is observed as state "absent".
const (
	AttrState    = "state"
	StateAbsent  = "absent"
	StatePresent = "present"
)

// ActionOf derives the action of one resource from its changes. This is the
// only rule; Plans, run steps and the API use it.
//
//   - no changes: ActionNone
//   - a "state" change from "absent" (or from nothing): ActionCreate
//   - a "state" change to "absent": ActionDelete
//   - only "restart" changes: ActionRestart
//   - everything else: ActionUpdate
func ActionOf(changes []Change) Action {
	if len(changes) == 0 {
		return ActionNone
	}
	onlyRestart := true
	for _, c := range changes {
		if c.Attr == AttrState {
			if c.To == StateAbsent {
				return ActionDelete
			}
			if c.From == nil || c.From == StateAbsent {
				return ActionCreate
			}
		}
		if c.Attr != AttrRestart {
			onlyRestart = false
		}
	}
	if onlyRestart {
		return ActionRestart
	}
	return ActionUpdate
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// AttrRestart is the attribute of the change that the agent adds to a
// service when one of its restart_on resources changed (D-036).
const AttrRestart = "restart"

// GuestConfigID is the id of the resource that holds the Proxmox settings
// of a target. The driver handles it, not the agent.
const GuestConfigID = "guest:config"

// GuestAddressID is the id of the resource of the address check (D-215): a
// Target with a static ip whose agent does not report that address. The
// controller makes it from the reported list; no agent reports it.
const GuestAddressID = "guest:address"

// agentTypes are the resource types that an agent manages and may report.
// It must equal the type table of internal/resource (a test there checks
// it). The type "guest" belongs to the controller.
var agentTypes = map[string]bool{
	"apt_repository": true, "command": true, "directory": true, "file": true, "package": true, "service": true, "symlink": true,
	"sysctl": true, "unit": true, "user": true,
}

// AgentType reports whether an agent may report a resource of this type.
func AgentType(typ string) bool { return agentTypes[typ] }

// AgentTypes returns the agent resource types, sorted.
func AgentTypes() []string {
	out := make([]string, 0, len(agentTypes))
	for t := range agentTypes {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// ID builds a resource id from a type and a key.
func ID(typ, key string) string { return typ + ":" + key }

// SplitID splits a resource id into type and key.
// ok is false when the id has no type or no key.
func SplitID(id string) (typ, key string, ok bool) {
	typ, key, found := strings.Cut(id, ":")
	return typ, key, found && typ != "" && key != ""
}

// Covers reports whether every change in now is also in planned: same
// attr, from, to and risk. When either side is sensitive, only attr and
// risk are compared. Values are compared in their JSON form, so an int
// equals a float64 or json.Number of the same value. Order does not
// matter. This is the one rule of D-075 for the controller and the agent.
func Covers(planned, now []Change) bool {
	for _, c := range now {
		found := false
		for _, p := range planned {
			if sameChange(p, c) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// SameValue reports whether two change values are equal in their JSON
// form, the comparison of Covers.
func SameValue(a, b any) bool { return jsonForm(a) == jsonForm(b) }

func sameChange(a, b Change) bool {
	if a.Attr != b.Attr || a.Risk != b.Risk {
		return false
	}
	if a.Sensitive || b.Sensitive {
		return true
	}
	return jsonForm(a.From) == jsonForm(b.From) && jsonForm(a.To) == jsonForm(b.To)
}

// jsonForm returns the normal JSON text of a value: decoded and encoded
// again, so numbers are float64 and object keys are sorted. A missing
// value is "null".
func jsonForm(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "\x00invalid"
	}
	var back any
	if err := json.Unmarshal(raw, &back); err != nil {
		return "\x00invalid"
	}
	out, _ := json.Marshal(back)
	return string(out)
}
