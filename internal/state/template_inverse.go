package state

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/Bernotese/plumb/internal/wire"
)

// Accept to a Block parameter (D-362). Accept maps the observed values of
// an Issue back to the Block parameters of the Target when a strict
// inverse of the attribute's template exists and a new render proves it:
// the new render gives the observed values and changes nothing else.
// Everything else falls back to raw mode (an override, D-296).

// Accept modes of an Issue (Issue.accept_mode).
const (
	AcceptParam = "param"
	AcceptRaw   = "raw"
	AcceptNone  = "none"
	AcceptGuest = "guest"
)

// ParamValue is one Block parameter that an Accept sets: spec.blocks[Index]
// (use id Use) gets params[Param] = Value.
type ParamValue struct {
	Use   string
	Index int
	Param string
	Value any
}

// AcceptPlan is the result of AcceptParams. Mode is AcceptParam or
// AcceptRaw; Reason says why in plain words, without guest values.
type AcceptPlan struct {
	Mode   string
	Reason string
	Params []ParamValue
}

// Reasons for raw mode. They hold no guest value (the Issue shows those).
const (
	reasonNoBlock     = "no Block renders this resource"
	reasonOverride    = "an override already fixes this attribute"
	reasonNotTemplate = "the Block sets this attribute without a parameter"
	reasonShape       = "the template of this attribute is more than one plain parameter between fixed text"
	reasonNoValue     = "the guest value does not have the fixed text of the template"
	reasonType        = "the guest value does not fit the parameter"
	reasonSecret      = "the parameter is a secret"
	reasonConflict    = "two attributes need different values of the same parameter"
	reasonSideEffect  = "the parameter also changes another resource or attribute"
	reasonNotProven   = "a new render with the parameter does not give the guest value"
	reasonNoChange    = "the Issue has no observed value"
)

func rawPlan(reason, attr string) AcceptPlan {
	if attr != "" {
		reason = "`" + attr + "`: " + reason
	}
	return AcceptPlan{Mode: AcceptRaw, Reason: reason}
}

// AcceptParams decides whether Accept of the changes of resource id on
// target can set Block parameters instead of an override (D-362). It
// renders the Target again with the new values and returns AcceptParam
// only when that render gives the observed value of every change and
// leaves every other resource and attribute of the Target as it is. The
// restart pseudo attribute is ignored, as in Accept. Every error is a raw
// plan with its reason; the function never returns a wrong mapping.
func (s *Set) AcceptParams(target, id string, changes []wire.Change) AcceptPlan {
	t, ok := s.targets[target]
	if !ok {
		return rawPlan(reasonNoBlock, "")
	}
	base, err := s.Render(target)
	if err != nil {
		return rawPlan(reasonNoBlock, "")
	}
	// The resource, its use and its index among the resources of the use.
	useID, pos := "", -1
	for i, r := range base.Resources {
		if r.ID == id {
			useID = r.Block
			for j := i; j >= 0 && base.Resources[j].Block == useID; j-- {
				pos++
			}
			break
		}
	}
	index := -1
	for i, u := range t.Blocks {
		if u.id() == useID {
			index = i
		}
	}
	if useID == "" || index < 0 {
		return rawPlan(reasonNoBlock, "")
	}
	use := t.Blocks[index]
	b := s.blocks[use.Use]
	if b == nil || pos < 0 || pos >= len(b.resources) {
		return rawPlan(reasonNoBlock, "")
	}
	br := b.resources[pos]

	values := map[string]any{} // parameter → new value
	want := map[string]any{}   // attribute → observed value
	for _, c := range changes {
		if c.Attr == wire.AttrRestart {
			continue
		}
		if c.Sensitive {
			return rawPlan(reasonSecret, c.Attr)
		}
		if c.From == nil {
			return rawPlan(reasonNoChange, c.Attr)
		}
		if _, over := t.Overrides[id][c.Attr]; over {
			return rawPlan(reasonOverride, c.Attr)
		}
		text, ok := br.Attrs[c.Attr].(string)
		if !ok || !strings.Contains(text, "{{") {
			return rawPlan(reasonNotTemplate, c.Attr)
		}
		param, prefix, suffix, ok := inverseShape(text)
		if !ok {
			return rawPlan(reasonShape, c.Attr)
		}
		if b.secrets[param] || !b.props[param] {
			return rawPlan(reasonSecret, c.Attr)
		}
		observed, ok := c.From.(string)
		if !ok {
			observed, ok = numberText(c.From)
		}
		if !ok || len(observed) < len(prefix)+len(suffix) ||
			!strings.HasPrefix(observed, prefix) || !strings.HasSuffix(observed, suffix) {
			return rawPlan(reasonNoValue, c.Attr)
		}
		mid := observed[len(prefix) : len(observed)-len(suffix)]
		v, ok := s.paramValue(t, index, param, mid, prefix == "" && suffix == "")
		if !ok {
			return rawPlan(reasonType, c.Attr)
		}
		if old, dup := values[param]; dup && !reflect.DeepEqual(old, v) {
			return rawPlan(reasonConflict, c.Attr)
		}
		values[param] = v
		want[c.Attr] = c.From
	}
	if len(values) == 0 {
		return rawPlan(reasonNoChange, "")
	}

	next, ok := s.renderWith(target, index, values)
	if !ok {
		return rawPlan(reasonType, "")
	}
	if attr, ok := sameExcept(base, next, id, want); !ok {
		if attr != "" {
			return rawPlan(reasonNotProven, attr)
		}
		return rawPlan(reasonSideEffect, "")
	}
	out := AcceptPlan{Mode: AcceptParam}
	for _, p := range sortedKeys(values) {
		out.Params = append(out.Params, ParamValue{Use: useID, Index: index, Param: p, Value: values[p]})
	}
	return out
}

