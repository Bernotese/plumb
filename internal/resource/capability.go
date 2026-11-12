package resource

import (
	"encoding/json"
	"sort"
)

// Capabilities returns the resource part of the capability list (D-259):
// every implemented type of the table with the property names of its
// schema, sorted. The agent adds its commands.
func Capabilities() map[string][]string {
	out := map[string][]string{}
	for name, t := range types {
		if t.New == nil {
			continue
		}
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(t.Schema(), &s); err != nil {
			panic("resource: bad schema for type " + name) // the tests catch it
		}
		attrs := make([]string, 0, len(s.Properties))
		for a := range s.Properties {
			attrs = append(attrs, a)
		}
		sort.Strings(attrs)
		out[name] = attrs
	}
	return out
}
