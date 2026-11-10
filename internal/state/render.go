package state

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// GuestType is the resource type of the guest:config resource.
const GuestType = "guest"

// Rendered is the flat result of one Target.
type Rendered struct {
	Target string    `json:"target"`
	Kind   wire.Kind `json:"kind"`
	Image  string    `json:"image"`
	// Guest is the guest:config resource: the Proxmox settings, with
	// defaults. The driver handles it, not the agent. Its Block is empty.
	Guest wire.Resource `json:"guest"`
	// Resources are the resources that the agent applies, in Block order
	// and, inside a Block, in the listed order.
	Resources []wire.Resource `json:"resources"`
}

// All returns guest:config followed by the agent resources.
func (r *Rendered) All() []wire.Resource {
	return append([]wire.Resource{r.Guest}, r.Resources...)
}

// Render renders one Target of the set into its flat resource list:
// value templates over the Block parameters, then the overrides. The
// limits of the Set apply. The error is of type Errors.
func (s *Set) Render(target string) (*Rendered, error) { return s.render(target, nil) }

// render renders one Target. total is the shared budget of a Validate
// call, or nil.
func (s *Set) render(target string, total *budget) (*Rendered, error) {
	t, ok := s.targets[target]
	if !ok {
		return nil, Errors{{Kind: KindTarget, Name: target, Msg: "Target does not exist"}}
	}
	errf := func(path, format string, args ...any) Errors {
		return Errors{{Kind: KindTarget, Name: target, Path: path, Msg: fmt.Sprintf(format, args...)}}
	}
	if errs := s.checkTarget(target); len(errs) > 0 {
		return nil, errs
	}
	sch := mustSchemas()
	out := &Rendered{Target: target, Kind: wire.Kind(t.Type), Image: t.Image}
	out.Guest = wire.Resource{
		ID:    wire.GuestConfigID,
		Type:  GuestType,
		Attrs: withDefaults(t.Guest, sch.guestDefaults),
	}

	bud := newBudget(s.limits, total)
	where := map[string]string{} // resource id → path of its origin
	byID := map[string]*wire.Resource{}
	for i, use := range t.Blocks {
		b := s.blocks[use.Use]
		params := templateData(withDefaults(use.Params, b.defaults)).(map[string]any)
		usePath := ptr("/spec/blocks", i)
		for j, br := range b.resources {
			origin := fmt.Sprintf("%s (Block %q /spec/resources/%d)", usePath, use.Use, j)
			rt, _ := resource.Lookup(br.Type)
			if !rt.Supports(out.Kind) {
				return nil, errf(usePath, "Block %q /spec/resources/%d: resource type %s does not support %s targets",
					use.Use, j, br.Type, t.Type)
			}
			attrs, err := renderValue(br.Attrs, params, bud, ptr("/spec/resources", j, br.Type))
			if err != nil {
				e := errf(usePath, "Block %q %v", use.Use, err)
				e[0].Err = err
				return nil, e
			}
			am := attrs.(map[string]any)
			key, ok := am[rt.Key].(string)
			if !ok || key == "" {
				return nil, errf(usePath, "Block %q /spec/resources/%d: key attribute %q must be a non-empty string",
					use.Use, j, rt.Key)
			}
			id := wire.ID(br.Type, key)
			if first, dup := where[id]; dup {
				return nil, errf(usePath, "resource %s from %s is already defined by %s; resource ids must be unique per target",
					id, origin, first)
			}
			where[id] = origin
			out.Resources = append(out.Resources, wire.Resource{ID: id, Type: br.Type, Block: use.id(), Attrs: am})
		}
	}
	for i := range out.Resources {
		byID[out.Resources[i].ID] = &out.Resources[i]
	}
	byID[wire.GuestConfigID] = &out.Guest

	// Overrides replace single rendered attributes. They are not templates.
	ids := make([]string, 0, len(t.Overrides))
	for id := range t.Overrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		path := ptr("/spec/overrides", id)
		r, ok := byID[id]
		if !ok {
			return nil, errf(path, "resource %s is not on the target", id)
		}
		keyAttr := ""
		if rt, ok := resource.Lookup(r.Type); ok {
			keyAttr = rt.Key
		}
		for attr, v := range t.Overrides[id] {
			if attr == keyAttr && !reflect.DeepEqual(v, r.Attrs[attr]) {
				return nil, errf(ptr(path, attr), "an override cannot change the key attribute; it would change the resource id")
			}
			r.Attrs[attr] = deepCopy(v)
		}
	}

	// Every attribute set must match the schema of its type.
	if err := sch.guest.Validate(out.Guest.Attrs); err != nil {
		return nil, schemaErrors(KindTarget, target, "/spec/guest", err)
	}
	for _, r := range out.Resources {
		if err := sch.resources[r.Type].Validate(r.Attrs); err != nil {
			errs := schemaErrors(KindTarget, target, "", err)
			for _, e := range errs {
				e.Msg = fmt.Sprintf("resource %s from %s: attribute %s: %s", r.ID, where[r.ID], e.Path, e.Msg)
				e.Path = "/spec/blocks"
				if _, over := t.Overrides[r.ID]; over {
					e.Msg += " (the resource has overrides)"
				}
			}
			return nil, errs
		}
	}

	// restart_on must name resources of this target that the agent applies.
	for _, r := range out.Resources {
		list, _ := r.Attrs["restart_on"].([]any)
		for k, v := range list {
			id, _ := v.(string)
			if _, ok := byID[id]; !ok || id == wire.GuestConfigID || id == r.ID {
				return nil, errf("/spec/blocks", "resource %s from %s: restart_on/%d: %q is not another resource on this target",
					r.ID, where[r.ID], k, id)
			}
		}
	}
	return out, nil
}

// renderValue copies v and renders every string in it as a template.
func renderValue(v any, params map[string]any, b *budget, path string) (any, error) {
	switch v := v.(type) {
	case string:
		s, err := execTemplate(v, params, b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return s, nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys) // stable order, so the budget is used up the same way
		m := make(map[string]any, len(v))
		for _, k := range keys {
			r, err := renderValue(v[k], params, b, ptr(path, k))
			if err != nil {
				return nil, err
			}
			m[k] = r
		}
		return m, nil
	case []any:
		s := make([]any, len(v))
		for i, e := range v {
			r, err := renderValue(e, params, b, ptr(path, i))
			if err != nil {
				return nil, err
			}
			s[i] = r
		}
		return s, nil
	}
	return v, nil
}
