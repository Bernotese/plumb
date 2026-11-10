package plan

import (
	"bytes"

	"github.com/Bernotese/plumb/internal/state"
)

// Reasons why only a human session may approve a Plan (D-099).
const (
	HumanOnlyRuleChange    = "rule-change"
	HumanOnlySecretBinding = "secret-binding" // T-029
)

// humanOnlyReasons computes Plan.human_only from the content of the
// proposal against its base, for every origin (D-099): a Rule that is
// put with another content than in the base, or deleted, is a
// rule-change. A Rule put unchanged is not.
func humanOnlyReasons(base *state.Set, p Proposal) ([]string, error) {
	old := map[string]state.Doc{}
	for _, d := range base.Docs() {
		if d.Kind == state.KindRule {
			old[d.Name] = d
		}
	}
	for _, k := range p.Delete {
		if k.Kind == state.KindRule {
			return []string{HumanOnlyRuleChange}, nil
		}
	}
	for _, d := range p.Put {
		if d.Kind != state.KindRule {
			continue
		}
		prev, ok := old[d.Name]
		if !ok {
			return []string{HumanOnlyRuleChange}, nil
		}
		a, err := state.Canonical(prev)
		if err != nil {
			return nil, err
		}
		b, err := state.Canonical(d)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(a, b) {
			return []string{HumanOnlyRuleChange}, nil
		}
	}
	return nil, nil
}

// markIgnored flags every planned change that a Rule of the planned state
// covers (D-092), and clears the flag on every other change. The Plan
// still shows and applies it.
func markIgnored(rules []state.Rule, plans []TargetPlan) {
	for i := range plans {
		for j := range plans[i].Resources {
			rc := &plans[i].Resources[j]
			for k := range rc.Changes {
				c := &rc.Changes[k]
				c.Ignored = false
				for _, r := range rules {
					if r.Matches(plans[i].Target, rc.Resource, c.Attr) {
						c.Ignored = true
						break
					}
				}
			}
		}
	}
}
