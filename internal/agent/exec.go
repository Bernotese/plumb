package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// handle runs one command and returns its result report.
func (a *Agent) handle(ctx context.Context, cmd wire.Command) wire.Report {
	rep := wire.Report{Type: wire.ReportResult, CommandID: cmd.ID}
	fail := func(err error) wire.Report {
		rep.Error = err.Error()
		return rep
	}
	switch cmd.Type {
	case wire.CommandObserve:
		var body wire.ObserveBody
		if err := json.Unmarshal(cmd.Body, &body); err != nil {
			return fail(fmt.Errorf("observe body: %w", err))
		}
		rep.Items = a.observe(ctx, body.Resources)
	case wire.CommandApply:
		var body wire.ApplyBody
		if err := json.Unmarshal(cmd.Body, &body); err != nil {
			return fail(fmt.Errorf("apply body: %w", err))
		}
		if !body.MaxRisk.Valid() {
			return fail(fmt.Errorf("apply: invalid max_risk %q", body.MaxRisk))
		}
		items, err := a.apply(ctx, cmd.ID, body)
		if err != nil {
			return fail(err)
		}
		rep.Items = items
	case wire.CommandWatch:
		var body wire.WatchBody
		if err := json.Unmarshal(cmd.Body, &body); err != nil {
			return fail(fmt.Errorf("watch body: %w", err))
		}
		// The watch mark first: a watched agent never seals (D-207).
		if err := a.state.markWatch(); err != nil {
			return fail(fmt.Errorf("store watch mark: %w", err))
		}
		if err := a.setDesired(body.Resources); err != nil {
			return fail(err)
		}
	case wire.CommandSeal:
		var body wire.SealBody
		if err := json.Unmarshal(cmd.Body, &body); err != nil {
			return fail(fmt.Errorf("seal body: %w", err))
		}
		rep.Items = a.seal(ctx, cmd.ID, body)
	case wire.CommandGrow:
		var body wire.GrowBody
		if err := json.Unmarshal(cmd.Body, &body); err != nil {
			return fail(fmt.Errorf("grow body: %w", err))
		}
		rep.Items = []wire.Item{a.grow(ctx, body)}
	case wire.CommandUpdate:
		var body wire.UpdateBody
		if err := json.Unmarshal(cmd.Body, &body); err != nil {
			return fail(fmt.Errorf("update body: %w", err))
		}
		rep.Items = []wire.Item{a.selfUpdate(ctx, cmd.ID, body)}
	default:
		return fail(fmt.Errorf("unknown command type %q", cmd.Type))
	}
	return rep
}

// observeOne observes and diffs one resource.
func (a *Agent) observeOne(ctx context.Context, r wire.Resource) (resource.Resource, wire.Item) {
	item := wire.Item{Resource: r.ID}
	// D-207: never the agent's own files or unit, also through a link
	// that exists only in the guest (review S1 of T-059).
	if why := a.ownPathRefusal(r); why != "" {
		item.Status, item.Output = wire.StatusRefused, resource.SafeOutput(r, why)
		return nil, item
	}
	rs, err := resource.New(r, a.env)
	if err != nil {
		item.Status, item.Output = wire.StatusFailed, resource.SafeOutput(r, err.Error())
		return nil, item
	}
	st, err := rs.Observe(ctx)
	if err != nil {
		item.Status, item.Output = wire.StatusFailed, resource.SafeOutput(r, err.Error())
		return rs, item
	}
	item.Changes = rs.Diff(st)
	item.Status = wire.StatusUnchanged
	if len(item.Changes) > 0 {
		item.Status = wire.StatusDiffers
	}
	return rs, item
}

// observe answers an observe command: the difference of every resource,
// plus the restart change of a service whose restart_on resources differ,
// so the Plan shows it (D-036).
func (a *Agent) observe(ctx context.Context, list []wire.Resource) []wire.Item {
	items := make([]wire.Item, len(list))
	var changed []string
	for i, r := range list {
		_, items[i] = a.observeOne(ctx, r)
		if items[i].Status == wire.StatusDiffers {
			changed = append(changed, r.ID)
		}
	}
	for i, r := range list {
		if c, ok := resource.RestartChange(r, changed); ok && items[i].Status != wire.StatusFailed && items[i].Status != wire.StatusRefused {
			items[i].Changes = append(items[i].Changes, c)
			items[i].Status = wire.StatusDiffers
		}
	}
	return items
}

