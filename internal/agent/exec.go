package agent

import (
	"context"
	"encoding/json"
	"fmt"
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
		if err := a.setDesired(body.Resources); err != nil {
			return fail(err)
		}
	default:
		return fail(fmt.Errorf("unknown command type %q", cmd.Type))
	}
	return rep
}

// observeOne observes and diffs one resource.
func (a *Agent) observeOne(ctx context.Context, r wire.Resource) (resource.Resource, wire.Item) {
	item := wire.Item{Resource: r.ID}
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
		if c, ok := resource.RestartChange(r, changed); ok && items[i].Status != wire.StatusFailed {
			items[i].Changes = append(items[i].Changes, c)
			items[i].Status = wire.StatusDiffers
		}
	}
	return items
}

// step is one resource of an apply.
type step struct {
	r       wire.Resource
	after   []*step       // restart_on resources of this apply
	done    chan struct{} // closed when the step ended
	changed bool          // the step applied changes (read after done)
	item    wire.Item
}

// apply answers an apply command. Resources of one Block run in the listed
// order; Blocks run in parallel. A service waits for its restart_on
// resources and restarts once when one of them changed. Every resource is
// observed again; a change above max_risk is refused and nothing of it is
// applied. After a failed or refused step, the rest of its Block is skipped.
func (a *Agent) apply(ctx context.Context, cmdID string, body wire.ApplyBody) ([]wire.Item, error) {
	steps, blocks, err := plan(body.Resources)
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
			failed := false
			for _, s := range block {
				if failed {
					s.item = wire.Item{Resource: s.r.ID, Status: wire.StatusSkipped}
				} else {
					a.applyStep(ctx, s, body.MaxRisk)
					st := s.item.Status
					failed = st == wire.StatusFailed || st == wire.StatusRefused
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

func (a *Agent) applyStep(ctx context.Context, s *step, maxRisk wire.Risk) {
	var changedBefore []string
	for _, dep := range s.after {
		select {
		case <-dep.done:
		case <-ctx.Done():
			s.item = wire.Item{Resource: s.r.ID, Status: wire.StatusSkipped}
			return
		}
		if dep.changed {
			changedBefore = append(changedBefore, dep.r.ID)
		}
	}
	rs, item := a.observeOne(ctx, s.r)
	if item.Status == wire.StatusFailed {
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
	for _, c := range item.Changes {
		if c.Risk.Exceeds(maxRisk) {
			s.item = wire.Item{Resource: s.r.ID, Status: wire.StatusRefused, Changes: item.Changes,
				Output: resource.SafeOutput(s.r, fmt.Sprintf("change of %s has risk %s, above the approved %s", c.Attr, c.Risk, maxRisk))}
			return
		}
	}
	if err := rs.Apply(ctx, item.Changes); err != nil {
		s.item = wire.Item{Resource: s.r.ID, Status: wire.StatusFailed, Changes: item.Changes, Output: resource.SafeOutput(s.r, err.Error())}
		return
	}
	s.changed = true
	s.item = wire.Item{Resource: s.r.ID, Status: wire.StatusApplied, Changes: item.Changes}
}

// progress sends one step result while the apply runs. It is best effort:
// the final result report holds every item.
func (a *Agent) progress(ctx context.Context, cmdID string, item wire.Item) {
	if err := a.send(ctx, wire.Report{Type: wire.ReportProgress, CommandID: cmdID, Items: []wire.Item{item}}); err != nil {
		a.log.Debug("progress report failed", "err", err)
	}
}

// plan builds the steps of an apply: blocks in order of first appearance,
// restart_on links, and a check for duplicate ids and for cycles (a service
// that waits for a resource that waits for the service would never run).
func plan(list []wire.Resource) ([]*step, [][]*step, error) {
	steps := make([]*step, len(list))
	byID := map[string]*step{}
	var blocks [][]*step
	blockIdx := map[string]int{}
	for i, r := range list {
		if byID[r.ID] != nil {
			return nil, nil, fmt.Errorf("resource %s is listed twice", r.ID)
		}
		s := &step{r: r, done: make(chan struct{})}
		steps[i], byID[r.ID] = s, s
		bi, ok := blockIdx[r.Block]
		if !ok {
			bi = len(blocks)
			blockIdx[r.Block] = bi
			blocks = append(blocks, nil)
		}
		blocks[bi] = append(blocks[bi], s)
	}
	// A step waits for the step before it in its Block and for its
	// restart_on resources. Find cycles in that graph.
	wait := map[*step][]*step{}
	for _, block := range blocks {
		for i, s := range block {
			if i > 0 {
				wait[s] = append(wait[s], block[i-1])
			}
		}
	}
	for _, s := range steps {
		if s.r.Type != "service" {
			continue
		}
		for _, id := range restartOn(s.r) {
			if dep := byID[id]; dep != nil && dep != s {
				s.after = append(s.after, dep)
				wait[s] = append(wait[s], dep)
			}
		}
	}
	const (
		white = iota
		grey
		black
	)
	color := map[*step]int{}
	var visit func(*step) error
	visit = func(s *step) error {
		color[s] = grey
		for _, d := range wait[s] {
			switch color[d] {
			case grey:
				return fmt.Errorf("resource %s must run after %s, which runs after it (Block order and restart_on form a cycle)", s.r.ID, d.r.ID)
			case white:
				if err := visit(d); err != nil {
					return err
				}
			}
		}
		color[s] = black
		return nil
	}
	for _, s := range steps {
		if color[s] == white {
			if err := visit(s); err != nil {
				return nil, nil, err
			}
		}
	}
	return steps, blocks, nil
}

// restartOn returns the restart_on list of a service ([]any from JSON or
// []string from Go).
func restartOn(r wire.Resource) []string {
	switch v := r.Attrs["restart_on"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
