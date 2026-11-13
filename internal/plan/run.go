package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// ErrStale is returned when a Plan cannot be approved because the head
// revision moved. The Plan is marked stale.
var ErrStale = store.ErrPlanStale

// CheckFunc decides whether the principal may approve the Plan, for
// example by comparing its risk with the token tier (T-011). nil allows.
type CheckFunc func(p store.Plan) error

// Approve approves a pending Plan: it checks the rights, commits the
// Revision (author and reason of the Plan, approver, plan id) and starts
// the Run. It returns the Run.
func (e *Engine) Approve(ctx context.Context, planID int64, principal string, check CheckFunc) (store.Run, error) {
	p, err := e.st.GetPlan(ctx, planID)
	if err != nil {
		return store.Run{}, err
	}
	if p.Status == store.PlanStale {
		// Stale is a kind of "not pending"; the API answers plan-stale.
		return store.Run{}, fmt.Errorf("plan: plan %d: %w (%w)", planID, ErrStale, store.ErrPlanNotPending)
	}
	if p.Status != store.PlanPending {
		return store.Run{}, fmt.Errorf("plan: plan %d is %s: %w", planID, p.Status, store.ErrPlanNotPending)
	}
	if check != nil {
		if err := check(p); err != nil {
			return store.Run{}, err
		}
	}
	images, err := imagePlans(p)
	if err != nil {
		return store.Run{}, err
	}
	if p.Origin == OriginBuild {
		return e.approveBuild(ctx, p, principal, images)
	}
	if p.Origin == OriginAgentUpdate {
		return e.approveAgentUpdate(ctx, p, principal)
	}
	var targets []TargetPlan
	if err := json.Unmarshal(p.Changes, &targets); err != nil {
		return store.Run{}, err
	}
	// The approver approves the planned values: another version or a
	// missing secret makes the Plan stale (D-089).
	if err := e.checkPlannedVersions(ctx, targets); err != nil {
		if errors.Is(err, ErrStale) {
			if ok, merr := e.st.MarkPlanStale(ctx, planID); merr == nil && ok {
				e.emit(ctx, "plan.stale", fmt.Sprintf("plan:%d", planID), "system", nil)
				if e.cfg.OnPlanDone != nil {
					e.cfg.OnPlanDone(ctx, planID, false)
				}
			}
		}
		return store.Run{}, err
	}
	// Reserve the targets before anything is committed. A second approval
	// for a busy target fails here; on any later error the reservation is
	// released.
	names := make([]string, len(targets))
	for i, tp := range targets {
		names[i] = tp.Target
	}
	if err := e.reserve(names); err != nil {
		// A second approval of the same Plan meets the first one's
		// reservation; say what it really is (review T-010 n2).
		if again, gerr := e.st.GetPlan(ctx, planID); gerr == nil && again.Status != store.PlanPending {
			return store.Run{}, fmt.Errorf("plan: plan %d is %s: %w", planID, again.Status, store.ErrPlanNotPending)
		}
		return store.Run{}, err
	}
	started := false
	defer func() {
		if !started {
			e.release(names)
		}
	}()
	slots, err := e.reserveImages(ctx, images)
	if err != nil {
		return store.Run{}, err
	}
	defer func() {
		if !started {
			e.release(slots)
		}
	}()
	var prop Proposal
	if err := json.Unmarshal(p.Proposal, &prop); err != nil {
		return store.Run{}, err
	}
	// Rebuild exactly the planned set on the base of the Plan. When the
	// head moved, ApprovePlan refuses it anyway.
	var base *state.Set
	if p.BaseRevision == 0 {
		base, err = state.Validate(nil)
	} else {
		base, err = e.load(ctx, p.BaseRevision)
	}
	if err != nil {
		return store.Run{}, err
	}
	next, err := apply(base, prop)
	if err != nil {
		return store.Run{}, err
	}
	// The agents may have changed since the Plan (D-259, D-268).
	if gateOrigin(p.Origin) {
		if err := e.checkGate(ctx, next, names); err != nil {
			return store.Run{}, err
		}
	}
	rows, err := state.Rows(next)
	if err != nil {
		return store.Run{}, err
	}
	rev, run, err := e.st.ApprovePlan(ctx, planID, principal, store.NewRevision{Author: p.Author, Reason: p.Reason, Docs: rows})
	if errors.Is(err, store.ErrPlanStale) {
		e.emit(ctx, "plan.stale", fmt.Sprintf("plan:%d", planID), "system", nil)
		if e.cfg.OnPlanDone != nil {
			e.cfg.OnPlanDone(ctx, planID, false)
		}
		return store.Run{}, fmt.Errorf("plan: plan %d: %w", planID, ErrStale)
	}
	if err != nil {
		return store.Run{}, err
	}
	e.emit(ctx, "plan.approved", fmt.Sprintf("plan:%d", planID), principal, map[string]any{"revision": rev.ID, "run": run.ID})
	e.emit(ctx, "revision.committed", fmt.Sprintf("revision:%d", rev.ID), p.Author, map[string]any{"plan": planID})
	// Every other pending Plan is now stale; a build Plan only when its
	// recipe changed (D-199).
	if ids, err := e.st.MarkStalePlans(ctx); err == nil {
		for _, id := range ids {
			e.emit(ctx, "plan.stale", fmt.Sprintf("plan:%d", id), "system", nil)
			if e.cfg.OnPlanDone != nil {
				e.cfg.OnPlanDone(ctx, id, false)
			}
		}
	}
	e.staleBuildPlans(ctx, next)
	e.resetSchedules(ctx, base, next)

	started = true
	e.start(p, run, next, targets, images, slots)
	return run, nil
}

