package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// ProposalError says why a proposal cannot become a Plan. It gets no Plan
// row (migration 0001: "a proposal that cannot be planned is an API
// error").
type ProposalError struct {
	Target string // empty when the error is not about one target
	Msg    string
	Err    error
}

func (e *ProposalError) Error() string {
	if e.Target != "" {
		return fmt.Sprintf("plan: target %s: %s", e.Target, e.Msg)
	}
	return "plan: " + e.Msg
}

func (e *ProposalError) Unwrap() error { return e.Err }

// ErrOffline is wrapped when a target that the proposal touches is offline.
var ErrOffline = errors.New("target is offline")

// ErrTargetBusy is wrapped when a Run works on a target that a proposal
// or an approval touches. Only one Run works on a target at a time, and
// nobody plans against a half-applied guest.
var ErrTargetBusy = errors.New("a Run is working on the target")

// ErrCreateOnly is wrapped when a proposal changes a create-only setting.
var ErrCreateOnly = errors.New("create-only setting")

// base returns the head revision id (0 for none) and its validated set.
func (e *Engine) base(ctx context.Context) (int64, *state.Set, error) {
	head, err := e.st.Head(ctx)
	if errors.Is(err, store.ErrNotFound) {
		set, err := state.Validate(nil)
		return 0, set, err
	}
	if err != nil {
		return 0, nil, err
	}
	set, err := state.Load(ctx, e.st, head.ID)
	return head.ID, set, err
}

// apply builds the new document set: the base, minus Delete, plus Put.
func apply(base *state.Set, p Proposal) (*state.Set, error) {
	docs := map[DocKey]state.Doc{}
	for _, d := range base.Docs() {
		docs[DocKey{d.Kind, d.Name}] = d
	}
	for _, k := range p.Delete {
		if _, ok := docs[k]; !ok {
			return nil, &ProposalError{Msg: fmt.Sprintf("%s %q does not exist and cannot be deleted", k.Kind, k.Name)}
		}
		delete(docs, k)
	}
	seen := map[DocKey]bool{}
	for _, d := range p.Put {
		k := DocKey{d.Kind, d.Name}
		if seen[k] {
			return nil, &ProposalError{Msg: fmt.Sprintf("%s %q is twice in the proposal", d.Kind, d.Name)}
		}
		seen[k] = true
		docs[k] = d
	}
	list := make([]state.Doc, 0, len(docs))
	for _, d := range docs {
		list = append(list, d)
	}
	set, err := state.Validate(list)
	if err != nil {
		return nil, &ProposalError{Msg: "the documents are not valid:\n" + err.Error(), Err: err}
	}
	return set, nil
}

// renderOrNil renders a target, or returns nil when the set has none.
func renderOrNil(set *state.Set, target string) (*state.Rendered, error) {
	if !contains(set.Targets(), target) {
		return nil, nil
	}
	return set.Render(target)
}

// Propose turns a proposal into a pending Plan. It observes every target
// that the proposal touches, and only those: an offline target that the
// proposal does not touch does not matter.
func (e *Engine) Propose(ctx context.Context, in Input) (store.Plan, error) {
	if in.Origin == "" {
		in.Origin = OriginProposal
	}
	if in.Author == "" {
		return store.Plan{}, errors.New("plan: a proposal needs an author")
	}
	baseRev, base, err := e.base(ctx)
	if err != nil {
		return store.Plan{}, err
	}
	next, err := apply(base, in.Proposal)
	if err != nil {
		return store.Plan{}, err
	}

	// Touched targets: added, removed, rendered differently, or named.
	touched := map[string]bool{}
	for _, t := range append(append([]string{}, in.Proposal.Targets...), in.Proposal.Reenroll...) {
		if !contains(next.Targets(), t) {
			return store.Plan{}, &ProposalError{Target: t, Msg: "no such target"}
		}
		touched[t] = true
	}
	for _, t := range union(base.Targets(), next.Targets()) {
		before, err := renderOrNil(base, t)
		if err != nil {
			return store.Plan{}, err
		}
		after, err := renderOrNil(next, t)
		if err != nil {
			return store.Plan{}, err
		}
		if !reflect.DeepEqual(before, after) {
			touched[t] = true
		}
	}

	names := make([]string, 0, len(touched))
	for t := range touched {
		names = append(names, t)
	}
	sort.Strings(names)
	if err := e.checkIdle(names); err != nil {
		return store.Plan{}, err
	}
	plans := make([]TargetPlan, len(names))
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i, t := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plans[i], errs[i] = e.planTarget(ctx, next, t, contains(in.Proposal.Reenroll, t))
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return store.Plan{}, err
	}

	risk := wire.RiskRead
	for _, tp := range plans {
		if r := tp.risk(); r.Exceeds(risk) {
			risk = r
		}
	}
	proposal, _ := json.Marshal(in.Proposal)
	changes, _ := json.Marshal(plans)
	p, err := e.st.CreatePlan(ctx, store.Plan{
		BaseRevision: baseRev, Origin: in.Origin, IssueID: in.IssueID, Proposal: proposal,
		Changes: changes, Risk: string(risk), Reason: in.Reason, Author: in.Author,
	})
	if err != nil {
		return store.Plan{}, err
	}
	e.emit(ctx, "plan.created", fmt.Sprintf("plan:%d", p.ID), in.Author,
		map[string]any{"origin": in.Origin, "risk": risk, "targets": names})
	return p, nil
}

