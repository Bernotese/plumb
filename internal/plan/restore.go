package plan

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	mu      sync.Mutex
	revoked map[string]bool // targets whose agent a guest:missing revoked
	// foreign are VMIDs of bindings whose guest has another name: the old
	// controller deleted the bound guest and Proxmox gave the VMID to a
	// new one (review H2 of #222). Unknown guests list them.
	foreign  map[string]bool
	unknown  []driver.PoolGuest
	unknownT time.Time
}

func (e *Engine) restoreActive() bool { return e.cfg.Restore.Active() }

// ConfirmRestore ends restored mode (POST /restore/confirm): the record,
// the switch, the event, and the current watch list to every online agent
// (until now they keep the one of the old controller).
func (e *Engine) ConfirmRestore(ctx context.Context, by, reason string) (store.RestoreState, error) {
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

// foreignHolds reports whether a name mismatch on the VMID of a Target
// still counts as a foreign guest (D-320): while restored mode holds, and
// after it while guest:missing of the Target is open or ignored (an
// ignore_once or a Rule keeps it, D-322). Otherwise a name mismatch is a
// rename, the same as on a controller that was never restored.
func (e *Engine) foreignHolds(ctx context.Context, target string) (bool, error) {
	if e.restoreActive() {
		return true, nil
	}
	iss, err := e.st.OpenIssue(ctx, target, GuestMissingID)
	if err == nil && iss.Status == store.IssueOpen {
		return true, nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	if _, err := e.st.IgnoredOnce(ctx, target, GuestMissingID); err == nil {
		return true, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	dropped, err := e.st.IgnoredDriftOf(ctx, target)
	if err != nil {
		return false, err
	}
	for _, d := range dropped {
		if d.Resource == GuestMissingID {
			return true, nil
		}
	}
	return false, nil
}

// setForeign records whether the VMID of a binding belongs to another
// guest now.
func (e *Engine) setForeign(ref string, foreign bool) {
	e.restore.mu.Lock()
	defer e.restore.mu.Unlock()
	if e.restore.foreign == nil {
		e.restore.foreign = map[string]bool{}
	}
	if foreign != e.restore.foreign[ref] {
		e.restore.unknownT = time.Time{} // list the guests again
	}
	if foreign {
		e.restore.foreign[ref] = true
	} else {
		delete(e.restore.foreign, ref)
	}
}

// ErrForeignGuest: after a restore the VMID of a Target belongs to
// another guest; the Run does not touch it (review H2 of #222).
var ErrForeignGuest = errors.New("plan: after the restore this VMID holds another guest; Plumb does not touch it (the Issue guest:missing tells)")

// checkOwnGuest is the guard of a Run on the guest of a Target after a
// restore: while a name mismatch counts as a foreign guest (foreignHolds),
// the bound VMID must hold a guest with the name of the Target. Later a
// renamed guest is written to as on a never restored controller (D-322).
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
	if holds, err := e.foreignHolds(ctx, target); err != nil || !holds {
		return err
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
	e.restore.mu.Lock()
	foreign := maps.Clone(e.restore.foreign)
	e.restore.mu.Unlock()
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
