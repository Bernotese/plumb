// Package issue takes drift reports, keeps Issues and resolves them
// (docs/ARCHITECTURE.md §6). Plumb never resolves drift by itself: every
// resolution is a Plan that a principal approves (D-022). The only
// exception is "gone": the drift went away on its own.
package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Resolve actions (D-044).
const (
	ActionAccept = "accept"
	ActionRevert = "revert"
)

// ErrAcceptSensitive is returned for accept on a sensitive change: the
// controller never knows the value, so it cannot write it (D-037).
var ErrAcceptSensitive = errors.New("issue: accept is not possible for a sensitive attribute; revert it or change the secret")

// ErrNotOpen is returned when an Issue is resolving or resolved already.
var ErrNotOpen = store.ErrIssueNotOpen

// ErrAcceptImage is returned for accept of an image or kind drift: a guest
// cannot take another image or kind, so there is nothing to write.
var ErrAcceptImage = errors.New("issue: accept is not possible for the image or the kind of a guest; revert it or create a new target")

// ErrAddressFix is returned for revert and accept of a guest:address
// Issue: ip is create-only, and the Proxmox config is right; the guest
// does not use it (D-185, D-215). Only the ignore actions apply.
var ErrAddressFix = errors.New("issue: the guest does not use its configured address; Plumb cannot revert or accept this (restart the guest, or ignore it)")

// reopenWindow: a new address mismatch within this time after the Issue
// was gone opens the same Issue again, which then stays open at least this
// long (D-215).
const reopenWindow = time.Hour

// Service keeps Issues.
type Service struct {
	st     *store.Store
	events *event.Log
	plans  *plan.Engine
	rules  ruleCache
}

// New returns the Issue service.
func New(st *store.Store, events *event.Log, plans *plan.Engine) *Service {
	return &Service{st: st, events: events, plans: plans}
}

func (s *Service) emit(ctx context.Context, typ string, id int64, actor string, data any) {
	if s.events != nil {
		s.events.Emit(ctx, typ, fmt.Sprintf("issue:%d", id), actor, data)
	}
}

// Intake is the plan.Config.OnDrift of the controller. The report is
// redacted already (link, D-062; the guest poll has no secrets). A
// differing item opens one Issue per target and resource, or updates the
// open one. An unchanged item resolves an open Issue as "gone".
func (s *Service) Intake(ctx context.Context, target string, rep wire.Report) error {
	actor := "agent:" + target
	var rules []state.Rule
	for _, it := range rep.Items {
		switch it.Status {
		case wire.StatusDiffers:
			if rules == nil {
				var err error
				if rules, err = s.headRules(ctx); err != nil {
					return err
				}
			}
			// Rules drop what they cover; the drop is recorded (D-092).
			kept, dropped := filterRules(rules, target, it.Resource, it.Changes)
			if err := s.recordDropped(ctx, target, it.Resource, dropped); err != nil {
				return err
			}
			if len(kept) == 0 {
				if err := s.dropIssue(ctx, target, it.Resource); err != nil {
					return err
				}
				continue
			}
			// "Ignore once" covers exactly these changes (D-091).
			if ok, err := s.ignoredOnce(ctx, rules, target, it.Resource, kept); err != nil {
				return err
			} else if ok {
				continue
			}
			raw, err := json.Marshal(kept)
			if err != nil {
				return err
			}
			if it.Resource == wire.GuestAddressID {
				// A flapping guest gets one Issue row (D-215).
				iss, err := s.st.ReopenGoneIssue(ctx, target, it.Resource, s.st.Now().Add(-reopenWindow), raw)
				if err == nil {
					s.emit(ctx, "issue.updated", iss.ID, "system", map[string]any{"target": target, "resource": it.Resource, "status": store.IssueOpen, "reopened": true})
					continue
				}
				if !errors.Is(err, store.ErrNotFound) {
					return err
				}
			}
			iss, opened, err := s.st.UpsertIssue(ctx, target, it.Resource, raw)
			if err != nil {
				return err
			}
			typ := "issue.updated"
			if opened {
				typ = "issue.opened"
			}
			s.emit(ctx, typ, iss.ID, actor, map[string]any{"target": target, "resource": it.Resource, "risk": wire.MaxRisk(kept)})
		case wire.StatusUnchanged:
			// No drift: an ignore and its record end (D-091, D-092).
			if err := s.st.ForgetIgnoreOnce(ctx, target, it.Resource); err != nil {
				return err
			}
			if err := s.st.ForgetIgnoredDrift(ctx, target, it.Resource); err != nil {
				return err
			}
			iss, err := s.st.OpenIssue(ctx, target, it.Resource)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if iss.Status != store.IssueOpen {
				continue // a Plan resolves it; its Run decides
			}
			if !iss.ReopenedAt.IsZero() && s.st.Now().Sub(iss.ReopenedAt) < reopenWindow {
				continue // opened again: it stays open for an hour (D-215)
			}
			if err := s.st.ResolveIssue(ctx, iss.ID, "gone", "system"); err != nil {
				return err
			}
			s.emit(ctx, "issue.resolved", iss.ID, "system", s.waitingNote(ctx, iss, map[string]any{"resolution": "gone"}))
		}
	}
	return nil
}

