package state

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
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
	// AllowRestart is spec.allow_restart of the Target (D-194): a Run may
	// restart the running VM. It is a rule of Plumb: never drift, no
	// override, and the driver never gets it.
	AllowRestart bool `json:"allow_restart,omitempty"`
	// Adopted is spec.adopted of an adopted Target (D-386), else nil.
	Adopted *Adopted `json:"adopted,omitempty"`
	// Guest is the guest:config resource: the Proxmox settings, with
	// defaults. The driver handles it, not the agent. Its Block is empty.
	Guest wire.Resource `json:"guest"`
	// Resources are the resources that the agent applies, in Block order
	// and, inside a Block, in the listed order. The first resource of a
	// use whose Block has spec.after carries the ids in After (D-169).
	Resources []wire.Resource `json:"resources"`
	// cycle is the text of an apply-order cycle in a stored revision
	// (D-168); see Set.Cycles.
	cycle string
}

// All returns guest:config followed by the agent resources.
func (r *Rendered) All() []wire.Resource {
	return append([]wire.Resource{r.Guest}, r.Resources...)
}

// Render renders one Target of the set into its flat resource list:
// value templates over the Block parameters, the restarts of the Blocks
// (D-172), then the overrides; then the checks, and the apply order last
// (D-168). The limits of the Set apply. The error is of type Errors.
func (s *Set) Render(target string) (*Rendered, error) { return s.render(target, nil) }

// render renders one Target. total is the shared budget of a Validate
// call, or nil.
func (s *Set) render(target string, total *budget) (*Rendered, error) {
	u, ok := s.targetUnit(target)
	if !ok {
		return nil, Errors{{Kind: KindTarget, Name: target, Msg: "Target does not exist"}}
	}
	return s.renderUnit(u, total)
}

// unit is what renderUnit renders: a Target, or the build of an Image
// (D-197). Paths point into the document, for the error texts.
type unit struct {
	kind, name   string
	typ          string
	image        string
	allowRestart bool
	guest        map[string]any
	blocks       []blockUse
	overrides    map[string]map[string]any
	adopted      *Adopted
	blocksPath   string // "/spec/blocks" or "/spec/build/blocks"
	guestPath    string // "/spec/guest" or "/spec/build/guest"
}

func (s *Set) targetUnit(name string) (*unit, bool) {
	t, ok := s.targets[name]
	if !ok {
		return nil, false
	}
	return &unit{kind: KindTarget, name: name, typ: t.Type, image: t.Image, allowRestart: t.AllowRestart,
		guest: t.Guest, blocks: t.Blocks, overrides: t.Overrides, adopted: t.Adopted,
		blocksPath: "/spec/blocks", guestPath: "/spec/guest"}, true
}

func (s *Set) imageUnit(name string) (*unit, bool) {
	img, ok := s.images[name]
	if !ok || img.Build == nil {
		return nil, false
	}
	return &unit{kind: KindImage, name: name, typ: img.Type, image: name,
		guest: img.Build.Guest, blocks: img.Build.Blocks,
		blocksPath: "/spec/build/blocks", guestPath: "/spec/build/guest"}, true
}

