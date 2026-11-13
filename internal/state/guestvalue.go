package state

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/Bernotese/plumb/internal/resource"
)

// The copy rule (D-331, D-350): which parts of a value are compared. The
// plan package calls it (it must not import resource, ARCHITECTURE §2).

// MaxGuestElems bounds the elements taken from one value, so a huge list
// cannot make a report or a check slow. A reported value past it adds
// nothing more; the per-Target cap (store.GuestValueCap) is the real limit.
const MaxGuestElems = 4096

type attrRule struct {
	skip bool   // enum or const: a fixed value, never compared
	sep  string // x-plumb-list separator; "" = not list-like
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
			} `json:"properties"`
		}
		if err := json.Unmarshal(typ.Schema(), &head); err != nil {
			panic("state: schema of resource " + name + ": " + err.Error()) // build mistake; the tests catch it
		}
		m := map[string]attrRule{}
		for attr, p := range head.Properties {
			m[attr] = attrRule{skip: p.Enum != nil || p.Const != nil, sep: p.List}
		}
		attrRules[name] = m
	}
}

// GuestValueElems returns the parts of value v of attribute attr of a
// resource of type typ that the copy rule hashes, sorted, without
// duplicates: nothing for a boolean, a number, null or an attribute with
// enum or const in its schema; each element of a list (an object as
// canonical JSON); each element of a list-like string (x-plumb-list),
// after the normalization of Diff; else the string itself.
func GuestValueElems(typ, attr string, v any) []string {
	attrRulesOnce.Do(loadAttrRules)
	rule := attrRules[typ][attr]
	if rule.skip {
		return nil
	}
	set := map[string]bool{}
	add := func(s string) {
		if s != "" && len(set) < MaxGuestElems {
			set[s] = true
		}
	}
	switch v := v.(type) {
	case string:
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
	return out
}