func union(a, b []string) []string {
	m := map[string]bool{}
	for _, s := range append(append([]string{}, a...), b...) {
		m[s] = true
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// planTarget observes one target and computes its changes.
func (e *Engine) planTarget(ctx context.Context, next *state.Set, target string, reenroll bool) (TargetPlan, error) {
	tp := TargetPlan{Target: target}
	r, err := renderOrNil(next, target)
	if err != nil {
		return tp, err
	}
	bind, err := e.st.GetBinding(ctx, state.KindTarget, target)
	exists := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return tp, err
	}

	switch {
	case r == nil: // the Target is deleted
		if exists {
			tp.Guest = "delete"
			tp.Resources = []ResourceChange{{Resource: wire.GuestConfigID, Action: wire.ActionDelete, Risk: wire.RiskDestructive,
				Changes: []wire.Change{{Attr: wire.AttrState, From: wire.StatePresent, To: wire.StateAbsent, Risk: wire.RiskDestructive}}}}
		}
		return tp, nil

	case !exists: // a new guest
		if reenroll {
			return tp, &ProposalError{Target: target, Msg: "the guest does not exist yet; there is nothing to re-enroll"}
		}
		tp.Guest = "create"
		spec, err := e.guestSpec(ctx, target, r)
		if err != nil {
			return tp, err
		}
		tp.Resources = append(tp.Resources, ResourceChange{Resource: wire.GuestConfigID, Action: wire.ActionCreate,
			Risk: wire.RiskReversible, Changes: createChanges(spec)})
		// Nobody can observe a guest that does not exist (D-066). The
		// first apply may restart services, so the risk is disruptive.
		for _, res := range r.Resources {
			tp.Resources = append(tp.Resources, ResourceChange{Resource: res.ID, Action: wire.ActionCreate,
				Risk: wire.RiskDisruptive, Unverified: true})
		}
		return tp, nil
	}

	// An existing guest: the driver level first.
	desired, err := e.guestSpec(ctx, target, r)
	if err != nil {
		return tp, err
	}
	observed, err := e.cfg.Driver.Observe(ctx, driver.GuestID(bind.Ref))
	if err != nil {
		return tp, &ProposalError{Target: target, Msg: "cannot read the guest: " + err.Error(), Err: err}
	}
	gchanges, err := e.cfg.Driver.Diff(desired, observed)
	var co *driver.CreateOnlyError
	if errors.As(err, &co) {
		return tp, &ProposalError{Target: target, Err: ErrCreateOnly, Msg: fmt.Sprintf(
			"%q can only be set when the guest is created (the guest has %v, the proposal wants %v); create a new target instead",
			co.Attr, createOnlyField(observed.Spec, co.Attr), createOnlyField(desired, co.Attr))}
	}
	if err != nil {
		return tp, err
	}
	if reenroll {
		tp.Guest = "reenroll"
		gchanges = append(gchanges, wire.Change{Attr: driver.AttrEnrollToken, Sensitive: true, Risk: wire.RiskDisruptive})
	} else if len(gchanges) > 0 {
		tp.Guest = "update"
	}
	if len(gchanges) > 0 {
		tp.Resources = append(tp.Resources, ResourceChange{Resource: wire.GuestConfigID,
			Action: wire.ActionOf(gchanges), Changes: gchanges, Risk: wire.MaxRisk(gchanges)})
	}
	if reenroll {
		return tp, nil // the agent is lost or about to be replaced; nothing to observe
	}

	// Then the agent level.
	if !e.cfg.Agents.Online(target) {
		return tp, &ProposalError{Target: target, Err: ErrOffline,
			Msg: "the agent is offline (no poll in the last 90 s); the proposal changes this target, so it cannot be planned"}
	}
	rep, err := e.command(ctx, target, wire.CommandObserve, wire.ObserveBody{Resources: r.Resources}, e.cfg.ObserveTimeout, nil)
	if err != nil {
		return tp, &ProposalError{Target: target, Msg: "observe failed: " + err.Error(), Err: err}
	}
	for _, it := range rep.Items {
		switch it.Status {
		case wire.StatusUnchanged:
		case wire.StatusDiffers:
			tp.Resources = append(tp.Resources, ResourceChange{Resource: it.Resource,
				Action: wire.ActionOf(it.Changes), Changes: it.Changes, Risk: wire.MaxRisk(it.Changes)})
		default:
			return tp, &ProposalError{Target: target, Msg: fmt.Sprintf("observe of %s failed: %s", it.Resource, it.Output)}
		}
	}
	return tp, nil
}

// createChanges lists the settings of a new guest as changes from nothing.
func createChanges(s driver.GuestSpec) []wire.Change {
	c := []wire.Change{{Attr: wire.AttrState, From: wire.StateAbsent, To: wire.StatePresent, Risk: wire.RiskReversible}}
	for _, kv := range []struct {
		attr string
		v    any
	}{{driver.AttrCores, s.Cores}, {driver.AttrMemory, s.MemoryMB}, {driver.AttrDisk, s.DiskGB}, {driver.AttrBridge, s.Bridge},
		{driver.AttrIP, s.IP}, {driver.AttrGateway, s.Gateway}, {driver.AttrUser, s.User}, {driver.AttrSSHKeys, s.SSHKeys}} {
		if reflect.ValueOf(kv.v).IsZero() {
			continue
		}
		c = append(c, wire.Change{Attr: kv.attr, To: kv.v, Risk: wire.RiskReversible})
	}
	return c
}

func createOnlyField(s driver.GuestSpec, attr string) any {
	switch attr {
	case driver.AttrDisk:
		return s.DiskGB
	case driver.AttrIP:
		return s.IP
	case driver.AttrGateway:
		return s.Gateway
	case driver.AttrUser:
		return s.User
	case driver.AttrSSHKeys:
		return s.SSHKeys
	case driver.AttrImage:
		return s.Image
	case driver.AttrKind:
		return s.Kind
	}
	return nil
}

// guestSpec turns the rendered guest:config into a driver spec.
func (e *Engine) guestSpec(ctx context.Context, target string, r *state.Rendered) (driver.GuestSpec, error) {
	a := r.Guest.Attrs
	spec := driver.GuestSpec{Name: target, Kind: r.Kind}
	var err error
	if spec.Cores, err = driver.IntValue(a[driver.AttrCores]); err != nil {
		return spec, err
	}
	if spec.MemoryMB, err = driver.IntValue(a[driver.AttrMemory]); err != nil {
		return spec, err
	}
	if spec.DiskGB, err = driver.IntValue(a[driver.AttrDisk]); err != nil {
		return spec, err
	}
	spec.Bridge, _ = a[driver.AttrBridge].(string)
	spec.IP, _ = a[driver.AttrIP].(string)
	spec.Gateway, _ = a[driver.AttrGateway].(string)
	spec.User, _ = a[driver.AttrUser].(string)
	keys, _ := a[driver.AttrSSHKeys].([]any)
	for _, k := range keys {
		if s, ok := k.(string); ok {
			spec.SSHKeys = append(spec.SSHKeys, s)
		}
	}
	// The image ref is known once the image is fetched. Before that, a
	// placeholder that no driver uses makes a changed image a create-only
	// difference.
	if b, err := e.st.GetBinding(ctx, state.KindImage, r.Image); err == nil {
		spec.Image = driver.ImageRef(b.Ref)
	} else if errors.Is(err, store.ErrNotFound) {
		spec.Image = driver.ImageRef("unfetched:" + r.Image)
	} else {
		return spec, err
	}
	return spec, nil
}
