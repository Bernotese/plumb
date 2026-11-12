package state

import (
	"errors"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// CheckBlockAttrs checks the resource attributes of the named Blocks at
// save, also when no Target or Image uses them (T-101). Render checks the
// rendered attributes again, in full, for every use.
//
// The rule, without render:
//   - every attribute must be a property of the schema of its type;
//   - every attribute value must match the schema of that property;
//   - a string that holds a template action ("{{") renders to a string
//     whose content is unknown now: only a "type" error is kept for it
//     (a template never renders to an integer, a boolean or a list);
//   - rules over the whole object (required, if/then/else) depend on
//     parameters and defaults, so render checks them.
//
// Errors have Kind Block, the Block name and a path under
// /spec/resources/<i>/<type>/<attr>. plan checks only the Blocks that a
// proposal adds or changes, so a stored Block still loads (Validate does
// not call this).
func (s *Set) CheckBlockAttrs(names []string) Errors {
	sch := mustSchemas()
	var errs Errors
	for _, name := range names {
		b, ok := s.blocks[name]
		if !ok {
			continue
		}
		for i, br := range b.resources {
			props := sch.props[br.Type]
			attrs := make([]string, 0, len(br.Attrs))
			for a := range br.Attrs {
				attrs = append(attrs, a)
			}
			sort.Strings(attrs)
			for _, attr := range attrs {
				path := ptr("/spec/resources", i, br.Type, attr)
				sub, ok := props[attr]
				if !ok {
					errs = append(errs, &Error{Kind: KindBlock, Name: name, Path: path,
						Msg: "unknown attribute " + attr + " of resource type " + br.Type})
					continue
				}
				v := br.Attrs[attr]
				err := sub.Validate(v)
				if err == nil {
					continue
				}
				for _, e := range schemaErrors(KindBlock, name, path, err) {
					if templated(v, strings.TrimPrefix(e.Path, path)) && !typeError(err, strings.TrimPrefix(e.Path, path)) {
						continue
					}
					errs = append(errs, e)
				}
			}
		}
	}
	return errs.sorted()
}

// templated reports whether the value at the JSON pointer loc inside v is
// a string with a template action.
func templated(v any, loc string) bool {
	node, ok := at(v, loc)
	if !ok {
		return false
	}
	s, isStr := node.(string)
	return isStr && strings.Contains(s, "{{")
}

// at returns the value at a JSON pointer ("" is v itself).
func at(v any, loc string) (any, bool) {
	if loc == "" {
		return v, true
	}
	for _, tok := range strings.Split(strings.TrimPrefix(loc, "/"), "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch n := v.(type) {
		case map[string]any:
			next, ok := n[tok]
			if !ok {
				return nil, false
			}
			v = next
		case []any:
			i := 0
			for _, c := range tok {
				if c < '0' || c > '9' {
					return nil, false
				}
				i = i*10 + int(c-'0')
			}
			if i >= len(n) {
				return nil, false
			}
			v = n[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// typeError reports whether a leaf error at loc is of kind "type".
func typeError(err error, loc string) bool {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return false
	}
	found := false
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			if _, isType := e.ErrorKind.(*kind.Type); isType && instancePath(e.InstanceLocation) == loc {
				found = true
			}
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	return found
}

func instancePath(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	toks := make([]any, len(tokens))
	for i, t := range tokens {
		toks[i] = t
	}
	return ptr("", toks...)
}
