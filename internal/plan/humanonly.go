package plan

import (
	"bytes"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// Reasons why only a human session may approve a Plan (D-099).
const (
	HumanOnlyRuleChange    = "rule-change"
	HumanOnlySecretBinding = "secret-binding" // T-029
	// HumanOnlyAcceptGuestData: the Plan adopts values that a guest
	// reported (Accept of an Issue, also "Accept as raw"). A compromised
	// guest can report valid-looking values, so a person must see them
	// (D-323). Set from the origin, which only the server sets.
	HumanOnlyAcceptGuestData = "accept-guest-data"
	// HumanOnlyUnverifiedPin: a pin_refresh Plan from a list without a
	// signature, with a signature that is not strictly newer than the last
	// accepted one (also the first refresh), or to a pin this Image used
	// before (D-335).
	HumanOnlyUnverifiedPin = "unverified-pin"
	// HumanOnlyFirewallOff: the Plan removes a firewall that is on in the
	// base (D-368): the resource leaves the render (its Block use or Block
	// goes, the Target is deleted) or gets state absent. That opens every
	// port at once, so a person must see it, whatever made the Plan.
	HumanOnlyFirewallOff = "firewall-off"
	// HumanOnlyRepair: every repair Plan (D-372, D-385). The Run puts a
	// new binary and a new credential into the guest as root, so a person
	// approves it, whatever the token tier.
	HumanOnlyRepair = "repair"
)

// firewallType is the resource type of the guest firewall (D-295).
const firewallType = "firewall"

// firewallOff reports whether the Plan from base to next turns off a
// firewall of one of the touched targets (D-368, ARCHITECTURE §18): a
// firewall resource that is in the base render and not absent there is
// gone from the next render, or absent in it. A Target that is deleted
// counts too: its firewall goes with it. A firewall that was absent in
// the base is off already.
func firewallOff(base, next *state.Set, targets []string) (bool, error) {
	for _, t := range targets {
		before, err := renderOrNil(base, t)
		if err != nil || before == nil {
			if err != nil {
				return false, err
			}
			continue
		}
		var after *state.Rendered
		for _, r := range before.Resources {
			if r.Type != firewallType || r.Attrs[wire.AttrState] == wire.StateAbsent {
				continue
			}
			if after == nil {
				if after, err = renderOrNil(next, t); err != nil {
					return false, err
				}
				if after == nil {
					return true, nil // the Target is deleted
				}
			}
			still := false
			for _, n := range after.Resources {
				if n.ID == r.ID {
					still = n.Attrs[wire.AttrState] != wire.StateAbsent
					break
				}
			}
			if !still {
				return true, nil
			}
		}
	}
	return false, nil
}

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
