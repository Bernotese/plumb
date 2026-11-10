package state

import "sort"

// RuleAllTargets is the target of a Rule for every target, also future ones.
const RuleAllTargets = "*"

// RuleIgnore is the only Rule action (D-092).
const RuleIgnore = "ignore"

// Rule is a drift rule (D-092): the controller drops matching drift at
// intake. A Plan still shows and applies the difference.
type Rule struct {
	Name     string   `json:"-"`
	Action   string   `json:"action"`
	Target   string   `json:"target"`   // a Target name or "*"
	Resource string   `json:"resource"` // exact resource id
	Attrs    []string `json:"attrs"`    // empty: every attribute
}

// Matches reports whether the rule covers one attribute of a resource of
// a target. It looks at the attribute name only, so a sensitive change
// matches too.
func (r Rule) Matches(target, resource, attr string) bool {
	if r.Action != RuleIgnore || r.Resource != resource || (r.Target != RuleAllTargets && r.Target != target) {
		return false
	}
	if len(r.Attrs) == 0 {
		return true
	}
	for _, a := range r.Attrs {
		if a == attr {
			return true
		}
	}
	return false
}

// Rules returns the Rules of the set, sorted by name.
func (s *Set) Rules() []Rule {
	out := make([]Rule, 0, len(s.rules))
	for _, name := range sortedKeys(s.rules) {
		r := s.rules[name]
		r.Attrs = append([]string(nil), r.Attrs...)
		out = append(out, r)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