// start runs a Plan in the background. The target names and the image
// slots are reserved; each is released when its lane ends.
func (e *Engine) start(p store.Plan, run store.Run, set *state.Set, targets []TargetPlan, images []ImagePlan, slots []string) {
	e.mu.Lock()
	e.active[run.ID] = true
	e.mu.Unlock()
	e.runs.Add(1)
	go func() {
		defer e.runs.Done()
		e.execute(e.ctx, p, run, set, targets, images)
		e.scheduledDone(p.ID)
		e.mu.Lock()
		delete(e.active, run.ID)
		e.mu.Unlock()
	}()
}

// execute runs all targets and image lanes of a Plan in parallel. A failed
// lane does not stop the others.
func (e *Engine) execute(ctx context.Context, p store.Plan, run store.Run, set *state.Set, targets []TargetPlan, images []ImagePlan) {
	subject := fmt.Sprintf("run:%d", run.ID)
	e.emit(ctx, "run.started", subject, "system", map[string]any{"plan": p.ID})
	ok := make([]bool, len(targets)+len(images))
	var wg sync.WaitGroup
	for i, tp := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok[i] = e.runTarget(ctx, p, run, set, tp)
			e.release([]string{tp.Target})
		}()
	}
	for i, ip := range images {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok[len(targets)+i] = e.runImage(ctx, p, run, set, ip)
			e.release([]string{imageSlot(ip.Image)})
		}()
	}
	wg.Wait()
	status := "succeeded"
	for _, o := range ok {
		if !o {
			status = "failed"
		}
	}
	// The final writes use a fresh context: a closing engine still records
	// how far the Run came.
	fin, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		status = "failed"
	}
	// The waiting list of every Target of the Run, succeeded or failed,
	// before the Issue hook (D-195, write point b).
	e.recordWaiting(fin, set, targets)
	waits := e.waitsOfRun(fin, run.ID)
	if err := e.st.FinishRun(fin, run.ID, status, waits); err != nil {
		e.log.Error("finish run", "run", run.ID, "err", err)
	}
	e.emit(fin, "run.finished", subject, "system", map[string]any{"plan": p.ID, "status": status, "waits_for_restart": waits})
	if e.cfg.OnPlanDone != nil {
		e.cfg.OnPlanDone(fin, p.ID, status == "succeeded")
	}
}

// stepper records the steps of one target and emits an event per change.
type stepper struct {
	e      *Engine
	run    store.Run
	target string
	seq    map[string]int64 // resource → step
	final  map[string]bool  // steps with a final status
	ok     bool
}

func (s *stepper) add(ctx context.Context, resource string) {
	ctx = context.WithoutCancel(ctx) // the record of a Run outlives a cancel
	seq, err := s.e.st.AddStep(ctx, store.Step{RunID: s.run.ID, Target: s.target, Resource: resource, Status: string(wire.StatusPending)})
	if err != nil {
		s.e.log.Error("add step", "err", err)
		s.ok = false
		return
	}
	s.seq[resource] = seq
}

func (s *stepper) set(ctx context.Context, resource string, status wire.Status, changes []wire.Change, output string) {
	// A closing engine cancels the Run, but its steps must still get a
	// final status (QA T-010): writes use a context without the cancel.
	ctx = context.WithoutCancel(ctx)
	seq, known := s.seq[resource]
	if !known {
		s.add(ctx, resource) // the agent reported a resource that was not planned
		seq = s.seq[resource]
	}
	var raw json.RawMessage
	if changes != nil {
		raw, _ = json.Marshal(changes)
	}
	if err := s.e.st.UpdateStep(ctx, s.run.ID, seq, string(status), raw, output); err != nil {
		s.e.log.Error("update step", "err", err)
	}
	switch status {
	case wire.StatusFailed, wire.StatusRefused, wire.StatusSkipped:
		s.ok = false
	}
	if status != wire.StatusPending && status != wire.StatusRunning {
		s.final[resource] = true
	}
	// The event has no values: changes stay in the step (D-047).
	s.e.emit(ctx, "run.step", fmt.Sprintf("run:%d", s.run.ID), "system",
		map[string]any{"target": s.target, "resource": resource, "status": status, "action": wire.ActionOf(changes)})
}

// failRest marks every step that did not finish as failed.
func (s *stepper) failRest(ctx context.Context, output string) {
	s.failRestExcept(ctx, output, "")
}

// failRestExcept is failRest without the step keep (the guest step that
// waits for the stop, D-299).
func (s *stepper) failRestExcept(ctx context.Context, output, keep string) {
	for res := range s.seq {
		if !s.final[res] && res != keep {
			s.set(ctx, res, wire.StatusFailed, nil, output)
		}
	}
}

