package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// Drift (ARCHITECTURE §6, D-028). The agent keeps the desired list on disk
// and checks it also while the controller is away:
//   - fast path: inotify on the parent directory of every Watch() path,
//     then, after the debounce, a check of the affected resources;
//   - slow path: a full check every Config.FullCheck.
// A check reports only differences from what was reported before: a
// resource that drifts (status differs), whose drift changed, or whose
// drift went away (status unchanged). After a reconnect the agent sends
// all open drift again. Events during an apply are not checked; after the
// apply the agent checks its resources again, against the list that now
// holds them, so its own apply creates no drift report.

// setDesired replaces the desired list (watch command).
func (a *Agent) setDesired(list []wire.Resource) error {
	ids := map[string]bool{}
	for _, r := range list {
		if ids[r.ID] {
			return fmt.Errorf("resource %s is listed twice", r.ID)
		}
		ids[r.ID] = true
	}
	a.mu.Lock()
	a.desired = append([]wire.Resource(nil), list...)
	// Drift of resources that are no longer desired is forgotten.
	for id := range a.open {
		if !ids[id] {
			delete(a.open, id)
			delete(a.dirty, id)
		}
	}
	a.mu.Unlock()
	if err := a.saveDesired(); err != nil {
		return fmt.Errorf("store desired list: %w", err)
	}
	select {
	case a.listChanged <- struct{}{}:
	default:
	}
	return nil
}

func (a *Agent) saveDesired() error {
	a.mu.Lock()
	list := append([]wire.Resource(nil), a.desired...)
	a.mu.Unlock()
	return a.state.saveDesired(list)
}

// beginApply merges the applied resources into the desired list (by id)
// and pauses drift checks.
func (a *Agent) beginApply(list []wire.Resource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.applying++
	pos := map[string]int{}
	for i, r := range a.desired {
		pos[r.ID] = i
	}
	for _, r := range list {
		if i, ok := pos[r.ID]; ok {
			a.desired[i] = r
		} else {
			pos[r.ID] = len(a.desired)
			a.desired = append(a.desired, r)
		}
	}
}

// endApply resumes drift checks and asks for a check of the applied
// resources.
func (a *Agent) endApply(list []wire.Resource) {
	a.mu.Lock()
	a.applying--
	a.mu.Unlock()
	ids := make([]string, len(list))
	for i, r := range list {
		ids[i] = r.ID
	}
	select {
	case a.applyDone <- ids:
	default: // the drift loop is gone or busy; the full check covers it
	}
}

func (a *Agent) isApplying() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applying > 0
}

// maxDebounceWait bounds the debounce: a file that changes more often than
// the debounce is still checked at least this often.
func (a *Agent) maxDebounceWait() time.Duration { return 5 * a.opt.Debounce }

// driftLoop runs the fast and the slow path until ctx ends.
func (a *Agent) driftLoop(ctx context.Context, w *watcher) {
	full := time.NewTicker(time.Duration(a.cfg.FullCheck))
	defer full.Stop()
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	pending := map[string]bool{}
	var firstPending time.Time // when the oldest pending event came
	arm := func() {
		if firstPending.IsZero() {
			firstPending = time.Now()
		}
		left := a.maxDebounceWait() - time.Since(firstPending)
		debounce.Reset(max(min(a.opt.Debounce, left), 0))
	}
	paths := a.rewatch(w)
	events := w.events

	a.check(ctx, nil) // a full check at start
	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-events:
			if !ok {
				a.log.Error("inotify stopped; drift is found only by the full check")
				events = nil
				continue
			}
			for _, id := range affected(paths, p) {
				pending[id] = true
			}
			if len(pending) > 0 && !a.isApplying() {
				arm()
			}
		case <-w.overflow:
			// Events were lost: only a full check is safe.
			clear(pending)
			firstPending = time.Time{}
			a.check(ctx, nil)
			paths = a.rewatch(w)
		case ids := <-a.applyDone:
			for _, id := range ids {
				pending[id] = true
			}
			paths = a.rewatch(w)
			arm()
		case <-debounce.C:
			if a.isApplying() {
				continue // endApply sets the timer again
			}
			ids := make([]string, 0, len(pending))
			for id := range pending {
				ids = append(ids, id)
			}
			clear(pending)
			firstPending = time.Time{}
			a.check(ctx, ids)
			paths = a.rewatch(w)
		case <-full.C:
			if !a.isApplying() {
				a.check(ctx, nil)
				paths = a.rewatch(w)
			}
		case <-a.listChanged:
			paths = a.rewatch(w)
			if !a.isApplying() {
				a.check(ctx, nil)
			}
		}
	}
}

