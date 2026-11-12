package wire

import (
	"slices"
	"sort"
)

// Capabilities is what an agent can do (D-259). The agent sends it in every
// Hello. It builds the list from its own type table and schemas, so it
// never claims a type or an attribute that it cannot apply.
type Capabilities struct {
	// Commands are the command types that the agent runs, sorted.
	Commands []CommandType `json:"commands"`
	// Resources maps each resource type that the agent applies to the
	// attribute names that it knows (the properties of its schema),
	// sorted. A type with no entry is unknown to the agent.
	Resources map[string][]string `json:"resources"`
}

// BaselineCapabilities is what every agent before slice 3 can do: an agent
// that sends no Capabilities (Hello.Capabilities == nil) has exactly these.
// Never change this list: it describes agents that are already in guests.
func BaselineCapabilities() Capabilities {
	return Capabilities{
		Commands: []CommandType{CommandApply, CommandObserve, CommandSeal, CommandWatch},
		Resources: map[string][]string{
			"directory": {"group", "mode", "owner", "path", "state"},
			"file":      {"content", "group", "mode", "owner", "path", "state"},
			"package":   {"name", "state", "strictness", "version"},
			"service":   {"enabled", "name", "restart_on", "state"},
			"symlink":   {"path", "state", "target"},
			"user":      {"groups", "home", "name", "shell", "ssh_keys", "state"},
		},
	}
}

// CapabilitiesOf returns the capabilities of a Hello: its list, or the
// baseline for an agent that sends none.
func CapabilitiesOf(h Hello) Capabilities {
	if h.Capabilities == nil {
		return BaselineCapabilities()
	}
	return *h.Capabilities
}

// CanUpdate reports whether the agent runs the update command. An agent
// that cannot needs the one-time manual install (D-258).
func (c Capabilities) CanUpdate() bool {
	return slices.Contains(c.Commands, CommandUpdate)
}

// Missing returns what the resources need and the agent lacks, sorted and
// without duplicates: "<type>" for an unknown type, "<type>.<attr>" for an
// unknown attribute of a known type. A resource needs its type and every
// attribute that its Attrs set (render sets no defaults). Resources of
// controller types (guest:config, guest:address) need nothing. An empty
// result means the agent can apply the list. This is the one gate rule
// (D-259) for the Plan and for the Run start.
func (c Capabilities) Missing(resources []Resource) []string {
	seen := map[string]bool{}
	for _, r := range resources {
		if r.Type == "guest" {
			continue
		}
		attrs, ok := c.Resources[r.Type]
		if !ok {
			seen[r.Type] = true
			continue
		}
		for a := range r.Attrs {
			if !slices.Contains(attrs, a) {
				seen[r.Type+"."+a] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// GateMessage is the start of the text of a refused Plan or a refused Run
// step when the agent lacks a capability (D-259). The full text is
// GateMessage + ": target <name> needs <missing, comma separated>".
const GateMessage = "update the agent first"