// skip marks a step skipped on purpose (a stopped Target, D-299): the
// Run does not fail for it.
func (s *stepper) skip(ctx context.Context, resource, output string) {
	ok := s.ok
	s.set(ctx, resource, wire.StatusSkipped, nil, output)
	s.ok = ok
}

// runTarget runs the guest step, waits for the agent and applies the
// resources. It returns whether every step succeeded.
func (e *Engine) runTarget(ctx context.Context, p store.Plan, run store.Run, set *state.Set, tp TargetPlan) bool {
	if p.Origin == OriginAgentUpdate {
		return e.runAgentUpdate(ctx, p, run, set, tp)
	}
	s := &stepper{e: e, run: run, target: tp.Target, seq: map[string]int64{}, final: map[string]bool{}, ok: true}
	r, err := renderOrNil(set, tp.Target)
	if err != nil {
		e.log.Error("render", "target", tp.Target, "err", err)
		return false
	}
	var guestChange *ResourceChange
	for i := range tp.Resources {
		if tp.Resources[i].Resource == wire.GuestConfigID {
			guestChange = &tp.Resources[i]
		}
	}
	reenroll := tp.Guest == "reenroll"
	// A container: its template is fetched in its own step (D-236), and
	// the enrollment token leaves its config after the hello (D-231).
	lxc := r != nil && r.Kind == wire.KindLXC
	// A built Image (T-082) clones its newest version: no fetch.
	built := false
	if lxc {
		info, ok := set.ImageInfo(r.Image)
		built = ok && info.Built
	}
	fetch := lxc && !built && guestChange != nil && tp.Guest == "create"
	clear := lxc && (tp.Guest == "create" || reenroll || e.tokenInConfig(ctx, tp.Target))
	if fetch {
		s.add(ctx, StepImageFetch)
	}
	if guestChange != nil {
		s.add(ctx, wire.GuestConfigID)
	}
	if clear {
		s.add(ctx, StepGuestBootstrap)
	}
	// Every path that keeps the target ends with the watch list of the new
	// revision, also when the Run fails early (review T-010 n1): a hello
	// during the Run was skipped, so the agent would watch the old list.
	watched := false
	if r != nil {
		defer func() {
			if !watched {
				if err := e.watch(ctx, tp.Target, r); err != nil {
					e.log.Error("watch", "target", tp.Target, "err", err)
				}
			}
		}()
	}
	if r != nil && !reenroll { // a re-enrollment applies no resource (D-075)
		for _, res := range r.Resources {
			s.add(ctx, res.ID)
		}
	}

	// 1. Guest step; for a new container, the template first.
	newAgent := false
	var gres guestResult // the guest step; open while gres.grow or gres.stop wait
	open := false
	// failAll ends every open step: the guest step keeps its changes.
	failAll := func(output string) {
		if open {
			s.set(ctx, wire.GuestConfigID, wire.StatusFailed, gres.changes, output)
			open = false
		}
		s.failRest(ctx, output)
	}
	hellos := e.hellos(tp.Target)
	var ref driver.ImageRef
	if fetch {
		s.set(ctx, StepImageFetch, wire.StatusRunning, nil, "")
		var err error
		if ref, err = e.fetchImage(ctx, run.RevisionID, r.Image); err != nil {
			s.set(ctx, StepImageFetch, wire.StatusFailed, nil, err.Error())
			s.failRest(ctx, "the template fetch failed")
			return false
		}
		s.set(ctx, StepImageFetch, wire.StatusApplied, nil, "template "+string(ref))
	}
	if guestChange != nil {
		s.set(ctx, wire.GuestConfigID, wire.StatusRunning, nil, "")
		res, err := e.guestStep(ctx, run, set, tp, r, *guestChange, ref)
		gres = res
		switch {
		case errors.Is(err, errRefused):
			s.set(ctx, wire.GuestConfigID, wire.StatusRefused, res.changes, err.Error())
		case err != nil:
			s.set(ctx, wire.GuestConfigID, wire.StatusFailed, res.changes, err.Error())
		case res.grow > 0 || res.stop:
			open = true // ends after the root grow or the stop (D-299)
		default:
			s.set(ctx, wire.GuestConfigID, wire.StatusApplied, res.changes, res.output)
		}
		if err != nil {
			s.failRest(ctx, "the guest step failed")
			return false
		}
		// After a new boot the old link may still look online: the
		// resource steps wait for a hello of the new boot (D-194).
		newAgent = tp.Guest == "create" || tp.Guest == "reenroll" || res.restarted
	}
	if r == nil { // the target was deleted
		return s.ok
	}

	// A Target that stays stopped (D-299): its agent is off on purpose, so
	// its agent steps are skipped. A hello after its next start gets the
	// watch list.
	if !reenroll && !gres.stop && desiredPower(r) == driver.PowerStopped && e.guestStopped(ctx, tp.Target) {
		for _, res := range r.Resources {
			s.skip(ctx, res.ID, textStoppedSkip)
		}
		watched = true
		return s.ok
	}

	// 2. Wait for the agent.
	if newAgent || !e.cfg.Agents.Online(tp.Target) {
		e.emit(ctx, "target.status", "target:"+tp.Target, "system", map[string]any{"phase": "enrolling", "run": run.ID})
		after := -1 // any hello
		if newAgent {
			after = hellos
		}
		if err := e.waitOnline(ctx, tp.Target, after); err != nil {
			failAll(err.Error())
			return false
		}
		e.emit(ctx, "target.status", "target:"+tp.Target, "system", map[string]any{"agent": "online", "run": run.ID})
	}

	// The agent is enrolled: the token is used and dead. Removing it from
	// the container config is hygiene; a failure fails the Run, and the
	// next Run of the target tries again (D-231).
	if clear {
		e.clearStep(ctx, s, tp.Target)
	}

	// The root file system of a grown VM disk, before the agent steps
	// (D-299): the agent is online now.
	if gres.grow > 0 {
		if err := e.growRoot(ctx, p.ID, tp.Target, gres.grow); err != nil {
			failAll(err.Error())
			return false
		}
		if !gres.stop {
			s.set(ctx, wire.GuestConfigID, wire.StatusApplied, gres.changes, gres.output)
			open = false
		}
	}

	// A re-enrollment only gives the new agent its watch list. What
	// differs becomes an Issue through the drift report (D-075).
	if reenroll {
		watched = true
		if err := e.watch(ctx, tp.Target, r); err != nil {
			e.log.Error("watch", "target", tp.Target, "err", err)
			return false
		}
		return s.ok
	}

	// 3. Resource steps. The agent observes again and changes a resource
	// only as far as the Plan shows it (D-075), and never above the
	// approved risk of this target (D-022).
	maxRisk := tp.risk()
	// The gate, right before the first command to the agent and after an
	// enrollment, so a new agent said hello (D-276): no command with a
	// resource that the agent lacks.
	if gateOrigin(p.Origin) {
		if err := e.runGate(ctx, tp.Target, r.Resources); err != nil {
			failAll(err.Error())
			return false
		}
	}
	// Exactly the planned secret versions, or no apply at all (D-089).
	body, _, err := e.withSecrets(ctx, r.Resources, plannedOrEmpty(tp.Secrets))
	if err != nil {
		failAll("not applied: " + err.Error() + "; plan again")
		return false
	}
	rep, err := e.command(ctx, tp.Target, wire.CommandApply,
		wire.ApplyBody{PlanID: p.ID, Resources: body, Planned: planned(tp), MaxRisk: maxRisk},
		// A command may run for its own timeout (T-094 review M2): the
		// apply waits for it, so a Run never fails while one still runs.
		e.cfg.ApplyTimeout+state.CommandTime(r.Resources),
		func(pr wire.Report) {
			for _, it := range pr.Items {
				if it.Status == wire.StatusRunning {
					s.set(ctx, it.Resource, wire.StatusRunning, nil, "")
				}
			}
		})
	for _, it := range rep.Items {
		s.set(ctx, it.Resource, it.Status, it.Changes, it.Output)
	}
	if err != nil {
		s.failRestExcept(ctx, err.Error(), wire.GuestConfigID)
	} else {
		s.failRestExcept(ctx, "the agent did not report this resource", wire.GuestConfigID)
	}
	// 4. The stop of power: stopped, after the agent steps (D-299). A
	// failed agent step keeps the guest running, so it can be planned again.
	if open {
		if !s.ok {
			s.set(ctx, wire.GuestConfigID, wire.StatusFailed, gres.changes, fmt.Sprintf(textNotStopped, "an agent step of this Run failed"))
		} else if err := e.stopGuest(ctx, tp.Target); err != nil {
			s.set(ctx, wire.GuestConfigID, wire.StatusFailed, gres.changes, err.Error())
		} else {
			s.set(ctx, wire.GuestConfigID, wire.StatusApplied, gres.changes, gres.output)
		}
		open = false
	}
	// 5. The agent watches the new desired list for drift (the deferred
	// send; a stopped agent gets it with the hello of its next start).
	return s.ok
}