// step is one resource of an apply. Its waits come from wire.Order and keep
// their kind apart (D-170): only restartDeps can restart a service.
type step struct {
	r           wire.Resource
	needs       []*step       // after: runs only when each ended unchanged or applied
	restartDeps []*step       // restart_on: waits; restarts when one changed; runs also when one failed
	prev        []*step       // the earlier steps of the same Block use (they ended before this one)
	done        chan struct{} // closed when the step ended
	changed     bool          // the step applied changes (read after done)
	item        wire.Item
}

// succeeded reports whether a step that ended lets the steps after it run.
func succeeded(st wire.Status) bool { return st == wire.StatusUnchanged || st == wire.StatusApplied }

// skipped is the item of a step that did not run. The output names the
// cause, so no skipped row is empty (D-170, N1).
func skipped(s *step, cause string) wire.Item {
	return wire.Item{Resource: s.r.ID, Status: wire.StatusSkipped, Output: "not applied: " + cause}
}

// apply answers an apply command. Each Block use runs in its listed order,
// in its own goroutine; uses wait for each other only through after and
// restart_on (wire.Order, D-168). A step with after waits for those steps
// and runs only when each ended unchanged or applied; else it is skipped. A
// service waits for its restart_on resources, restarts once when one of
// them changed, and still runs when one failed. Every resource is observed
// again; a change above max_risk is refused and nothing of it is applied.
// After a step that failed, was refused or was skipped, the rest of its use
// is skipped. When ctx ends during a wait, the waiting step and the rest of
// its use are skipped, and apply returns: every goroutine of the apply has
// ended when it returns.
func (a *Agent) apply(ctx context.Context, cmdID string, body wire.ApplyBody) ([]wire.Item, error) {
	steps, blocks, err := plan(body.Resources)
	if err != nil {
		return nil, err
	}
	planned, err := plannedByID(body)
	if err != nil {
		return nil, err
	}
	// The applied resources become part of the desired list, so the drift
	// check after the apply compares against what was applied.
	// No drift check runs while the apply changes the guest.
	a.applyLock.Lock()
	a.beginApply(body.Resources)
	defer a.endApply(body.Resources)
	defer a.applyLock.Unlock()
	if err := a.saveDesired(); err != nil {
		return nil, fmt.Errorf("store desired list: %w", err)
	}

	var wg sync.WaitGroup
	for _, block := range blocks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cause := "" // set by the first step of the use that did not succeed
			for _, s := range block {
				if cause != "" {
					s.item = skipped(s, cause)
				} else {
					a.applyStep(ctx, cmdID, s, body.MaxRisk, planned)
					if st := s.item.Status; !succeeded(st) {
						cause = fmt.Sprintf("%s was %s", s.r.ID, st)
					}
				}
				close(s.done)
				a.progress(ctx, cmdID, s.item)
			}
		}()
	}
	wg.Wait()
	items := make([]wire.Item, len(steps))
	for i, s := range steps {
		items[i] = s.item
	}
	return items, nil
}

// notPlanned is the output of a resource whose difference the approved
// Plan does not show (D-075).
const notPlanned = "the guest differs from the approved Plan; plan again"

// plannedByID checks the planned entries of an apply: each names a resource
// of the body, and at most once.
func plannedByID(body wire.ApplyBody) (map[string]wire.Planned, error) {
	inBody := map[string]bool{}
	for _, r := range body.Resources {
		inBody[r.ID] = true
	}
	out := map[string]wire.Planned{}
	for _, p := range body.Planned {
		if !inBody[p.Resource] {
			return nil, fmt.Errorf("planned resource %s is not in the resource list", p.Resource)
		}
		if _, dup := out[p.Resource]; dup {
			return nil, fmt.Errorf("planned resource %s is listed twice", p.Resource)
		}
		out[p.Resource] = p
	}
	return out, nil
}