// inverseShape returns the parameter and the literal prefix and suffix of
// a template that is exactly [text] {{ .p }} [text]: one action with one
// plain field of one top-level name, no pipeline, no function, no
// variable, no trim marker and no comment. ok is false for anything else.
func inverseShape(text string) (param, prefix, suffix string, ok bool) {
	// Trim markers change the text nodes at parse time, and a comment
	// leaves no node: both would make prefix or suffix lie.
	if strings.Count(text, "{{") != 1 || strings.Count(text, "}}") != 1 ||
		strings.Contains(text, "{{-") || strings.Contains(text, "-}}") || strings.Contains(text, "/*") {
		return "", "", "", false
	}
	t, err := parseTemplate(text, nil)
	if err != nil || t.Tree == nil || t.Tree.Root == nil {
		return "", "", "", false
	}
	nodes := t.Tree.Root.Nodes
	at := -1
	for i, n := range nodes {
		switch n.(type) {
		case *parse.TextNode:
		case *parse.ActionNode:
			if at >= 0 {
				return "", "", "", false
			}
			at = i
		default:
			return "", "", "", false
		}
	}
	if at < 0 || at > 1 || len(nodes)-at > 2 {
		return "", "", "", false
	}
	a := nodes[at].(*parse.ActionNode)
	if a.Pipe == nil || len(a.Pipe.Decl) > 0 || a.Pipe.IsAssign || len(a.Pipe.Cmds) != 1 || len(a.Pipe.Cmds[0].Args) != 1 {
		return "", "", "", false
	}
	f, isField := a.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
	if !isField || len(f.Ident) != 1 {
		return "", "", "", false
	}
	if at == 1 {
		prefix = string(nodes[0].(*parse.TextNode).Text)
	}
	if at+1 < len(nodes) {
		suffix = string(nodes[at+1].(*parse.TextNode).Text)
	}
	// The literal text must be the template text around the action, byte
	// for byte (no escape or trim changed it).
	open, close := strings.Index(text, "{{"), strings.LastIndex(text, "}}")
	if text[:open] != prefix || text[close+2:] != suffix {
		return "", "", "", false
	}
	return f.Ident[0], prefix, suffix, true
}