// dropIssue resolves the open Issue of a target and resource as "ignore"
// by system: a rule covers all of its drift now (D-092).
func (s *Service) dropIssue(ctx context.Context, target, resource string) error {
	iss, err := s.st.OpenIssue(ctx, target, resource)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil || iss.Status != store.IssueOpen {
		return err
	}
	if err := s.st.ResolveIssue(ctx, iss.ID, store.ResolutionIgnore, "system"); err != nil {
		return err
	}
	s.emit(ctx, "issue.resolved", iss.ID, "system", map[string]any{"resolution": store.ResolutionIgnore})
	return nil
}

// Resolve creates the Plan that resolves an open Issue. revert plans the
// target back to the desired state. accept writes the observed values as
// overrides of the Target (ARCHITECTURE §6).
func (s *Service) Resolve(ctx context.Context, id int64, action, principal string) (store.Plan, error) {
	return s.ResolveWithRule(ctx, id, action, principal, RuleScope{})
}

// ResolveWithRule is Resolve with the scope of the Rule for ignore_rule.
func (s *Service) ResolveWithRule(ctx context.Context, id int64, action, principal string, scope RuleScope) (store.Plan, error) {
	iss, err := s.st.GetIssue(ctx, id)
	if err != nil {
		return store.Plan{}, err
	}
	if iss.Status != store.IssueOpen {
		return store.Plan{}, ErrNotOpen
	}
	if iss.Resource == wire.GuestAddressID && (action == ActionRevert || action == ActionAccept) {
		return store.Plan{}, ErrAddressFix
	}
	var changes []wire.Change
	if err := json.Unmarshal(iss.Changes, &changes); err != nil {
		return store.Plan{}, err
	}
	in := plan.Input{Author: principal, IssueID: id}
	switch action {
	case ActionRevert:
		in.Origin = plan.OriginRevert
		in.Reason = fmt.Sprintf("revert drift of %s on %s (issue %d)", iss.Resource, iss.Target, id)
		in.Proposal.Targets = []string{iss.Target}
	case ActionAccept:
		in.Origin = plan.OriginAccept
		in.Reason = fmt.Sprintf("accept drift of %s on %s (issue %d)", iss.Resource, iss.Target, id)
		doc, err := s.acceptDoc(ctx, iss, changes)
		if err != nil {
			return store.Plan{}, err
		}
		in.Proposal.Put = []state.Doc{doc}
	case ActionIgnoreRule:
		if in, err = ruleProposal(iss, scope, principal); err != nil {
			return store.Plan{}, err
		}
	default:
		return store.Plan{}, fmt.Errorf("issue: unknown action %q", action)
	}
	p, err := s.plans.Propose(ctx, in)
	if err != nil {
		return store.Plan{}, err
	}
	// Only an open Issue becomes resolving: of two quick resolves, the
	// second fails here and its Plan stays pending until it goes stale.
	if err := s.st.SetIssueResolving(ctx, id, p.ID); err != nil {
		return store.Plan{}, err
	}
	s.emit(ctx, "issue.updated", id, principal, map[string]any{"status": store.IssueResolving, "action": action, "plan": p.ID})
	if action == ActionRevert {
		s.attachCovered(ctx, iss.Target, id, p, principal)
	}
	return p, nil
}

// attachCovered links the other open Issues of the target whose resource
// the revert Plan also changes. The Plan shows those changes, so their
// resolution names this Plan and its approver, not "gone" (review T-010 m3).
func (s *Service) attachCovered(ctx context.Context, target string, primary int64, p store.Plan, principal string) {
	var targets []plan.TargetPlan
	if err := json.Unmarshal(p.Changes, &targets); err != nil {
		return
	}
	planned := map[string]bool{}
	for _, tp := range targets {
		if tp.Target == target {
			for _, r := range tp.Resources {
				planned[r.Resource] = true
			}
		}
	}
	open, err := s.st.Issues(ctx, target)
	if err != nil {
		return
	}
	for _, other := range open {
		if other.ID == primary || other.Status != store.IssueOpen || !planned[other.Resource] {
			continue
		}
		if s.st.SetIssueResolving(ctx, other.ID, p.ID) == nil {
			s.emit(ctx, "issue.updated", other.ID, principal, map[string]any{"status": store.IssueResolving, "action": ActionRevert, "plan": p.ID})
		}
	}
}