// desiredPower is the rendered power of a Target, "" when not managed.
func desiredPower(r *state.Rendered) driver.Power {
	p, _ := r.Guest.Attrs[driver.AttrPower].(string)
	return driver.Power(p)
}

// guestStopped reports whether the guest of a target is stopped now. An
// error reads as no: the agent steps then run and fail as before.
func (e *Engine) guestStopped(ctx context.Context, target string) bool {
	b, err := e.st.GetBinding(ctx, state.KindTarget, target)
	if err != nil {
		return false
	}
	st, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
	return err == nil && st.Power == driver.PowerStopped
}

// stopGuest stops the guest of a target at the end of its Run (D-299)
// and stores the new poll key, so the stop is no drift.
func (e *Engine) stopGuest(ctx context.Context, target string) error {
	b, err := e.st.GetBinding(ctx, state.KindTarget, target)
	if err != nil {
		return err
	}
	id := driver.GuestID(b.Ref)
	if err := e.cfg.Driver.Power(ctx, id, driver.PowerStop); err != nil {
		return fmt.Errorf("the stop failed: %w", err)
	}
	st, err := e.cfg.Driver.Observe(ctx, id)
	if err != nil {
		return fmt.Errorf("the guest cannot be read after the stop: %w", err)
	}
	if st.Power != driver.PowerStopped {
		return fmt.Errorf("the guest still runs after the stop; %s", forKind("check the VM in Proxmox", st.Spec.Kind))
	}
	b.Digest = pollKey(st)
	return e.st.PutBinding(ctx, b)
}

