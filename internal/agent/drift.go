package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
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
//   - netlink path (D-367, netwatch.go): an ip or nft change triggers,
//     after the debounce, a check of every network or firewall resource;
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
	// resend flushes the drift that a busy controller (429, 503) refused,
	// after its Retry-After (T-202, D-366). It lives in this goroutine, so
	// a resend never runs beside a check; it ends with the loop.
	resend := time.NewTimer(time.Hour)
	resend.Stop()
	defer resend.Stop()
	defer a.net.close()
	paths := a.rewatch(ctx, w)
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
			paths = a.rewatch(ctx, w)
		case ids := <-a.applyDone:
			for _, id := range ids {
				pending[id] = true
			}
			paths = a.rewatch(ctx, w)
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
			paths = a.rewatch(ctx, w)
		case <-full.C:
			if !a.isApplying() {
				a.check(ctx, nil)
				paths = a.rewatch(ctx, w)
			}
		case k := <-a.net.fire:
			// A netlink trigger (D-367): the normal check of every
			// resource of the type, as after an inotify event.
			if a.isApplying() {
				continue
			}
			a.mu.Lock()
			ids := netIDs(a.desired, k)
			a.mu.Unlock()
			if len(ids) > 0 {
				a.check(ctx, ids)
				paths = a.rewatch(ctx, w)
			}
		case <-a.listChanged:
			paths = a.rewatch(ctx, w)
			if !a.isApplying() {
				a.check(ctx, nil)
			}
		case d := <-a.driftBusy:
			// A second 429 arms the timer again (one resend per wait).
			resend.Reset(resendWait(d))
		case <-resend.C:
			// Only the kept items; a new 429 sends on driftBusy again.
			a.flushAfterCheck(ctx)
		}
	}
}

// maxResendWait caps the wait before the kept drift is sent again.
const maxResendWait = 2 * time.Minute

// resendWait is Retry-After (at least 1 s, at most maxResendWait) plus up
// to 10 % jitter, so agents that a restart made busy together do not
// come back together.
func resendWait(retryAfter time.Duration) time.Duration {
	d := min(max(retryAfter, time.Second), maxResendWait)
	return d + rand.N(d/10+time.Millisecond)
}

// rewatch watches the parent directories of all Watch() paths and returns
// the map watch path → resource ids. A missing parent is replaced by its
// nearest existing ancestor, so the creation of the parent is seen too.
// It also subscribes netlink for the network and firewall resources of
// the list (D-367).
func (a *Agent) rewatch(ctx context.Context, w *watcher) map[string][]string {
	a.mu.Lock()
	list := append([]wire.Resource(nil), a.desired...)
	a.mu.Unlock()
	a.net.sync(ctx, netWant(list))
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
		if t, ok := resource.Lookup(r.Type); ok && t.NoDrift {
			continue // a command has no drift (D-260)
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
	handled, err := a.sendDrift(ctx, items)
	a.markSent(handled) // taken, or refused for good one by one
	var busy *busyError
	if errors.As(err, &busy) {
		// The drift loop sends the kept items again after the wait. A
		// signal that is still waiting stands for this one too.
		select {
		case a.driftBusy <- busy.RetryAfter:
		default:
		}
	}
	return err
}

// markSent clears the items that are sent (or refused for good), unless
// they changed meanwhile.
func (a *Agent) markSent(items []wire.Item) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, it := range items {
		if cur, ok := a.open[it.Resource]; ok && sameItem(cur, it) {
			delete(a.dirty, it.Resource)
		}
		if cur, ok := a.resolved[it.Resource]; ok && sameItem(cur, it) {
			delete(a.resolved, it.Resource)
		}
	}
}

// Limits of the link for one report (internal/link: maxItems, maxOutput;
// wire.MaxReportBytes for the body).
const (
	maxDriftItems  = 10000
	maxItemOutput  = 64 << 10
	maxDriftBytes  = wire.MaxReportBytes * 3 / 4 // room for the envelope
	truncateMarker = "\n[output truncated by the agent]"
)

// sendDrift sends drift items within the limits of the link (T-022): the
// Output of an item is cut to the limit, and the items go out in chunks.
// A chunk that the controller refuses for good is split, so only an item
// that is refused on its own is dropped (logged). It returns the items
// that are handled; on a temporary error, the ones before it.
func (a *Agent) sendDrift(ctx context.Context, items []wire.Item) ([]wire.Item, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) > 1 && (len(items) > maxDriftItems || driftSize(items) > maxDriftBytes) {
		return a.sendHalves(ctx, items)
	}
	err := a.send(ctx, wire.Report{Type: wire.ReportDrift, Items: limitOutput(items)})
	switch {
	case err == nil:
		return items, nil
	case !permanent(err):
		return nil, err
	case len(items) == 1:
		a.log.Warn("drift item refused for good; dropped", "resource", items[0].Resource, "err", err)
		return items, nil
	}
	return a.sendHalves(ctx, items)
}

func (a *Agent) sendHalves(ctx context.Context, items []wire.Item) ([]wire.Item, error) {
	mid := len(items) / 2
	left, err := a.sendDrift(ctx, items[:mid])
	if err != nil {
		return left, err
	}
	right, err := a.sendDrift(ctx, items[mid:])
	return append(left, right...), err
}

// limitOutput returns copies with Output cut to the link limit.
func limitOutput(items []wire.Item) []wire.Item {
	out := make([]wire.Item, len(items))
	for i, it := range items {
		if len(it.Output) > maxItemOutput {
			it.Output = strings.ToValidUTF8(it.Output[:maxItemOutput-len(truncateMarker)], "") + truncateMarker
		}
		out[i] = it
	}
	return out
}

func driftSize(items []wire.Item) int {
	b, _ := json.Marshal(limitOutput(items))
	return len(b)
}