// acceptDoc returns the Target document with the observed values of the
// drift as overrides of the resource.
func (s *Service) acceptDoc(ctx context.Context, iss store.Issue, changes []wire.Change) (state.Doc, error) {
	head, err := s.st.Head(ctx)
	if err != nil {
		return state.Doc{}, err
	}
	row, err := s.st.RevisionDoc(ctx, head.ID, state.KindTarget, iss.Target)
	if err != nil {
		return state.Doc{}, err
	}
	var doc struct {
		Kind string         `json:"kind"`
		Name string         `json:"name"`
		Spec map[string]any `json:"spec"`
	}
	if err := json.Unmarshal(row.Body, &doc); err != nil {
		return state.Doc{}, err
	}
	overrides, _ := doc.Spec["overrides"].(map[string]any)
	if overrides == nil {
		overrides = map[string]any{}
	}
	attrs, _ := overrides[iss.Resource].(map[string]any)
	if attrs == nil {
		attrs = map[string]any{}
	}
	for _, c := range changes {
		if c.Sensitive {
			return state.Doc{}, ErrAcceptSensitive
		}
		if iss.Resource == wire.GuestConfigID && (c.Attr == "image" || c.Attr == "kind") {
			return state.Doc{}, ErrAcceptImage
		}
		if c.Attr == wire.AttrRestart {
			continue
		}
		if c.From == nil {
			return state.Doc{}, fmt.Errorf("issue: %s has no observed value for %q to accept", iss.Resource, c.Attr)
		}
		attrs[c.Attr] = c.From // from = what the guest has now
	}
	overrides[iss.Resource] = attrs
	doc.Spec["overrides"] = overrides
	spec, err := json.Marshal(doc.Spec)
	if err != nil {
		return state.Doc{}, err
	}
	return state.Doc{Kind: doc.Kind, Name: doc.Name, Spec: spec}, nil
}

// PlanDone is the plan.Config.OnPlanDone of the controller. When the Run
// of a resolving Plan succeeded, its Issues are resolved; otherwise they
// are open again.
func (s *Service) PlanDone(ctx context.Context, planID int64, ok bool) {
	p, err := s.st.GetPlan(ctx, planID)
	if err != nil {
		return
	}
	if p.Status == store.PlanApproved {
		// The revision committed: its rules apply to open Issues (D-092),
		// and a succeeded Run ends "Ignore once" of what it applied (D-091).
		// The approver of a rule change resolves what the rule covers;
		// after any other Plan the rules are only re-applied, by system.
		by := "system"
		if slices.Contains(p.HumanOnly, plan.HumanOnlyRuleChange) {
			by = p.ApprovedBy
		}
		defer s.applyRules(ctx, by)
		if ok {
			s.forgetApplied(ctx, p)
		}
	}
	issues, err := s.st.IssuesOfPlan(ctx, planID)
	if err != nil || len(issues) == 0 {
		return
	}
	resolution := p.Origin
	if resolution == plan.OriginIgnoreRule {
		resolution = store.ResolutionIgnore // D-092
	}
	for _, iss := range issues {
		if iss.Status != store.IssueResolving {
			continue
		}
		if ok {
			if err := s.st.ResolveIssue(ctx, iss.ID, resolution, p.ApprovedBy); err == nil {
				s.emit(ctx, "issue.resolved", iss.ID, p.ApprovedBy, s.waitingNote(ctx, iss, map[string]any{"resolution": resolution, "plan": planID}))
			}
		} else if err := s.st.ReopenIssue(ctx, iss.ID); err == nil {
			s.emit(ctx, "issue.updated", iss.ID, "system", map[string]any{"status": store.IssueOpen, "plan": planID})
		}
	}
}

// waitingNote handles an Issue of guest:config that is resolved while
// values of its Target wait for a restart (D-195): it adds a system
// comment that names them, and the count to the event data. The engine
// writes the waiting list before the Issue is resolved.
func (s *Service) waitingNote(ctx context.Context, iss store.Issue, data map[string]any) map[string]any {
	if iss.Resource != wire.GuestConfigID {
		return data
	}
	b, err := s.st.GetBinding(ctx, state.KindTarget, iss.Target)
	if err != nil {
		return data
	}
	data["waits_for_restart"] = len(b.Waiting)
	if len(b.Waiting) == 0 {
		return data
	}
	var parts []string
	for _, w := range b.Waiting {
		parts = append(parts, fmt.Sprintf("%s %d is set and waits for a restart of the VM. The VM uses %d.", w.Attr, w.To, w.From))
	}
	body := "Resolved, but not active: " + strings.Join(parts, " ")
	if _, err := s.st.AddComment(ctx, store.Comment{IssueID: iss.ID, Author: "system", Body: body}); err != nil {
		return data
	}
	return data
}