// rewatch watches the parent directories of all Watch() paths and returns
// the map watch path → resource ids. A missing parent is replaced by its
// nearest existing ancestor, so the creation of the parent is seen too.
func (a *Agent) rewatch(w *watcher) map[string][]string {
	a.mu.Lock()
	list := append([]wire.Resource(nil), a.desired...)
	a.mu.Unlock()
	paths := map[string][]string{}
	dirs := map[string]bool{}
	for _, r := range list {
		rs, err := resource.New(r, a.env)
		if err != nil {
			continue
		}
		for _, p := range rs.Watch() {
			p = filepath.Clean(p)
			paths[p] = append(paths[p], r.ID)
			dirs[filepath.Dir(p)] = true
		}
	}
	w.sync(dirs, a.log)
	return paths
}

// affected returns the ids of resources whose watch path is p or below p.
func affected(paths map[string][]string, p string) []string {
	var ids []string
	for wp, list := range paths {
		if wp == p || strings.HasPrefix(wp, p+"/") {
			ids = append(ids, list...)
		}
	}
	return ids
}

// check observes the given resources (all for nil) and reports what
// changed since the last report.
func (a *Agent) check(ctx context.Context, ids []string) {
	// No apply runs while the check reads the list and observes (D-055).
	// The list is copied under the lock: an apply that ended just before
	// has merged its resources into it.
	a.applyLock.RLock()
	defer a.flushAfterCheck(ctx)
	defer a.applyLock.RUnlock()
	a.mu.Lock()
	list := append([]wire.Resource(nil), a.desired...)
	a.mu.Unlock()
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, r := range list {
		if ids != nil && !want[r.ID] {
			continue
		}
		_, item := a.observeOne(ctx, r)
		a.mu.Lock()
		prev, wasOpen := a.open[r.ID]
		switch {
		case item.Status == wire.StatusUnchanged:
			if wasOpen {
				delete(a.open, r.ID)
				delete(a.dirty, r.ID)
				a.resolved[r.ID] = item
			}
		case !wasOpen || !sameItem(prev, item):
			a.open[r.ID] = item
			a.dirty[r.ID] = true
			delete(a.resolved, r.ID)
		}
		a.mu.Unlock()
	}
}

func (a *Agent) flushAfterCheck(ctx context.Context) {
	if err := a.flushDrift(ctx, false); err != nil {
		a.log.Debug("drift report not sent; it is sent again after reconnect", "err", err)
	}
}

func sameItem(x, y wire.Item) bool {
	bx, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return reflect.DeepEqual(bx, by)
}

// flushDrift sends the drift that was not sent yet, or with all set every
// open drift (after a reconnect). Without a credential it sends nothing.
func (a *Agent) flushDrift(ctx context.Context, all bool) error {
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	if a.credential() == "" {
		return nil
	}
	a.mu.Lock()
	var items []wire.Item
	for id, it := range a.open {
		if all || a.dirty[id] {
			items = append(items, it)
		}
	}
	for _, it := range a.resolved {
		items = append(items, it)
	}
	a.mu.Unlock()
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Resource < items[j].Resource })
	if err := a.send(ctx, wire.Report{Type: wire.ReportDrift, Items: items}); err != nil {
		if !permanent(err) {
			return err
		}
		// The controller refuses this report for good (for example more
		// than 10 000 items or an Output over 64 KiB). Sending it again
		// cannot help and would keep the agent from polling (T-022): the
		// items count as sent. They stay open and go out again when they
		// change.
		a.log.Warn("drift report refused for good; dropped", "items", len(items), "err", err)
	}
	a.mu.Lock()
	for _, it := range items {
		if cur, ok := a.open[it.Resource]; ok && sameItem(cur, it) {
			delete(a.dirty, it.Resource)
		}
		if cur, ok := a.resolved[it.Resource]; ok && sameItem(cur, it) {
			delete(a.resolved, it.Resource)
		}
	}
	a.mu.Unlock()
	return nil
}
