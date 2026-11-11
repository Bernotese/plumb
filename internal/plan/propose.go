package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
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
	set, err := e.load(ctx, head.ID)
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
	if in.Base != 0 && in.Base != baseRev {
		return store.Plan{}, fmt.Errorf("plan: the head moved from revision %d to %d: %w", in.Base, baseRev, store.ErrPlanStale)
	}
	next, err := apply(base, in.Proposal)
	if err != nil {
		return store.Plan{}, err
	}
	// Without builds a built Image would be used as a stock image.
	if built := next.BuiltImages(); len(built) > 0 && !e.cfg.ImageBuilds {
		return store.Plan{}, &ProposalError{Msg: fmt.Sprintf("Image %q has spec.build: %s", built[0], textBuildsOff)}
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
	// A touched target that refers to a missing secret is 422 (D-089).
	if err := e.checkSecretsPresent(ctx, next, names, in.Imported); err != nil {
		return store.Plan{}, err
	}
	plans := make([]TargetPlan, len(names))
	errs := make([]error, len(names))
	binding := make([]bool, len(names)) // a resource gets a new secret (D-099)
	var wg sync.WaitGroup
	for i, t := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The rule reads the renders, not the origin (D-099). The same
			// result keeps the value of a new binding out of the planning
			// observe (D-156).
			before, err := renderOrNil(base, t)
			after, err2 := renderOrNil(next, t)
			if errs[i] = errors.Join(err, err2); errs[i] != nil {
				return
			}
			fresh := newBindings(before, after)
			binding[i] = len(fresh) > 0
			// An imported value is not sent before the approval either.
			for id := range usesAny(after, in.Imported) {
				fresh[id] = true
			}
			plans[i], errs[i] = e.planTarget(ctx, before, next, t, contains(in.Proposal.Reenroll, t), fresh)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return store.Plan{}, err
	}

	markIgnored(next.Rules(), plans)
	// The image part: builds of new or changed recipes and removals
	// (D-199, D-209), and the Targets that cannot be made yet (D-200).
	images, imageNotes, err := e.planImages(ctx, base, next, "", "")
	if err != nil {
		return store.Plan{}, err
	}
	if err := e.checkImageTargets(ctx, next, plans, images); err != nil {
		return store.Plan{}, err
	}
	in.Proposal.Notes = append(in.Proposal.Notes, imageNotes...)
	humanOnly, err := humanOnlyReasons(base, in.Proposal)
	if err != nil {
		return store.Plan{}, err
	}
	if recipe, err := imagesHumanOnly(base, next); err != nil {
		return store.Plan{}, err
	} else if recipe {
		humanOnly = append(humanOnly, HumanOnlyImageRecipe)
	}
	if slices.Contains(binding, true) {
		humanOnly = append(humanOnly, HumanOnlySecretBinding)
	}

	// Every check passed: now an import writes its secrets (D-153), and the
	// Plan records the versions it wrote.
	if in.BeforeStore != nil {
		// The head must still be the base: an import writes nothing for a
		// Plan that would be stale at once (D-158).
		if head, err := e.st.Head(ctx); err == nil && head.ID != baseRev {
			return store.Plan{}, fmt.Errorf("plan: the head moved to revision %d during planning: %w", head.ID, store.ErrPlanStale)
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return store.Plan{}, err
		} else if errors.Is(err, store.ErrNotFound) && baseRev != 0 {
			return store.Plan{}, store.ErrPlanStale
		}
		notes, err := in.BeforeStore(ctx)
		if err != nil {
			return store.Plan{}, err
		}
		in.Proposal.Notes = append(in.Proposal.Notes, notes...)
		if err := e.refreshVersions(ctx, plans, in.Imported); err != nil {
			return store.Plan{}, err
		}
	}

	risk := wire.RiskRead
	for _, tp := range plans {
		if r := tp.risk(); r.Exceeds(risk) {
			risk = r
		}
	}
	// A build makes a new template and changes no guest (D-201).
	if len(images) > 0 && wire.RiskReversible.Exceeds(risk) {
		risk = wire.RiskReversible
	}
	proposal, _ := json.Marshal(in.Proposal)
	changes, _ := json.Marshal(plans)
	imagesJSON := []byte("[]")
	if len(images) > 0 {
		imagesJSON, _ = json.Marshal(images)
	}
	p, err := e.st.CreatePlan(ctx, store.Plan{
		BaseRevision: baseRev, Origin: in.Origin, IssueID: in.IssueID, RollbackTo: in.RollbackTo, HumanOnly: humanOnly, Proposal: proposal,
		Changes: changes, Images: imagesJSON, Risk: string(risk), Reason: in.Reason, Author: in.Author,
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
// fresh are the resources with a new secret binding (D-156): they are not
// observed before the approval.
// before is the render of the base revision (nil for a new Target).
func (e *Engine) planTarget(ctx context.Context, before *state.Rendered, next *state.Set, target string, reenroll bool, fresh map[string]bool) (TargetPlan, error) {
	r, err := renderOrNil(next, target)
	if err != nil {
		return TargetPlan{Target: target}, err
	}
	tp, err := e.planRendered(ctx, before, r, target, reenroll, fresh)
	if err == nil && r != nil {
		showOrder(&tp, r)
	}
	return tp, err
}

// showOrder adds what the Plan shows about the apply order (D-171): After
// on a resource that waits, and RestartBy on a service with a planned
// restart. A resource causes the restart when it is in restart_on of the
// service and this Plan changes it: it has a change that is not a restart
// itself (as in the agent, D-036), or it is unverified.
func showOrder(tp *TargetPlan, r *state.Rendered) {
	byID := map[string]wire.Resource{}
	for _, res := range r.Resources {
		byID[res.ID] = res
	}
	changed := map[string]bool{}
	for _, rc := range tp.Resources {
		if rc.Unverified || (len(rc.Changes) > 0 && rc.Action != wire.ActionRestart) {
			changed[rc.Resource] = true
		}
	}
	for i := range tp.Resources {
		rc := &tp.Resources[i]
		res, ok := byID[rc.Resource]
		if !ok {
			continue // guest:config
		}
		rc.After = append([]string(nil), res.After...)
		restarts := false
		for _, c := range rc.Changes {
			restarts = restarts || c.Attr == wire.AttrRestart
		}
		if !restarts {
			continue
		}
		for _, id := range wire.RestartOn(res) {
			if changed[id] && id != rc.Resource {
				rc.RestartBy = append(rc.RestartBy, id)
			}
		}
	}
}

// planRendered plans one target from its render (nil: the Target is
// deleted). before is the render of the base revision, for the Plan-only
// change of allow_restart.
func (e *Engine) planRendered(ctx context.Context, before, r *state.Rendered, target string, reenroll bool, fresh map[string]bool) (TargetPlan, error) {
	tp := TargetPlan{Target: target}
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
		spec, err := e.guestSpec(target, r)
		if err != nil {
			return tp, err
		}
		tp.Resources = append(tp.Resources, ResourceChange{Resource: wire.GuestConfigID, Action: wire.ActionCreate,
			Risk: wire.RiskReversible, Changes: createChanges(spec)})
		// Nobody can observe a guest that does not exist (D-066). The
		// first apply may restart services, so the risk is disruptive.
		for _, res := range r.Resources {
			rc := ResourceChange{Resource: res.ID, Action: wire.ActionCreate, Risk: wire.RiskDisruptive, Unverified: true}
			// A sensitive attribute shows as "set", never with its value (D-089).
			for _, attr := range res.Sensitive {
				rc.Changes = append(rc.Changes, wire.Change{Attr: attr, Sensitive: true, Risk: wire.RiskDisruptive})
			}
			tp.Resources = append(tp.Resources, rc)
		}
		if names := state.SecretNames(r); len(names) > 0 {
			have, err := e.versions(ctx, names)
			if err != nil {
				return tp, err
			}
			for _, n := range names {
				tp.Secrets = append(tp.Secrets, PlannedSecret{Name: n, Version: have[n]})
			}
		}
		return tp, nil
	}

	// An existing guest: the driver level first.
	desired, err := e.guestSpec(target, r)
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
		if c := pendingChange(desired, observed, co.Attr); c != nil {
			return tp, &ProposalError{Target: target, Err: ErrCreateOnly, Msg: fmt.Sprintf(textPendingCreateOnly, co.Attr, c.From, c.To, bind.Ref)}
		}
		return tp, &ProposalError{Target: target, Err: ErrCreateOnly, Msg: fmt.Sprintf(
			"%q can only be set when the guest is created (the guest has %v, the proposal wants %v); create a new target instead",
			co.Attr, createOnlyField(observed.Spec, co.Attr), createOnlyField(desired, co.Attr))}
	}
	if err != nil {
		return tp, err
	}
	// One rule for Plan and Run (D-194): the restart, or the flag that a
	// value waits for one.
	gchanges, _, err = restartRule(gchanges, driver.WaitingGuest(desired, observed), observed.Power, r.AllowRestart, reenroll)
	if err != nil {
		return tp, &ProposalError{Target: target, Msg: err.Error()}
	}
	if ac := allowRestartChange(before, r); ac != nil {
		gchanges = append([]wire.Change{*ac}, gchanges...)
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
	// The observe body is the one place in planning with clear values;
	// the versions it used are the versions of the Plan (D-089).
	// A resource with a new secret binding is left out: no value goes to
	// a resource that did not have it before a human approved (D-156).
	var watched, left []wire.Resource
	for _, res := range r.Resources {
		if fresh[res.ID] {
			left = append(left, res)
		} else {
			watched = append(watched, res)
		}
	}
	body, used, err := e.withSecrets(ctx, watched, nil)
	if err != nil {
		return tp, &ProposalError{Target: target, Msg: "the secrets of the target cannot be read: " + err.Error(), Err: err}
	}
	tp.Secrets = used
	if len(left) > 0 {
		if err := e.planUnobserved(ctx, &tp, r, left); err != nil {
			return tp, err
		}
	}
	rep, err := e.command(ctx, target, wire.CommandObserve, wire.ObserveBody{Resources: body}, e.cfg.ObserveTimeout, nil)
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
	plannedRestarts(&tp, r, left)
	return tp, nil
}

// createChanges lists the settings of a new guest as changes from nothing.
func createChanges(s driver.GuestSpec) []wire.Change {
	c := []wire.Change{{Attr: wire.AttrState, From: wire.StateAbsent, To: wire.StatePresent, Risk: wire.RiskReversible}}
	for _, kv := range []struct {
		attr string
		v    any
	}{{driver.AttrImage, s.ImageName}, {driver.AttrCores, s.Cores}, {driver.AttrMemory, s.MemoryMB}, {driver.AttrDisk, s.DiskGB}, {driver.AttrBridge, s.Bridge},
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
		return s.ImageName
	case driver.AttrKind:
		return s.Kind
	}
	return nil
}

// guestSpec turns the rendered guest:config into a driver spec.
func (e *Engine) guestSpec(target string, r *state.Rendered) (driver.GuestSpec, error) {
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
	// The create-only attribute image is the Image name (D-189). The ref
	// (spec.Image) is set only by the create step, from FetchImage (D-188).
	spec.ImageName = r.Image
	return spec, nil
}

// planUnobserved plans the resources that the planning observe left out
// (D-156) as a new target plans its resources (D-066, D-133): create,
// unverified, disruptive, one sensitive change per sensitive attribute.
// Their secrets get the current versions (D-138).
func (e *Engine) planUnobserved(ctx context.Context, tp *TargetPlan, r *state.Rendered, left []wire.Resource) error {
	names := map[string]bool{}
	for _, s := range tp.Secrets {
		names[s.Name] = true
	}
	var add []string
	for _, res := range left {
		rc := ResourceChange{Resource: res.ID, Action: wire.ActionCreate, Risk: wire.RiskDisruptive, Unverified: true}
		for _, attr := range res.Sensitive {
			rc.Changes = append(rc.Changes, wire.Change{Attr: attr, Sensitive: true, Risk: wire.RiskDisruptive})
		}
		tp.Resources = append(tp.Resources, rc)
		for _, n := range state.ResourceSecrets(res) {
			if !names[n] {
				names[n] = true
				add = append(add, n)
			}
		}
	}
	if len(add) == 0 {
		return nil
	}
	have, err := e.versions(ctx, add)
	if err != nil {
		return err
	}
	for _, n := range add {
		tp.Secrets = append(tp.Secrets, PlannedSecret{Name: n, Version: have[n]})
	}
	sort.Slice(tp.Secrets, func(i, j int) bool { return tp.Secrets[i].Name < tp.Secrets[j].Name })
	return nil
}

// plannedRestarts adds a restart change (disruptive) to every service
// whose restart_on names a resource that was not observed: the agent adds
// that change at apply (D-036), and the apply refuses what the Plan does
// not show (D-075, D-156).
func plannedRestarts(tp *TargetPlan, r *state.Rendered, left []wire.Resource) {
	if len(left) == 0 {
		return
	}
	gone := map[string]bool{}
	for _, res := range left {
		gone[res.ID] = true
	}
	restart := wire.Change{Attr: wire.AttrRestart, Risk: wire.RiskDisruptive}
	for _, res := range r.Resources {
		if gone[res.ID] {
			continue
		}
		needs := false
		for _, id := range wire.RestartOn(res) {
			if gone[id] {
				needs = true
			}
		}
		if !needs {
			continue
		}
		found := false
		for i := range tp.Resources {
			rc := &tp.Resources[i]
			if rc.Resource != res.ID {
				continue
			}
			found = true
			has := false
			for _, c := range rc.Changes {
				has = has || c.Attr == wire.AttrRestart
			}
			if !has {
				rc.Changes = append(rc.Changes, restart)
				rc.Action, rc.Risk = wire.ActionOf(rc.Changes), wire.MaxRisk(rc.Changes)
			}
		}
		if !found {
			cs := []wire.Change{restart}
			tp.Resources = append(tp.Resources, ResourceChange{Resource: res.ID, Action: wire.ActionOf(cs), Changes: cs, Risk: wire.RiskDisruptive})
		}
	}
}
