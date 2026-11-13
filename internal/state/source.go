package state

import (
	"maps"
	"slices"
)

// Source is the Block use that renders a resource (D-301): Block is the
// Block document, Use the id of the use in spec.blocks.
type Source struct {
	Block string
	Use   string
}

// Sources returns the Block use of every agent resource that the Target
// renders. guest:config has none. An unknown Target or one that does not
// render gives nil and the render error.
func (s *Set) Sources(target string) (map[string]Source, error) {
	r, err := s.Render(target)
	if err != nil {
		return nil, err
	}
	names := map[string]string{} // use id → Block name
	for _, u := range s.targets[target].Blocks {
		names[u.id()] = u.Use
	}
	out := make(map[string]Source, len(r.Resources))
	for _, res := range r.Resources {
		if b, ok := names[res.Block]; ok {
			out[res.ID] = Source{Block: b, Use: res.Block}
		}
	}
	return out, nil
}

// Override is one entry of spec.overrides of a Target: the resource and
// the names of the attributes it fixes, sorted (D-301).
type Override struct {
	Resource string
	Attrs    []string
}

// Overrides returns spec.overrides of a Target, sorted by resource. nil
// for an unknown Target.
func (s *Set) Overrides(target string) []Override {
	t, ok := s.targets[target]
	if !ok {
		return nil
	}
	out := make([]Override, 0, len(t.Overrides))
	for _, id := range slices.Sorted(maps.Keys(t.Overrides)) {
		out = append(out, Override{Resource: id, Attrs: slices.Sorted(maps.Keys(t.Overrides[id]))})
	}
	return out
}