// paramValue returns the value for parameter p of the use: the string, or,
// when whole is set (no literal text), a strict integer or boolean when
// the string does not pass the parameter schema. ok is false when no form
// passes the schema of the Block parameters.
func (s *Set) paramValue(t *targetSpec, index int, p, text string, whole bool) (any, bool) {
	cands := []any{text}
	if whole {
		if n, err := strconv.ParseInt(text, 10, 64); err == nil && strconv.FormatInt(n, 10) == text {
			cands = append(cands, json.Number(text))
		}
		if text == "true" || text == "false" {
			cands = append(cands, text == "true")
		}
	}
	b := s.blocks[t.Blocks[index].Use]
	for _, v := range cands {
		params := withDefaults(t.Blocks[index].Params, b.defaults)
		params[p] = v
		if b.params == nil || b.params.Validate(params) == nil {
			return v, true
		}
	}
	return nil, false
}

// renderWith renders the Target with values set in the params of use
// index. The stored Target does not change.
func (s *Set) renderWith(target string, index int, values map[string]any) (*Rendered, bool) {
	u, ok := s.targetUnit(target)
	if !ok {
		return nil, false
	}
	uses := make([]blockUse, len(u.blocks))
	copy(uses, u.blocks)
	params := map[string]any{}
	for k, v := range uses[index].Params {
		params[k] = deepCopy(v)
	}
	for k, v := range values {
		params[k] = v
	}
	uses[index].Params = params
	u.blocks = uses
	r, err := s.renderUnit(u, nil)
	return r, err == nil
}

// sameExcept reports whether next equals base in everything except the
// attributes of resource id in want, which must equal the observed values.
// attr names the first attribute of id that did not become the observed
// value; it is empty for a change anywhere else.
func sameExcept(base, next *Rendered, id string, want map[string]any) (attr string, ok bool) {
	if !reflect.DeepEqual(base.Guest, next.Guest) || len(base.Resources) != len(next.Resources) {
		return "", false
	}
	for i := range base.Resources {
		a, b := base.Resources[i], next.Resources[i]
		if a.ID != b.ID || a.Type != b.Type || a.Block != b.Block ||
			!reflect.DeepEqual(a.After, b.After) || !reflect.DeepEqual(a.Sensitive, b.Sensitive) {
			return "", false
		}
		if a.ID != id {
			if !reflect.DeepEqual(a.Attrs, b.Attrs) {
				return "", false
			}
			continue
		}
		for _, k := range sortedKeys(want) {
			if !sameValue(b.Attrs[k], want[k]) {
				return k, false
			}
		}
		if len(a.Attrs) != len(b.Attrs) {
			return "", false
		}
		for k, v := range a.Attrs {
			if _, changed := want[k]; changed {
				continue
			}
			if !reflect.DeepEqual(v, b.Attrs[k]) {
				return "", false
			}
		}
	}
	return "", true
}

// sameValue compares a rendered value with an observed one. A template
// renders text, and a guest may report a number for it: a number equals
// a string with exactly its canonical text.
func sameValue(rendered, observed any) bool {
	if reflect.DeepEqual(rendered, observed) {
		return true
	}
	rs, ok1 := rendered.(string)
	os, ok2 := numberText(observed)
	if !ok2 {
		os, ok2 = observed.(string)
	}
	return ok1 && ok2 && rs == os
}

// numberText returns the canonical text of a JSON number.
func numberText(v any) (string, bool) {
	switch n := v.(type) {
	case json.Number:
		return n.String(), true
	case float64:
		if n == float64(int64(n)) {
			return strconv.FormatInt(int64(n), 10), true
		}
		return strconv.FormatFloat(n, 'f', -1, 64), true
	case int:
		return strconv.Itoa(n), true
	case int64:
		return strconv.FormatInt(n, 10), true
	}
	return "", false
}