// applyStep applies one resource (D-075): observe again; nothing differs →
// unchanged; the new difference must be covered by the planned changes
// (or the entry is unverified) and stay within max_risk, else refused and
// nothing of the resource changes. The agent never fixes a difference that
// the Plan does not show; the drift check reports it.
func (a *Agent) applyStep(ctx context.Context, cmdID string, s *step, maxRisk wire.Risk, planned map[string]wire.Planned) {
	// wait returns false when ctx ends first; the step is then skipped.
	wait := func(dep *step) bool {
		select {
		case <-dep.done:
			return true
		case <-ctx.Done():
			s.item = skipped(s, "the apply ended while this resource waited for "+dep.r.ID)
			return false
		}
	}
	for _, dep := range s.needs {
		if !wait(dep) {
			return
		}
		if st := dep.item.Status; !succeeded(st) {
			s.item = skipped(s, fmt.Sprintf("%s was %s", dep.r.ID, st))
			return
		}
	}
	var changedBefore []string
	for _, dep := range s.restartDeps {
		if !wait(dep) {
			return
		}
		if dep.changed {
			changedBefore = append(changedBefore, dep.r.ID)
		}
	}
	rs, item := a.observeOne(ctx, s.r)
	if item.Status == wire.StatusFailed || item.Status == wire.StatusRefused {
		s.item = item
		return
	}
	if c, ok := resource.RestartChange(s.r, changedBefore); ok {
		item.Changes = append(item.Changes, c)
	}
	if len(item.Changes) == 0 {
		s.item = wire.Item{Resource: s.r.ID, Status: wire.StatusUnchanged}
		return
	}
	if p, ok := planned[s.r.ID]; !ok || (!p.Unverified && !wire.Covers(p.Changes, item.Changes) &&
		!resource.NewUnitCovers(s.r, p.Changes, item.Changes, s.madeBefore())) {
		s.item = wire.Item{Resource: s.r.ID, Status: wire.StatusRefused, Changes: item.Changes,
			Output: resource.SafeOutput(s.r, notPlanned)}
		return
	}
	for _, c := range item.Changes {
		if c.Risk.Exceeds(maxRisk) {
			s.item = wire.Item{Resource: s.r.ID, Status: wire.StatusRefused, Changes: item.Changes,
				Output: resource.SafeOutput(s.r, fmt.Sprintf("change of %s has risk %s, above the approved %s", c.Attr, c.Risk, maxRisk))}
			return
		}
	}
	if isSafeApply(s.r) {
		// D-300: live apply, round trip, then commit or restore.
		s.item = a.safeApply(ctx, cmdID, s.r, rs, item.Changes)
		s.changed = s.item.Status == wire.StatusApplied
		return
	}
	if err := rs.Apply(ctx, item.Changes); err != nil {
		s.item = wire.Item{Resource: s.r.ID, Status: wire.StatusFailed, Changes: item.Changes, Output: resource.SafeOutput(s.r, err.Error())}
		return
	}
	s.changed = true
	s.item = wire.Item{Resource: s.r.ID, Status: wire.StatusApplied, Changes: item.Changes}
}

// madeBefore returns the resources that this step waited for (after,
// restart_on, earlier in its Block use) and that this apply created. Each of
// those steps has ended, so its result can be read.
func (s *step) madeBefore() []wire.Resource {
	var out []wire.Resource
	seen := map[*step]bool{}
	for _, list := range [][]*step{s.needs, s.restartDeps, s.prev} {
		for _, dep := range list {
			if seen[dep] || !dep.changed || wire.ActionOf(dep.item.Changes) != wire.ActionCreate {
				continue
			}
			seen[dep] = true
			out = append(out, dep.r)
		}
	}
	return out
}

// progress sends one step result while the apply runs. It is best effort:
// the final result report holds every item.
func (a *Agent) progress(ctx context.Context, cmdID string, item wire.Item) {
	if err := a.send(ctx, wire.Report{Type: wire.ReportProgress, CommandID: cmdID, Items: []wire.Item{item}}); err != nil {
		a.log.Debug("progress report failed", "err", err)
	}
}

// plan builds the steps of an apply from wire.Order, the one rule for the
// apply order (D-168): Block uses in order of first appearance, and the
// after and restart_on waits of each step. A duplicate id, an unknown after
// id or a cycle fails the apply before anything runs.
func plan(list []wire.Resource) ([]*step, [][]*step, error) {
	edges, err := wire.Order(list)
	if err != nil {
		return nil, nil, err
	}
	steps := make([]*step, len(list))
	var blocks [][]*step
	blockIdx := map[string]int{}
	for i, r := range list {
		s := &step{r: r, done: make(chan struct{})}
		steps[i] = s
		bi, ok := blockIdx[r.Block]
		if !ok {
			bi = len(blocks)
			blockIdx[r.Block] = bi
			blocks = append(blocks, nil)
		}
		s.prev = slices.Clone(blocks[bi])
		blocks[bi] = append(blocks[bi], s)
	}
	for i, s := range steps {
		for _, e := range edges[i] {
			switch e.Kind {
			case wire.EdgeAfter:
				s.needs = append(s.needs, steps[e.To])
			case wire.EdgeRestart:
				s.restartDeps = append(s.restartDeps, steps[e.To])
			case wire.EdgeBlock:
				// The goroutine of the use runs its steps in order.
			}
		}
	}
	return steps, blocks, nil
}
