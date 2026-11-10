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

// Service keeps Issues.
type Service struct {
	st     *store.Store
	events *event.Log
	plans  *plan.Engine
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
	for _, it := range rep.Items {
		switch it.Status {
		case wire.StatusDiffers:
			raw, err := json.Marshal(it.Changes)
			if err != nil {
				return err
			}
			iss, opened, err := s.st.UpsertIssue(ctx, target, it.Resource, raw)
			if err != nil {
				return err
			}
			typ := "issue.updated"
			if opened {
				typ = "issue.opened"
			}
			s.emit(ctx, typ, iss.ID, actor, map[string]any{"target": target, "resource": it.Resource, "risk": wire.MaxRisk(it.Changes)})
		case wire.StatusUnchanged:
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
			if err := s.st.ResolveIssue(ctx, iss.ID, "gone", "system"); err != nil {
				return err
			}
			s.emit(ctx, "issue.resolved", iss.ID, "system", map[string]any{"resolution": "gone"})
		}
	}
	return nil
}

// Resolve creates the Plan that resolves an open Issue. revert plans the
// target back to the desired state. accept writes the observed values as
// overrides of the Target (ARCHITECTURE §6).
func (s *Service) Resolve(ctx context.Context, id int64, action, principal string) (store.Plan, error) {
	iss, err := s.st.GetIssue(ctx, id)
	if err != nil {
		return store.Plan{}, err
	}
	if iss.Status != store.IssueOpen {
		return store.Plan{}, ErrNotOpen
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
	s.emit(ctx, "issue.resolving", id, principal, map[string]any{"action": action, "plan": p.ID})
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
			s.emit(ctx, "issue.resolving", other.ID, principal, map[string]any{"action": ActionRevert, "plan": p.ID})
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
	issues, err := s.st.IssuesOfPlan(ctx, planID)
	if err != nil || len(issues) == 0 {
		return
	}
	p, err := s.st.GetPlan(ctx, planID)
	if err != nil {
		return
	}
	for _, iss := range issues {
		if iss.Status != store.IssueResolving {
			continue
		}
		if ok {
			if err := s.st.ResolveIssue(ctx, iss.ID, p.Origin, p.ApprovedBy); err == nil {
				s.emit(ctx, "issue.resolved", iss.ID, p.ApprovedBy, map[string]any{"resolution": p.Origin, "plan": planID})
			}
		} else if err := s.st.ReopenIssue(ctx, iss.ID); err == nil {
			s.emit(ctx, "issue.reopened", iss.ID, "system", map[string]any{"plan": planID})
		}
	}
}
