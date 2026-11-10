package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// The ignore actions (D-091, D-092).
const (
	ActionIgnoreOnce = "ignore_once"
	ActionIgnoreRule = "ignore_rule"
)

// ruleCache holds the Rules of one head revision.
type ruleCache struct {
	mu    sync.Mutex
	head  int64
	rules []state.Rule
}

// headRules returns the Rules of the head revision. It reads only the Rule
// documents, so intake stays cheap.
func (s *Service) headRules(ctx context.Context) ([]state.Rule, error) {
	head, err := s.st.Head(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.rules.mu.Lock()
	defer s.rules.mu.Unlock()
	if s.rules.head == head.ID {
		return s.rules.rules, nil
	}
	docs, err := s.st.RevisionDocs(ctx, head.ID)
	if err != nil {
		return nil, err
	}
	var rules []state.Rule
	for _, d := range docs {
		if d.Kind != state.KindRule {
			continue
		}
		var doc struct {
			Spec state.Rule `json:"spec"`
		}
		if err := json.Unmarshal(d.Body, &doc); err != nil {
			return nil, fmt.Errorf("issue: rule %s: %w", d.Name, err)
		}
		doc.Spec.Name = d.Name
		rules = append(rules, doc.Spec)
	}
	s.rules.head, s.rules.rules = head.ID, rules
	return rules, nil
}

// filterRules splits the changes of one resource into those that no rule
// covers and, per rule, the attributes that it dropped (D-092).
func filterRules(rules []state.Rule, target, resource string, changes []wire.Change) (kept []wire.Change, dropped map[string][]string) {
	for _, c := range changes {
		matched := ""
		for _, r := range rules {
			if r.Matches(target, resource, c.Attr) {
				matched = r.Name
				break
			}
		}
		if matched == "" {
			kept = append(kept, c)
			continue
		}
		if dropped == nil {
			dropped = map[string][]string{}
		}
		dropped[matched] = append(dropped[matched], c.Attr)
	}
	return kept, dropped
}

func (s *Service) recordDropped(ctx context.Context, target, resource string, dropped map[string][]string) error {
	for rule, attrs := range dropped {
		if err := s.st.RecordIgnored(ctx, target, resource, rule, attrs); err != nil {
			return err
		}
	}
	return nil
}

// CanonChanges is the form in which "Ignore once" remembers changes and
// compares later reports: sorted by attribute, without the ignored flag.
func CanonChanges(changes []wire.Change) (json.RawMessage, error) {
	c := make([]wire.Change, len(changes))
	copy(c, changes)
	for i := range c {
		c[i].Ignored = false
	}
	sort.SliceStable(c, func(i, j int) bool { return c[i].Attr < c[j].Attr })
	return json.Marshal(c)
}

// ignoredOnce reports whether "Ignore once" covers exactly these changes.
func (s *Service) ignoredOnce(ctx context.Context, rules []state.Rule, target, resource string, kept []wire.Change) (bool, error) {
	mem, err := s.st.IgnoredOnce(ctx, target, resource)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var old []wire.Change
	if err := json.Unmarshal(mem, &old); err != nil {
		return false, err
	}
	// A rule added later drops the same attributes from both sides.
	old, _ = filterRules(rules, target, resource, old)
	a, err := CanonChanges(old)
	if err != nil {
		return false, err
	}
	b, err := CanonChanges(kept)
	if err != nil {
		return false, err
	}
	return string(a) == string(b), nil
}

// IgnoreCheck decides whether a principal may ignore these changes once
// (D-091). The API passes it; nil allows.
type IgnoreCheck func(changes []wire.Change) error

// IgnoreOnce resolves an open Issue as "ignore" without a Plan (D-091).
// Equal later reports open no Issue until the resource reports unchanged
// or a Run applies it.
func (s *Service) IgnoreOnce(ctx context.Context, id int64, principal string, check IgnoreCheck) (store.Issue, error) {
	iss, err := s.st.GetIssue(ctx, id)
	if err != nil {
		return store.Issue{}, err
	}
	if iss.Status != store.IssueOpen {
		return store.Issue{}, ErrNotOpen
	}
	var changes []wire.Change
	if err := json.Unmarshal(iss.Changes, &changes); err != nil {
		return store.Issue{}, err
	}
	if check != nil {
		if err := check(changes); err != nil {
			return store.Issue{}, err
		}
	}
	canon, err := CanonChanges(changes)
	if err != nil {
		return store.Issue{}, err
	}
	// The row must still hold the changes that check saw (review #53).
	out, err := s.st.IgnoreIssue(ctx, id, principal, iss.Changes, canon)
	if err != nil {
		return store.Issue{}, err
	}
	s.emit(ctx, "issue.resolved", id, principal, map[string]any{"resolution": store.ResolutionIgnore})
	return out, nil
}

// RuleScope is the scope of a new Rule from "Ignore by rule".
type RuleScope struct {
	AllTargets bool
	Attrs      []string // empty: every attribute
}

// RuleName is the name of the Rule that "Ignore by rule" makes for an Issue.
func RuleName(issueID int64) string { return fmt.Sprintf("ignore-issue-%d", issueID) }

// ruleDoc returns the Rule document for an Issue and a scope.
func ruleDoc(iss store.Issue, scope RuleScope) (state.Doc, error) {
	target := iss.Target
	if scope.AllTargets {
		target = state.RuleAllTargets
	}
	spec := map[string]any{"action": state.RuleIgnore, "target": target, "resource": iss.Resource}
	if len(scope.Attrs) > 0 {
		attrs := append([]string{}, scope.Attrs...)
		sort.Strings(attrs)
		spec["attrs"] = attrs
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return state.Doc{}, err
	}
	return state.Doc{Kind: state.KindRule, Name: RuleName(iss.ID), Spec: raw}, nil
}

// ruleProposal is the Plan input of "Ignore by rule" (D-092): one Rule,
// no target change, origin ignore_rule. Only a human approves it (D-099).
func ruleProposal(iss store.Issue, scope RuleScope, principal string) (plan.Input, error) {
	doc, err := ruleDoc(iss, scope)
	if err != nil {
		return plan.Input{}, err
	}
	where := iss.Target
	if scope.AllTargets {
		where = "every target"
	}
	return plan.Input{Author: principal, IssueID: iss.ID, Origin: plan.OriginIgnoreRule,
		Reason:   fmt.Sprintf("ignore drift of %s on %s by rule (issue %d)", iss.Resource, where, iss.ID),
		Proposal: plan.Proposal{Put: []state.Doc{doc}}}, nil
}

// applyRules drops the changes of open Issues that the head rules cover
// (D-092): an Issue with no change left resolves as "ignore" by `by` (the
// approver of a rule change, else system); one with some left keeps the
// rest. Records of removed rules go.
func (s *Service) applyRules(ctx context.Context, by string) {
	rules, err := s.headRules(ctx)
	if err != nil {
		return
	}
	names := make([]string, len(rules))
	for i, r := range rules {
		names[i] = r.Name
	}
	s.st.PruneIgnoredDrift(ctx, names)
	if len(rules) == 0 {
		return
	}
	open, err := s.st.Issues(ctx, "")
	if err != nil {
		return
	}
	for _, iss := range open {
		if iss.Status != store.IssueOpen {
			continue // a Plan resolves it; its Run decides
		}
		var changes []wire.Change
		if json.Unmarshal(iss.Changes, &changes) != nil {
			continue
		}
		kept, dropped := filterRules(rules, iss.Target, iss.Resource, changes)
		if len(dropped) == 0 {
			continue
		}
		if s.recordDropped(ctx, iss.Target, iss.Resource, dropped) != nil {
			continue
		}
		if len(kept) == 0 {
			if s.st.ResolveIssue(ctx, iss.ID, store.ResolutionIgnore, by) == nil {
				s.emit(ctx, "issue.resolved", iss.ID, by, map[string]any{"resolution": store.ResolutionIgnore})
			}
			continue
		}
		if raw, err := json.Marshal(kept); err == nil && s.st.SetIssueChanges(ctx, iss.ID, raw) == nil {
			s.emit(ctx, "issue.updated", iss.ID, by, map[string]any{"risk": wire.MaxRisk(kept)})
		}
	}
}

// forgetApplied ends "Ignore once" for every resource that a succeeded
// Run applied (D-091).
func (s *Service) forgetApplied(ctx context.Context, p store.Plan) {
	var targets []plan.TargetPlan
	if json.Unmarshal(p.Changes, &targets) != nil {
		return
	}
	for _, tp := range targets {
		for _, rc := range tp.Resources {
			if len(rc.Changes) > 0 || rc.Action != "" {
				s.st.ForgetIgnoreOnce(ctx, tp.Target, rc.Resource)
			}
		}
	}
}

// IgnoredDrift returns what the rules of the head revision dropped for a
// target (D-092). Records of removed rules are left out.
func (s *Service) IgnoredDrift(ctx context.Context, target string) ([]store.IgnoredDrift, error) {
	rules, err := s.headRules(ctx)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, r := range rules {
		live[r.Name] = true
	}
	all, err := s.st.IgnoredDriftOf(ctx, target)
	if err != nil {
		return nil, err
	}
	var out []store.IgnoredDrift
	for _, d := range all {
		if live[d.Rule] {
			out = append(out, d)
		}
	}
	return out, nil
}
