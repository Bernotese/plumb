package state

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/Bernotese/plumb/internal/resource"
)

// The copy rule (D-331, D-350): which parts of a value are compared. The
// plan package calls it (it must not import resource, ARCHITECTURE §2).

// MaxGuestElems bounds the elements taken from one value, so a huge list
// cannot make a report or a check slow. Past it the caller fails closed
// (review K1 of #248): a report flags its Target, a Plan is human-only.
const MaxGuestElems = 4096

type attrRule struct {
	skip  bool   // enum or const: a fixed value, never compared
	sep   string // x-plumb-list separator; "" = not list-like
	lines bool   // x-plumb-lines: a text whose new lines count too (D-359)
}

var (
	attrRulesOnce sync.Once
	attrRules     map[string]map[string]attrRule // type → attr → rule
)

func loadAttrRules() {
	attrRules = map[string]map[string]attrRule{}
	for _, name := range resource.Names() {
		typ, _ := resource.Lookup(name)
		var head struct {
			Properties map[string]struct {
				Enum  json.RawMessage `json:"enum"`
				Const json.RawMessage `json:"const"`
				List  string          `json:"x-plumb-list"`
				Lines bool            `json:"x-plumb-lines"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(typ.Schema(), &head); err != nil {
			panic("state: schema of resource " + name + ": " + err.Error()) // build mistake; the tests catch it
		}
		m := map[string]attrRule{}
		for attr, p := range head.Properties {
			m[attr] = attrRule{skip: p.Enum != nil || p.Const != nil, sep: p.List, lines: p.Lines}
		}
		attrRules[name] = m
	}
}

// GuestValueElems returns the parts of value v of attribute attr of a
// resource of type typ that the copy rule hashes, sorted, without
// duplicates: nothing for a boolean, a number, null or an attribute with
// enum or const in its schema; each element of a list (an object as
// canonical JSON); each element of a list-like string (x-plumb-list),
// after the normalization of Diff; the source elements of an observed
// text that shows a list (resource.ObservedList); else the string itself.
// over reports more than MaxGuestElems distinct elements: elems then
// holds only the first ones, and the caller must fail closed.
func GuestValueElems(typ, attr string, v any) (elems []string, over bool) {
	attrRulesOnce.Do(loadAttrRules)
	rule := attrRules[typ][attr]
	if rule.skip {
		return nil, false
	}
	set := map[string]bool{}
	add := func(s string) {
		if s == "" || set[s] {
			return
		}
		if len(set) >= MaxGuestElems {
			over = true
			return
		}
		set[s] = true
	}
	switch v := v.(type) {
	case string:
		if list, ok := resource.ObservedList(typ, attr, v); ok {
			for e := range strings.SplitSeq(list, ",") {
				add(strings.TrimSpace(e))
			}
			break
		}
		if rule.sep == "" {
			add(v)
			break
		}
		for e := range strings.SplitSeq(resource.NormalizeList(typ, attr, v), rule.sep) {
			add(strings.TrimSpace(e))
		}
	case []any:
		for _, e := range v {
			switch e := e.(type) {
			case string:
				add(e)
			case map[string]any, []any:
				b, _ := json.Marshal(e) // map keys sorted
				add(string(b))
			}
		}
	case []string:
		for _, e := range v {
			add(e)
		}
	case map[string]any:
		b, _ := json.Marshal(v)
		add(string(b))
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, over
}

// Opaque reports whether attr of type typ is an observed status text, not
// a value (resource.Type.Opaque, D-359).
func Opaque(typ, attr string) bool {
	t, ok := resource.Lookup(typ)
	return ok && slices.Contains(t.Opaque, attr)
}

// TypeWide reports whether the copy rule treats every new value of type
// typ (except its key) as guest data while an opaque change of the type
// counts (D-359), and returns the key attribute.
func TypeWide(typ string) (key string, ok bool) {
	t, found := resource.Lookup(typ)
	if !found || len(t.Opaque) == 0 {
		return "", false
	}
	return t.Key, true
}

// OpaqueValue is the text whose keyed hash stands for "the guest changed
// something of type typ that has no value" (D-359). The NUL bytes keep it
// apart from normal values; a guest that reports this exact text only
// makes more Plans human-only (fail closed).
func OpaqueValue(typ string) string { return "\x00plumb-opaque\x00" + typ }

// MinLineChars is the shortest line (non-space characters) that the copy
// rule hashes in a text attribute (x-plumb-lines): shorter lines ("}",
// "fi", "[Unit]") are in every file and would only make noise.
const MinLineChars = 8

// GuestValueLines returns the lines of text v that text other does not
// have, for an attribute marked x-plumb-lines (file.content, unit.content;
// D-359, review O2 of #264): normalized (inner spaces and tabs collapsed to
// one space, lower case; review W1 of #264), at least MinLineChars
// non-space characters, sorted, without duplicates. Only verbatim or
// normalized copies match: an edited, split or commented line does not
// (the opaque-type rule is the fail-closed part). The bound MaxGuestElems counts
// only these new lines, so a big unchanged file never overflows; over
// reports more, and the caller fails closed. Every other attribute, and a
// value that is not a string, gives nothing.
func GuestValueLines(typ, attr string, v, other any) (lines []string, over bool) {
	attrRulesOnce.Do(loadAttrRules)
	if !attrRules[typ][attr].lines {
		return nil, false
	}
	text, ok := v.(string)
	if !ok {
		return nil, false
	}
	have := map[string]bool{}
	if o, ok := other.(string); ok {
		for l := range strings.SplitSeq(o, "\n") {
			have[normLine(l)] = true
		}
	}
	set := map[string]bool{}
	for l := range strings.SplitSeq(text, "\n") {
		l = normLine(l)
		if have[l] || set[l] || len(strings.ReplaceAll(l, " ", "")) < MinLineChars {
			continue
		}
		if len(set) >= MaxGuestElems {
			over = true
			break
		}
		set[l] = true
	}
	for l := range set {
		lines = append(lines, l)
	}
	sort.Strings(lines)
	return lines, over
}

// normLine is the compared form of a line: white space runs (spaces,
// tabs, the \r of CRLF) collapsed to one space, trimmed, lower case.
func normLine(l string) string { return strings.ToLower(strings.Join(strings.Fields(l), " ")) }

// Scalar reports whether the copy rule compares value v of attr as a whole
// in a type-wide check (D-359, review O1 of #264): enum and const values,
// booleans and numbers, which GuestValueElems skips.
func Scalar(typ, attr string, v any) bool {
	attrRulesOnce.Do(loadAttrRules)
	if attrRules[typ][attr].skip {
		return true
	}
	switch v.(type) {
	case bool, float64, float32, int, int64, json.Number:
		return true
	}
	return false
}
