package plan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Restored mode (D-290, D-303). After "plumb restore" the controller
// observes, but changes nothing until a person confirms that the old
// controller is off.

// ErrRestoredMode: the action waits for POST /restore/confirm (409
// restored-mode).
var ErrRestoredMode = errors.New("plan: restored mode: confirm that the old controller is off first (POST /restore/confirm)")

// RestoreMode is the shared switch of restored mode: the engine, the link,
// the driver guard and the backup schedule read it. The zero value and a
// nil pointer are "never restored".
type RestoreMode struct {
	active   atomic.Bool // restored mode holds
	restored atomic.Bool // this controller came from a restore
}

// Active reports whether restored mode holds.
func (m *RestoreMode) Active() bool { return m != nil && m.active.Load() }

// Restored reports whether the controller came from a restore.
func (m *RestoreMode) Restored() bool { return m != nil && m.restored.Load() }

// Load reads the restore record.
func (m *RestoreMode) Load(ctx context.Context, st *store.Store) error {
	r, err := st.GetRestore(ctx)
	if err != nil {
		return err
	}
	m.restored.Store(r.Restored)
	m.active.Store(r.RestoredMode)
	return nil
}

// Issue resources of a restore (D-303).
const (
	// GuestMissingID: the bound guest of a Target is gone in Proxmox.
	GuestMissingID = "guest:missing"
	// AgentSilentID: no hello in SilentAfter after the confirm.
	AgentSilentID = "agent:silent"
)

// SilentAfter is the time after the confirm in which every agent of a
// bound, running guest must say hello (D-303).
const SilentAfter = 10 * time.Minute

// interruptedByRestore is the step output of a Run of the backup that
// was running (D-303).
const interruptedByRestore = "interrupted by a restore"

// restoreState is what the engine keeps of a restore.
type restoreState struct {
	mu       sync.Mutex
	revoked  map[string]bool // targets whose agent a guest:missing revoked
	unknown  []driver.PoolGuest
	unknownT time.Time
}

func (e *Engine) restoreActive() bool { return e.cfg.Restore.Active() }

// ConfirmRestore ends restored mode (POST /restore/confirm): the record,
// the switch, the event, and the current watch list to every online agent
// (until now they keep the one of the old controller).
func (e *Engine) ConfirmRestore(ctx context.Context, by, reason string) (store.RestoreState, error) {
	if e.restoreActive() {
		if err := e.checkBeforeConfirm(ctx); err != nil {
			return store.RestoreState{}, err
		}
	}
	r, err := e.st.ConfirmRestore(ctx, by, reason)
	if err != nil {
		return r, err
	}
	if e.cfg.Restore != nil {
		e.cfg.Restore.active.Store(false)
	}
	e.emit(ctx, "restore.confirmed", "restore:1", by, map[string]any{"reason": reason})
	binds, err := e.st.Bindings(ctx, state.KindTarget)
	if err != nil {
		return r, err
	}
	for _, b := range binds {
		if !e.cfg.Agents.Online(b.Name) {
			continue
		}
		if err := e.sendWatch(ctx, b.Name); err != nil {
			e.log.Warn("watch list after the restore confirm", "target", b.Name, "err", err)
		}
	}
	return r, nil
}

// ErrRestoreCheck: the guest check before the confirm could not observe
// a bound guest; restored mode stays (409 restore-check-failed).
var ErrRestoreCheck = errors.New("plan: the guest check before the confirm failed")