func (s *Set) renderUnit(u *unit, total *budget) (*Rendered, error) {
	errf := func(path, format string, args ...any) Errors {
		return Errors{{Kind: u.kind, Name: u.name, Path: path, Msg: fmt.Sprintf(format, args...)}}
	}
	if errs := s.checkUnit(u); len(errs) > 0 {
		return nil, errs
	}
	t := u
	target := u.name
	sch := mustSchemas()
	out := &Rendered{Target: target, Kind: wire.Kind(t.typ), Image: t.image, AllowRestart: t.allowRestart}
	if t.adopted != nil {
		a := *t.adopted
		out.Adopted = &a
	}
	if u.kind == KindImage {
		out.Target = ""
	}
	out.Guest = wire.Resource{
		ID:    wire.GuestConfigID,
		Type:  GuestType,
		Attrs: withDefaults(t.guest, sch.guestDefaults),
	}
	if u.kind == KindImage {
		out.Guest.Attrs = withDefaults(withDefaults(t.guest, sch.buildGuestDefaults), sch.guestDefaults)
	}
	if out.Kind == wire.KindLXC {
		// The default bootstrap user is a cloud-init setting; an LXC
		// Target has none (D-226).
		delete(out.Guest.Attrs, "user")
	}

	bud := newBudget(s.limits, total)
	nonce := newNonce()          // markers of this render only (D-088)
	where := map[string]string{} // resource id → path of its origin
	byID := map[string]*wire.Resource{}
	first := make([]int, len(t.blocks)+1) // use i has out.Resources[first[i]:first[i+1]]
	for i, use := range t.blocks {
		first[i] = len(out.Resources)
		b := s.blocks[use.Use]
		params := templateData(withDefaults(use.Params, b.defaults)).(map[string]any)
		for p := range b.secrets {
			if name, ok := refName(params[p]); ok && name != "" {
				params[p] = secretValue{marker: renderMarker(nonce, name)}
			}
		}
		usePath := ptr(u.blocksPath, i)
		for j, br := range b.resources {
			origin := fmt.Sprintf("%s (Block %q /spec/resources/%d)", usePath, use.Use, j)
			rt, _ := resource.Lookup(br.Type)
			if !rt.Supports(out.Kind) {
				return nil, errf(usePath, "Block %q /spec/resources/%d: resource type %s does not support %s targets",
					use.Use, j, br.Type, t.typ)
			}
			attrs, err := renderValue(br.Attrs, params, bud, ptr("/spec/resources", j, br.Type))
			if err == nil {
				attrs, err = finalizeMarkers(attrs, nonce, ptr("/spec/resources", j, br.Type))
			}
			if err != nil {
				e := errf(usePath, "Block %q %v", use.Use, err)
				e[0].Err = err
				return nil, e
			}
			am := attrs.(map[string]any)
			// A key attribute names the resource and restart_on names
			// others: both are visible everywhere, so no secret (D-088).
			for _, attr := range []string{rt.Key, "restart_on"} {
				names := map[string]bool{}
				stableNames(am[attr], names)
				if len(names) > 0 {
					return nil, errf(usePath, "Block %q /spec/resources/%d: %s cannot hold a secret", use.Use, j, attr)
				}
			}
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
	first[len(t.blocks)] = len(out.Resources)
	for i := range out.Resources {
		byID[out.Resources[i].ID] = &out.Resources[i]
	}

	// restarts (D-172): the resources of a use go into restart_on of the
	// services that its Block names, in Target order. This runs before the
	// overrides, so an override of restart_on still replaces the full list
	// (D-053). The list keeps its JSON form ([]any of strings): the checks
	// below and the Plan read it like a list from a Block.
	for i, use := range t.blocks {
		for _, id := range s.blocks[use.Use].restarts {
			svc, ok := byID[id]
			if !ok {
				return nil, errf(ptr(u.blocksPath, i),
					"Block %q (use %q) restarts %s, but %s is not on this target; add the Block that provides it",
					use.Use, use.id(), id, id)
			}
			list, isList := svc.Attrs["restart_on"].([]any)
			if _, has := svc.Attrs["restart_on"]; has && !isList {
				continue // not a list: the type schema reports it below
			}
			have := map[string]bool{svc.ID: true} // never the service itself
			for _, v := range list {
				if s, ok := v.(string); ok {
					have[s] = true
				}
			}
			list = append([]any(nil), list...)
			for _, r := range out.Resources[first[i]:first[i+1]] {
				if !have[r.ID] {
					have[r.ID] = true
					list = append(list, r.ID)
				}
			}
			svc.Attrs["restart_on"] = list
		}
	}
	byID[wire.GuestConfigID] = &out.Guest

	// Overrides replace single rendered attributes. They are not templates.
	ids := make([]string, 0, len(t.overrides))
	for id := range t.overrides {
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
		for attr, v := range t.overrides[id] {
			if _, isRef := refName(v); isRef {
				return nil, errf(ptr(path, attr), "an override cannot hold a secret reference (D-088)")
			}
			if attr == keyAttr && !reflect.DeepEqual(v, r.Attrs[attr]) {
				return nil, errf(ptr(path, attr), "an override cannot change the key attribute; it would change the resource id")
			}
			r.Attrs[attr] = deepCopy(v)
		}
	}

	// An attribute with a secret marker is sensitive (D-088). It is
	// computed after the overrides, so an override with a literal makes
	// the attribute plain again.
	for i := range out.Resources {
		r := &out.Resources[i]
		var sens []string
		for attr, v := range r.Attrs {
			names := map[string]bool{}
			stableNames(v, names)
			if len(names) > 0 {
				sens = append(sens, attr)
			}
		}
		sort.Strings(sens)
		r.Sensitive = sens
	}

	// Every attribute set must match the schema of its type.
	if err := sch.guest.Validate(out.Guest.Attrs); err != nil {
		return nil, schemaErrors(u.kind, target, u.guestPath, err)
	}
	for _, r := range out.Resources {
		if err := sch.resources[r.Type].Validate(r.Attrs); err != nil {
			errs := schemaErrors(u.kind, target, "", err)
			for _, e := range errs {
				e.Msg = fmt.Sprintf("resource %s from %s: attribute %s: %s", r.ID, where[r.ID], e.Path, e.Msg)
				e.Path = u.blocksPath
				if _, over := t.overrides[r.ID]; over {
					e.Msg += " (the resource has overrides)"
				}
			}
			return nil, errs
		}
	}

	// restart_on must name resources of this target that the agent applies.
	for _, r := range out.Resources {
		for k, id := range wire.RestartOn(r) {
			if _, ok := byID[id]; !ok || id == wire.GuestConfigID || id == r.ID {
				return nil, errf(u.blocksPath, "resource %s from %s: restart_on/%d: %q is not another resource on this target",
					r.ID, where[r.ID], k, id)
			}
		}
	}

	// after (D-167, D-168): each id is a resource of this target that the
	// agent applies, from another use. The first resource of the use
	// carries the ids; the rest of the use follows by the listed order.
	for i, use := range t.blocks {
		after := s.blocks[use.Use].after
		if len(after) == 0 {
			continue
		}
		own := map[string]bool{}
		for _, r := range out.Resources[first[i]:first[i+1]] {
			own[r.ID] = true
		}
		for _, id := range after {
			switch _, ok := byID[id]; {
			case !ok || id == wire.GuestConfigID:
				return nil, errf(ptr(u.blocksPath, i),
					"Block %q (use %q) comes after %s, but %s is not on this target; add the Block that provides it",
					use.Use, use.id(), id, id)
			case own[id]:
				return nil, errf(ptr(u.blocksPath, i),
					"Block %q (use %q) comes after %s, which is its own resource; resources of one Block already apply in the listed order",
					use.Use, use.id(), id)
			}
		}
		out.Resources[first[i]].After = append([]string(nil), after...)
	}

	// No resource may write, link or remove the agent's own paths, or
	// manage its service (D-207). A stored revision from before this rule still loads; the
	// agent refuses the resource (T-060).
	if !s.stored {
		if c := wire.AgentConflicts(out.Resources); len(c) > 0 {
			if c[0].Attr == "name" {
				return nil, errf(u.blocksPath, "resource %s from %s: the service %q is the Plumb agent; a Block cannot stop, mask or change it (D-207)",
					c[0].ID, where[c[0].ID], c[0].Value)
			}
			return nil, errf(u.blocksPath, "resource %s from %s: %s resolves to %q, a path of the Plumb agent; a Block cannot change the agent's files (D-207)",
				c[0].ID, where[c[0].ID], c[0].Attr, c[0].Value)
		}
	}
	// A secret in the argv of a command is readable by every user of the
	// guest in /proc/<pid>/cmdline while it runs (T-094 review L1). Put it
	// in a file with mode 0600 and let the command read the file.
	for _, r := range out.Resources {
		if r.Type == "command" && slices.Contains(r.Sensitive, "argv") {
			return nil, errf(u.blocksPath, "resource %s from %s: argv cannot hold a secret; every user of the guest can read it while the command runs. Write the secret to a file with mode 0600 and let the command read it",
				r.ID, where[r.ID])
		}
	}
	// An Image build holds no secret (D-202). The use check refuses every
	// reference; this catches a marker that came in any other way (N2).
	if u.kind == KindImage {
		for _, r := range out.Resources {
			if len(r.Sensitive) > 0 {
				return nil, errf(u.blocksPath, "resource %s from %s: an Image cannot hold a secret (D-202)", r.ID, where[r.ID])
			}
			// A build starts without a person (D-101, D-199), so it must
			// not run a command, which only a person approves (D-260).
			if rt, ok := resource.Lookup(r.Type); ok && rt.NotInImage {
				return nil, errf(u.blocksPath, "resource %s from %s: an Image build cannot hold a %s resource; only a person approves it, and a build starts without one (D-260)",
					r.ID, where[r.ID], r.Type)
			}
		}
	}

	// The apply order must exist: no cycle through the listed order of a
	// use, restart_on and after. wire.Order is the rule that the agent uses
	// too (D-168). A stored revision from before this check keeps its
	// cycle, so that it loads and a proposal can repair it.
	if _, err := wire.Order(out.Resources); err != nil {
		var oe *wire.OrderError
		if !s.stored || !errors.As(err, &oe) || oe.Reason != wire.OrderCycle {
			return nil, errf(u.blocksPath, "%v", err)
		}
		out.cycle = err.Error()
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