var errRefused = errors.New("refused: the guest changed since the Plan; its changes are above the approved risk")

// hellos returns how many hellos the target's agent sent since the
// controller started. It counts every hello, not only one of a new boot:
// a link reconnect between the count and the restart ends the wait
// early (review N1). The resource steps still need the agent online, and
// the agent observes again before it applies (D-075).
func (e *Engine) hellos(target string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hello[target]
}

// waitOnline waits until the agent is online and, when after >= 0, has
// sent a hello after the count after: a hello of a new boot.
func (e *Engine) waitOnline(ctx context.Context, target string, after int) error {
	deadline := time.NewTimer(e.cfg.EnrollTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !e.cfg.Agents.Online(target) || (after >= 0 && e.hellos(target) <= after) {
		select {
		case <-tick.C:
		case <-deadline.C:
			return fmt.Errorf("the agent of %s did not come online within %v", target, e.cfg.EnrollTimeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// guestResult is what the guest step did.
type guestResult struct {
	changes   []wire.Change // recorded in the step
	output    string
	restarted bool // the guest had a new boot
	// grow: a VM disk grew to this many GiB; the agent grows the root
	// file system once it is online (D-299). The step stays running.
	grow int
	// stop: the Run stops the guest after its agent steps (D-299). The
	// step stays running until then.
	stop bool
}

// guestStep does the driver part of a target. It returns the changes it made.
//
// ref is the template that the Run fetched already (a container), or "".
func (e *Engine) guestStep(ctx context.Context, run store.Run, set *state.Set, tp TargetPlan, r *state.Rendered, planned ResourceChange, ref driver.ImageRef) (guestResult, error) {
	var res guestResult
	switch tp.Guest {
	case "delete":
		b, err := e.st.GetBinding(ctx, state.KindTarget, tp.Target)
		if err != nil {
			return res, err
		}
		if err := e.cfg.Driver.Delete(ctx, driver.GuestID(b.Ref)); err != nil && !errors.Is(err, driver.ErrNotFound) {
			return res, err // the guest may still run: its agent stays (D-211)
		}
		// The guest is gone: its credential (in a backup, a template or a
		// copy of the disk) must stop working before the binding goes.
		if err := e.cfg.Agents.Revoke(ctx, tp.Target); err != nil {
			return res, err
		}
		return res, e.st.DeleteBinding(ctx, state.KindTarget, tp.Target)

	case "create":
		spec, err := e.guestSpec(tp.Target, r)
		if err != nil {
			return res, err
		}
		// A built Image: the newest ready version (D-200); a stock Image:
		// the base template (D-188).
		if info, ok := set.ImageInfo(r.Image); ok && info.Built {
			var unpin func()
			if spec.Image, spec.ImageVersion, unpin, err = e.newestVersion(ctx, r.Image); err != nil {
				return res, err
			}
			defer unpin() // the clone is done when guestStep returns
		} else if ref != "" {
			spec.Image = ref
		} else if spec.Image, err = e.fetchImage(ctx, run.RevisionID, r.Image); err != nil {
			return res, err
		}
		// T-074: the full disk counts until the clone is done; then the
		// real use that the driver reads counts.
		release, err := e.disk.Reserve(ctx, DiskBytes(spec.DiskGB), fmt.Sprintf("the %d GiB disk of %s", spec.DiskGB, tp.Target))
		if err != nil {
			return res, fmt.Errorf("%w; nothing was made: free space on the storage or choose a smaller disk", err)
		}
		token, _, err := e.cfg.Agents.NewEnrollToken(ctx, tp.Target)
		if err != nil {
			release()
			return res, err
		}
		id, err := e.cfg.Driver.Create(ctx, spec, driver.Bootstrap{Token: token})
		release()
		if err != nil {
			return res, err
		}
		// The guest exists: its record must too, also when Stop cancels
		// the Run now (review S1 of #159). Without a binding no Run could
		// delete it or clear a container's token.
		keep := context.WithoutCancel(ctx)
		digest := ""
		if st, err := e.cfg.Driver.Observe(keep, id); err == nil {
			digest = pollKey(st)
		}
		res.changes = createChanges(spec)
		res.stop = spec.Power == driver.PowerStopped // created, enrolled and applied, then stopped (D-299)
		return res, e.st.PutBinding(keep, store.Binding{Kind: state.KindTarget, Name: tp.Target, Ref: string(id), Digest: digest})

	case "update", "reenroll":
		return e.updateGuest(ctx, tp, r, planned)
	}
	return res, fmt.Errorf("plan: unknown guest action %q", tp.Guest)
}

// updateGuest is the guest step of an existing guest (D-194). It observes
// the power state before any write and uses the rule of the Plan with it.
func (e *Engine) updateGuest(ctx context.Context, tp TargetPlan, r *state.Rendered, planned ResourceChange) (guestResult, error) {
	var res guestResult
	b, err := e.st.GetBinding(ctx, state.KindTarget, tp.Target)
	if err != nil {
		return res, err
	}
	id := driver.GuestID(b.Ref)
	desired, err := e.guestSpec(tp.Target, r)
	if err != nil {
		return res, err
	}
	observed, err := e.cfg.Driver.Observe(ctx, id)
	if err != nil {
		return res, err
	}
	changes, err := e.cfg.Driver.Diff(desired, observed)
	if err != nil {
		return res, err
	}
	reenroll := tp.Guest == "reenroll"
	running := observed.Power == driver.PowerRunning
	// B1: a guest that someone started after the Plan was made is not
	// restarted. No token, no write, no power action.
	if reenroll && running && !r.AllowRestart {
		return res, errors.New(forKind(textReenrollRuns, desired.Kind))
	}
	// Day 2 (D-299): the Plan checks again with the guest as it is now.
	if !reenroll {
		if powerTo(changes) == driver.PowerStopped && !r.AllowRestart {
			return res, errors.New(textStopNeedsRestart)
		}
		if driver.Grow(changes) != nil && !runsAfter(desired, observed) {
			return res, errors.New(textGrowNotRunning)
		}
	}
	recorded, restart, err := restartRule(desired.Kind, changes, driver.WaitingGuest(desired, observed), rulePower(desired, observed, reenroll), r.AllowRestart, reenroll)
	if err != nil {
		return res, err
	}
	if restart && !reenroll && !hasAttr(planned.Changes, wire.AttrRestart) {
		return guestResult{changes: recorded}, errors.New(textRestartNotPlanned)
	}
	for _, c := range planned.Changes {
		if c.Attr == AttrAllowRestart { // Plan-only: recorded, never written
			recorded = append([]wire.Change{c}, recorded...)
		}
	}
	res.changes = recorded
	if wire.MaxRisk(recorded).Exceeds(planned.Risk) {
		return res, errRefused
	}
	var boot *driver.Bootstrap
	if reenroll {
		token, _, err := e.cfg.Agents.NewEnrollToken(ctx, tp.Target)
		if err != nil {
			return res, err
		}
		boot = &driver.Bootstrap{Token: token}
		recorded = append(recorded, wire.Change{Attr: driver.AttrEnrollToken, Sensitive: true, Risk: wire.RiskDisruptive})
		res.changes = recorded
	}
	// The config first, also on a stopped guest: a start then makes the
	// new values active with no restart. The disk grows after the start,
	// so a VM's agent can grow its file system (D-308).
	write := driverChanges(recorded)
	grow := driver.Grow(write)
	write = slices.DeleteFunc(write, func(c wire.Change) bool { return c.Attr == driver.AttrDisk })
	if len(write) > 0 {
		if err := e.cfg.Driver.Update(ctx, id, write, boot); err != nil {
			return res, err
		}
	}
	after, err := e.cfg.Driver.Observe(ctx, id)
	if err != nil {
		return res, err
	}
	switch {
	case reenroll && !running:
		// Start, never restart: a guest that someone started meanwhile
		// is not restarted (D-194).
		if err := e.cfg.Driver.Power(ctx, id, driver.PowerStart); err != nil {
			return res, err
		}
		res.restarted = true
		if after, err = e.cfg.Driver.Observe(ctx, id); err != nil {
			return res, err
		}
		if after.TokenPending {
			if desired.Kind == wire.KindLXC {
				return res, errors.New(textStartedOutsideCT)
			}
			return res, errors.New(textStartedOutside)
		}
	case restart:
		// The bridge first: a bridge that waits is no reason to restart.
		if _, err := checkWritten(recorded, desired, after); err != nil {
			return res, err
		}
		if reenroll || after.TokenPending || len(driver.WaitingGuest(desired, after)) > 0 {
			if err := e.cfg.Driver.Power(ctx, id, driver.PowerRestart); err != nil {
				return res, e.restartFailed(ctx, id, err)
			}
			res.restarted = true
			if after, err = e.cfg.Driver.Observe(ctx, id); err != nil {
				return res, err
			}
			if w := driver.WaitingGuest(desired, after); len(w) > 0 || after.TokenPending {
				return res, fmt.Errorf("after the restart a value still waits for a stop and start (%s); %s", waitingNames(w, after.TokenPending), forKind("check the VM in Proxmox", desired.Kind))
			}
		} else {
			// Proxmox applied it live: the planned restart is skipped.
			recorded = slices.DeleteFunc(recorded, func(c wire.Change) bool { return c.Attr == wire.AttrRestart })
		}
	}
	if !reenroll && desired.Power == driver.PowerRunning && after.Power != driver.PowerRunning {
		if err := e.cfg.Driver.Power(ctx, id, driver.PowerStart); err != nil {
			return res, err
		}
		res.restarted = true
		if after, err = e.cfg.Driver.Observe(ctx, id); err != nil {
			return res, err
		}
	}
	if grow != nil {
		if err := e.growDisk(ctx, tp.Target, id, *grow, after); err != nil {
			return res, err
		}
		if after, err = e.cfg.Driver.Observe(ctx, id); err != nil {
			return res, err
		}
		if desired.Kind == wire.KindVM {
			res.grow = desired.DiskGB
		}
	}
	recorded, err = checkWritten(recorded, desired, after)
	res.changes = recorded
	if err != nil {
		return res, err
	}
	if err := checkDay2Written(recorded, desired, after); err != nil {
		return res, err
	}
	res.output = waitingOutput(recorded)
	res.stop = !reenroll && powerTo(planned.Changes) == driver.PowerStopped && powerTo(recorded) == driver.PowerStopped
	b.Digest = pollKey(after)
	return res, e.st.PutBinding(ctx, b)
}

// growDisk grows the disk of a running guest by the change c, inside the
// disk gate of D-219 (T-074): the added bytes count until the resize is
// done.
func (e *Engine) growDisk(ctx context.Context, target string, id driver.GuestID, c wire.Change, now driver.GuestState) error {
	if now.Power != driver.PowerRunning {
		return errors.New(textGrowNotRunning)
	}
	from, _ := driver.IntValue(c.From)
	to, err := driver.IntValue(c.To)
	if err != nil {
		return err
	}
	release, err := e.disk.Reserve(ctx, DiskBytes(to-from), fmt.Sprintf("the %d GiB disk grow of %s", to-from, target))
	if err != nil {
		return fmt.Errorf("%w; the disk was not grown: free space on the storage or grow less", err)
	}
	defer release()
	return e.cfg.Driver.Update(ctx, id, []wire.Change{c}, nil)
}

// Step ids of a container target (T-038 §4).
const (
	// StepImageFetch fetches, repacks and uploads the template of a new
	// container (D-236, D-237).
	StepImageFetch = "image:fetch"
	// StepGuestBootstrap removes the used enrollment token from the
	// container config (D-231).
	StepGuestBootstrap = "guest:bootstrap"
)

// tokenInConfig reports whether the container of a target still has an
// enrollment token in its config: a clear step that failed or never ran
// (a Run that ended before the hello). An error reads as no.
func (e *Engine) tokenInConfig(ctx context.Context, target string) bool {
	b, err := e.st.GetBinding(ctx, state.KindTarget, target)
	if err != nil {
		return false
	}
	st, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
	if err != nil {
		e.log.Warn("observe for the bootstrap step", "target", target, "err", err)
		return false
	}
	return st.TokenInConfig
}

// clearStep runs the step guest:bootstrap. The binding gets the new
// digest, so the clear is not drift.
func (e *Engine) clearStep(ctx context.Context, s *stepper, target string) {
	s.set(ctx, StepGuestBootstrap, wire.StatusRunning, nil, "")
	fail := func(err error) {
		s.set(ctx, StepGuestBootstrap, wire.StatusFailed, nil,
			"the used enrollment token is still in the container config: "+err.Error()+"; it is dead, and the next Run of the target removes it")
	}
	b, err := e.st.GetBinding(ctx, state.KindTarget, target)
	if err != nil {
		fail(err)
		return
	}
	id := driver.GuestID(b.Ref)
	if err := e.cfg.Driver.ClearBootstrap(ctx, id); err != nil {
		fail(err)
		return
	}
	st, err := e.cfg.Driver.Observe(ctx, id)
	if err != nil {
		fail(err)
		return
	}
	if st.TokenInConfig {
		fail(errors.New("the config still shows it after the clear"))
		return
	}
	b.Digest = pollKey(st)
	if err := e.st.PutBinding(ctx, b); err != nil {
		fail(err)
		return
	}
	s.set(ctx, StepGuestBootstrap, wire.StatusApplied, nil,
		"the used enrollment token is removed from the container config; on a running container Proxmox applies the removal at the next restart")
}

func waitingNames(w []wire.Change, token bool) string {
	var names []string
	for _, c := range w {
		names = append(names, c.Attr)
	}
	if token {
		names = append(names, driver.AttrEnrollToken)
	}
	return strings.Join(names, ", ")
}

// fetchImage asks the driver for the image of a new guest and returns
// its ref. It reads the Image document of the Run's revision and keeps no
// binding: the driver finds the template again or imports it again, so a
// deleted, changed or destroyed template is never stale (D-188).
func (e *Engine) fetchImage(ctx context.Context, revision int64, name string) (driver.ImageRef, error) {
	row, err := e.st.RevisionDoc(ctx, revision, state.KindImage, name)
	if err != nil {
		return "", fmt.Errorf("image %s: %w", name, err)
	}
	var doc struct {
		Spec struct {
			Type   string `json:"type"`
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
			SHA512 string `json:"sha512"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(row.Body, &doc); err != nil {
		return "", err
	}
	ref, err := e.cfg.Driver.FetchImage(ctx, driver.ImageSpec{Name: name, Kind: wire.Kind(doc.Spec.Type), URL: doc.Spec.URL, SHA256: doc.Spec.SHA256, SHA512: doc.Spec.SHA512})
	if err != nil {
		return "", err
	}
	e.log.Info("image ready", "image", name, "ref", ref)
	return ref, nil
}

// Recover closes what a controller crash left open. Call it once at
// start, before the link, the API and the guest poll start (review T-010
// n3): it skips only Runs that this process already committed, so a call
// while approvals happen could fail a Run that is just starting. A Run in the
// database with status running has no goroutine after a restart: its
// unfinished steps fail, the Run fails, and OnPlanDone opens its Issues
// again. There is no automatic resend (D-067); the user plans again.
func (e *Engine) Recover(ctx context.Context) error {
	runs, err := e.st.RunningRuns(ctx)
	if err != nil {
		return err
	}
	// Builds first: their versions fail, their agents are revoked and
	// their guests removed (S4).
	if err := e.recoverBuilds(ctx); err != nil {
		return err
	}
	for _, run := range runs {
		e.mu.Lock()
		active := e.active[run.ID]
		e.mu.Unlock()
		if active {
			continue // a Run of this process
		}
		if err := e.st.FailUnfinishedSteps(ctx, run.ID, "the controller restarted during the Run"); err != nil {
			return err
		}
		if err := e.finishDeletes(ctx, run); err != nil {
			return err
		}
		if err := e.st.FinishRun(ctx, run.ID, "failed", e.waitsOfRun(ctx, run.ID)); err != nil {
			return err
		}
		e.emit(ctx, "run.finished", fmt.Sprintf("run:%d", run.ID), "system",
			map[string]any{"plan": run.PlanID, "status": "failed", "reason": "controller restart"})
		if e.cfg.OnPlanDone != nil {
			e.cfg.OnPlanDone(ctx, run.PlanID, false)
		}
	}
	return nil
}

// finishDeletes ends the delete steps that a crash cut off (D-211): when
// the guest of a deleted target is gone but its binding is still there,
// the agent is revoked and the binding deleted, as the step would have
// done. A guest that still exists keeps its agent and binding.
func (e *Engine) finishDeletes(ctx context.Context, run store.Run) error {
	p, err := e.st.GetPlan(ctx, run.PlanID)
	if err != nil {
		return err
	}
	var targets []TargetPlan
	if err := json.Unmarshal(p.Changes, &targets); err != nil {
		return err
	}
	for _, tp := range targets {
		if tp.Guest != "delete" {
			continue
		}
		b, err := e.st.GetBinding(ctx, state.KindTarget, tp.Target)
		if errors.Is(err, store.ErrNotFound) {
			continue // the step finished
		}
		if err != nil {
			return err
		}
		if _, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref)); !errors.Is(err, driver.ErrNotFound) {
			if err != nil {
				e.log.Warn("recover: guest of a deleted target not checked; agent kept", "target", tp.Target, "err", err)
			}
			continue
		}
		if err := e.cfg.Agents.Revoke(ctx, tp.Target); err != nil {
			return err
		}
		if err := e.st.DeleteBinding(ctx, state.KindTarget, tp.Target); err != nil {
			return err
		}
	}
	return nil
}

// checkIdle returns ErrTargetBusy when a Run works on one of the targets.
func (e *Engine) checkIdle(targets []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range targets {
		if e.busy[t] > 0 {
			return &ProposalError{Target: t, Err: ErrTargetBusy, Msg: "a Run is working on this target; wait for it to finish, then plan again"}
		}
	}
	return nil
}

// reserve marks the targets busy, or fails when one is busy already.
func (e *Engine) reserve(targets []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range targets {
		if e.busy[t] > 0 {
			return &ProposalError{Target: t, Err: ErrTargetBusy, Msg: "a Run is working on this target; approve after it finished"}
		}
	}
	for _, t := range targets {
		e.busy[t]++
	}
	return nil
}

func (e *Engine) release(targets []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range targets {
		if e.busy[t]--; e.busy[t] <= 0 {
			delete(e.busy, t)
		}
	}
}

// planned lists what the approved Plan shows for the agent resources of a
// target: the changes, or unverified for a resource of a new target. The
// stored changes are redacted already, so no secret value goes back.
func planned(tp TargetPlan) []wire.Planned {
	out := []wire.Planned{}
	for _, rc := range tp.Resources {
		if rc.Resource == wire.GuestConfigID || (len(rc.Changes) == 0 && !rc.Unverified) {
			continue
		}
		out = append(out, wire.Planned{Resource: rc.Resource, Changes: rc.Changes, Unverified: rc.Unverified})
	}
	return out
}

// Discard drops a pending Plan: it becomes stale, and the Issues that it
// would resolve are open again (review T-010 m5).
func (e *Engine) Discard(ctx context.Context, planID int64, principal string) error {
	if err := e.st.DiscardPlan(ctx, planID); err != nil {
		return err
	}
	e.emit(ctx, "plan.stale", fmt.Sprintf("plan:%d", planID), principal, map[string]any{"discarded": true})
	if e.cfg.OnPlanDone != nil {
		e.cfg.OnPlanDone(ctx, planID, false)
	}
	return nil
}