// checkBeforeConfirm observes the guest of every Target once before
// restored mode ends (review H1 of #224): a gone guest and a guest of
// another name open guest:missing now and a guest of another name is
// marked foreign (D-322), so the mark holds after the confirm. A guest that Proxmox
// does not answer for fails the confirm: a mismatch it hides would count
// as a rename afterwards.
func (e *Engine) checkBeforeConfirm(ctx context.Context) error {
	binds, err := e.st.Bindings(ctx, state.KindTarget)
	if err != nil {
		return err
	}
	for _, b := range binds {
		observed, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
		switch {
		case errors.Is(err, driver.ErrNotFound):
			err = e.guestMissing(ctx, b, true)
		case err != nil:
			return fmt.Errorf("%w: Proxmox did not answer for %s (%v); try again", ErrRestoreCheck, b.Name, err)
		case observed.Spec.Name != b.Name:
			if err = e.markForeign(ctx, b); err == nil {
				err = e.guestMissing(ctx, b, false)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// guestMissing reports the Issue guest:missing of a Target whose bound
// guest Proxmox no longer has (D-303). revoke: the guest is gone, so the
// agent credential of that Target is revoked once (a copy of the old disk
// cannot enroll as it). A guest of another name on the VMID revokes
// nothing (review M-a of #222): a person may have renamed our own guest.
func (e *Engine) guestMissing(ctx context.Context, b store.Binding, revoke bool) error {
	e.restore.mu.Lock()
	first := revoke && !e.restore.revoked[b.Name]
	if first {
		if e.restore.revoked == nil {
			e.restore.revoked = map[string]bool{}
		}
		e.restore.revoked[b.Name] = true
	}
	e.restore.mu.Unlock()
	if first {
		if err := e.cfg.Agents.Revoke(ctx, b.Name); err != nil {
			return fmt.Errorf("revoke the agent of %s (guest missing): %w", b.Name, err)
		}
	}
	return e.restoreIssue(ctx, b.Name, GuestMissingID, wire.StatusDiffers,
		[]wire.Change{{Attr: "guest_id", From: nil, To: b.Ref, Risk: wire.RiskDestructive}})
}

// TargetDeletedReason is the Output of the items that resolve the
// restore Issues of a deleted Target (T-134 F8).
const TargetDeletedReason = "the Target was deleted"

// targetDeleted resolves the restore Issues (guest:missing,
// agent:silent) of a Target whose binding a delete step dropped: no
// poll reports on it again, and their actions need the Target. A
// foreign guest keeps its own rules (D-322): its mark went with the
// binding and the guest stays in unknown_guests. An error is only
// logged: the delete is done.
func (e *Engine) targetDeleted(ctx context.Context, target string) {
	if !e.cfg.Restore.Restored() {
		return
	}
	e.restore.mu.Lock()
	delete(e.restore.revoked, target) // a new Target of that name revokes again
	e.restore.unknownT = time.Time{}  // list the guests again
	e.restore.mu.Unlock()
	if e.cfg.OnDrift == nil {
		return
	}
	var items []wire.Item
	for _, id := range []string{GuestMissingID, AgentSilentID} {
		items = append(items, wire.Item{Resource: id, Status: wire.StatusUnchanged, Output: TargetDeletedReason})
	}
	if err := e.cfg.OnDrift(ctx, target, wire.Report{Type: wire.ReportDrift, Items: items}); err != nil {
		e.log.Warn("resolve the restore Issues of a deleted Target", "target", target, "err", err)
	}
}

// restoreIssue sends a drift item of a restore Issue: differs opens it,
// unchanged resolves it as gone.
func (e *Engine) restoreIssue(ctx context.Context, target, id string, status wire.Status, changes []wire.Change) error {
	if e.cfg.OnDrift == nil {
		return nil
	}
	return e.cfg.OnDrift(ctx, target, wire.Report{Type: wire.ReportDrift,
		Items: []wire.Item{{Resource: id, Status: status, Changes: changes}}})
}

// checkSilent opens agent:silent for a bound, running guest whose agent
// this controller never heard since the restore, once SilentAfter passed
// after the confirm; a hello resolves it. An agent that said hello in
// restored mode and stays connected says no new hello, so a hello since
// the restore or an online agent counts (review M1 of #222).
func (e *Engine) checkSilent(ctx context.Context, target string, observed driver.GuestState, r store.RestoreState) error {
	if !r.Restored || r.RestoredMode || r.ConfirmedAt.IsZero() || e.cfg.Now().Sub(r.ConfirmedAt) < SilentAfter {
		return nil
	}
	a, err := e.st.AgentByTarget(ctx, target)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	heard := e.cfg.Agents.Online(target) || err == nil && a.Hello != nil && !a.Hello.At.Before(r.RestoredAt)
	if heard || observed.Power != driver.PowerRunning {
		return e.restoreIssue(ctx, target, AgentSilentID, wire.StatusUnchanged, nil)
	}
	return e.restoreIssue(ctx, target, AgentSilentID, wire.StatusDiffers,
		[]wire.Change{{Attr: "hello", From: nil, To: "any", Risk: wire.RiskRead}})
}

// markForeign marks the bound VMID of a Target as foreign (D-322): it
// holds a guest of another name after a restore. Only a matching name or
// the end of the binding clears it.
func (e *Engine) markForeign(ctx context.Context, b store.Binding) error {
	if err := e.st.MarkForeign(ctx, b.Name, b.Ref); err != nil {
		return err
	}
	e.restore.mu.Lock()
	e.restore.unknownT = time.Time{} // list the guests again
	e.restore.mu.Unlock()
	return nil
}

// clearForeign drops the foreign mark of a Target whose guest has its
// name again.
func (e *Engine) clearForeign(ctx context.Context, target string) error {
	cleared, err := e.st.ClearForeign(ctx, target)
	if cleared {
		e.restore.mu.Lock()
		e.restore.unknownT = time.Time{}
		e.restore.mu.Unlock()
	}
	return err
}

// ErrForeignGuest: after a restore the VMID of a Target belongs to
// another guest; the Run does not touch it (review H2 of #222).
var ErrForeignGuest = errors.New("plan: after the restore this VMID holds another guest; Plumb does not touch it (the Issue guest:missing tells)")

// checkOwnGuest is the guard of a Run on the guest of a Target after a
// restore: a VMID marked foreign (D-322) is refused while it holds a
// guest of another name. An unmarked name mismatch is a rename, written
// to as on a never restored controller.
// A guest that is gone is no error here.
func (e *Engine) checkOwnGuest(ctx context.Context, target string) error {
	if !e.cfg.Restore.Restored() {
		return nil
	}
	b, err := e.st.GetBinding(ctx, state.KindTarget, target)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	observed, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
	if errors.Is(err, driver.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if observed.Spec.Name == target {
		return nil
	}
	if foreign, err := e.st.IsForeign(ctx, target, b.Ref); err != nil || !foreign {
		return err // a rename (D-322)
	}
	return fmt.Errorf("%w (VMID %s, name %q, Target %s)", ErrForeignGuest, b.Ref, observed.Spec.Name, target)
}

// unknownTTL is how long the list of unknown guests is kept.
const unknownTTL = time.Minute

// UnknownGuests lists the guests of the pool that no Target, Image
// version or build is bound to (D-303). Plumb never touches them. The
// list is read at most once a minute.
func (e *Engine) UnknownGuests(ctx context.Context) ([]driver.PoolGuest, error) {
	e.restore.mu.Lock()
	if !e.restore.unknownT.IsZero() && e.cfg.Now().Sub(e.restore.unknownT) < unknownTTL {
		out := slices.Clone(e.restore.unknown)
		e.restore.mu.Unlock()
		return out, nil
	}
	e.restore.mu.Unlock()
	all, err := e.cfg.Driver.ListGuests(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	foreign, err := e.st.ForeignRefs(ctx)
	if err != nil {
		return nil, err
	}
	for _, kind := range []string{state.KindTarget, state.KindImage} {
		binds, err := e.st.Bindings(ctx, kind)
		if err != nil {
			return nil, err
		}
		for _, b := range binds {
			if !foreign[b.Ref] {
				known[b.Ref] = true
			}
		}
	}
	builds, err := e.cfg.Driver.ListBuilds(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range builds {
		known[string(b.ID)] = true
	}
	if head, err := e.st.Head(ctx); err == nil {
		set, err := e.load(ctx, head.ID)
		if err != nil {
			return nil, err
		}
		for _, img := range set.Images() {
			objs, err := e.cfg.Driver.ListImages(ctx, img)
			if err != nil {
				return nil, err
			}
			for _, o := range objs {
				if o.Ref != "" {
					known[string(o.Ref)] = true
				}
			}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	out := []driver.PoolGuest{}
	for _, g := range all {
		if !known[string(g.ID)] {
			out = append(out, g)
		}
	}
	e.restore.mu.Lock()
	e.restore.unknown, e.restore.unknownT = out, e.cfg.Now()
	e.restore.mu.Unlock()
	return slices.Clone(out), nil
}

// RestoredMode reports whether restored mode holds (for the API).
func (e *Engine) RestoredMode() bool { return e.restoreActive() }
