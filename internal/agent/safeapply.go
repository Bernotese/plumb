package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// Safe apply (D-293, D-300; the steps are in wire.SafeApplySeconds).

// safeApplyMarker is the marker file in the state dir. It exists from
// before the live change until the confirmed state is set again.
const safeApplyMarker = "safe-apply.json"

// safeMarker is the content of the marker (version 1).
type safeMarker struct {
	Version  int       `json:"version"`
	Resource string    `json:"resource"`
	Type     string    `json:"type"`
	Command  string    `json:"command"` // the apply; its result is sent after a restore at start
	Deadline time.Time `json:"deadline"`
}

// Timing of the confirm round trip. Tests shorten the window
// (Options.ConfirmWindow).
const (
	confirmTry   = 15 * time.Second // one attempt
	confirmPause = 2 * time.Second  // between attempts
)

// Output texts of a failed safe apply.
const (
	rolledBackText  = "no round trip to the controller within %d s after the change; the confirmed state is back"
	restoreFailText = "no round trip to the controller within %d s after the change, and the restore failed: %s (the rollback timer and the next boot load the confirmed state)"
	restartedText   = "the agent stopped during the safe apply of %s; it put the confirmed state back at its start"
)

// isSafeApply reports whether r gets safe apply.
func isSafeApply(r wire.Resource) bool { return slices.Contains(wire.SafeApplyTypes, r.Type) }

// safeApply applies the changes of one safe-apply resource (D-300):
// prepare (boot file, rollback timer), marker, live apply, confirm round
// trip, then commit or restore. It returns the item of the step.
func (a *Agent) safeApply(ctx context.Context, cmdID string, r wire.Resource, rs resource.Resource, changes []wire.Change) wire.Item {
	fail := func(text string, rolledBack bool) wire.Item {
		return wire.Item{Resource: r.ID, Status: wire.StatusFailed, Changes: changes,
			Output: resource.SafeOutput(r, text), RolledBack: rolledBack}
	}
	sa, ok := rs.(resource.SafeApplier)
	if !ok {
		return fail("the agent has no safe apply for this type", false)
	}
	a.safeMu.Lock()
	defer a.safeMu.Unlock()
	if err := sa.Prepare(ctx); err != nil {
		return fail(err.Error(), false)
	}
	window := a.opt.ConfirmWindow
	m := safeMarker{Version: 1, Resource: r.ID, Type: r.Type, Command: cmdID, Deadline: time.Now().Add(window)}
	if err := a.writeMarker(m); err != nil {
		a.restore(ctx, sa)
		return fail("store the safe apply marker: "+err.Error(), false)
	}
	if err := sa.Apply(ctx, changes); err != nil {
		// nft -f is atomic: nothing changed. Restore anyway, it also
		// stops the rollback timer.
		if a.restore(ctx, sa) {
			a.removeMarker()
		}
		return fail(err.Error(), false)
	}
	deadline := time.Now().Add(window)
	if a.confirmRoundTrip(ctx, cmdID, r.ID, deadline) {
		if err := sa.Commit(ctx); err != nil {
			if a.restore(ctx, sa) {
				a.removeMarker()
			}
			return fail("keep the new state: "+err.Error()+"; the confirmed state is back", true)
		}
		a.removeMarker()
		return wire.Item{Resource: r.ID, Status: wire.StatusApplied, Changes: changes}
	}
	secs := int(window / time.Second)
	// The apply context may be done (the agent stops): the restore must
	// still run.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if err := sa.Restore(rctx); err != nil {
		a.log.Error("safe apply: restore failed", "resource", r.ID, "err", err)
		return fail(fmt.Sprintf(restoreFailText, secs, err), true)
	}
	a.removeMarker()
	a.log.Warn("safe apply: no round trip; confirmed state restored", "resource", r.ID)
	return fail(fmt.Sprintf(rolledBackText, secs), true)
}

// restore runs Restore and logs a failure. It reports success.
func (a *Agent) restore(ctx context.Context, sa resource.SafeApplier) bool {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if err := sa.Restore(rctx); err != nil {
		a.log.Error("safe apply: restore failed", "err", err)
		return false
	}
	return true
}

// confirmRoundTrip sends the confirm report until the controller answers
// 2xx or the deadline passes. A 401 or another refusal (409: the
// controller does not know this apply) ends it at once.
func (a *Agent) confirmRoundTrip(ctx context.Context, cmdID, id string, deadline time.Time) bool {
	rep := wire.Report{Type: wire.ReportConfirm, CommandID: cmdID,
		Items: []wire.Item{{Resource: id, Status: wire.StatusRunning}}}
	for {
		left := time.Until(deadline)
		if left <= 0 || ctx.Err() != nil {
			return false
		}
		err := a.client.confirm(ctx, a.credential(), rep, min(confirmTry, left))
		if err == nil {
			return true
		}
		a.log.Warn("safe apply: confirm failed", "resource", id, "err", err)
		if errors.Is(err, errUnauthorized) || permanent(err) {
			return false
		}
		t := time.NewTimer(min(confirmPause, max(time.Until(deadline), 0)))
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case <-t.C:
		}
	}
}

func (a *Agent) writeMarker(m safeMarker) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return a.state.write(safeApplyMarker, b)
}

func (a *Agent) removeMarker() {
	if err := removeFile(a.state.path(safeApplyMarker)); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.log.Error("safe apply: remove marker", "err", err)
	}
}

// restoreAtStart puts the confirmed state back when the marker exists:
// the agent stopped or the guest rebooted inside the window (D-300). It
// returns the result report of the apply, so the Run step ends failed
// with rolled_back instead of waiting for its timeout (review F3 of
// T-111); nil when there was no marker. A marker that cannot be read is
// removed after a restore of every safe-apply type.
func (a *Agent) restoreAtStart(ctx context.Context) *wire.Report {
	b, err := os.ReadFile(a.state.path(safeApplyMarker))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var m safeMarker
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	if err == nil && (m.Version != 1 || !slices.Contains(wire.SafeApplyTypes, m.Type)) {
		err = fmt.Errorf("unknown marker version %d or type %q", m.Version, m.Type)
	}
	types := []string{m.Type}
	if err != nil {
		a.log.Error("safe apply: bad marker; restoring every type", "err", err)
		types = wire.SafeApplyTypes
		m = safeMarker{}
	}
	for _, typ := range types {
		if err := resource.Restore(ctx, a.env, typ); err != nil {
			// Keep the marker: the next start tries again. The rollback
			// timer and the boot file still hold the confirmed state.
			a.log.Error("safe apply: restore at start failed", "type", typ, "err", err)
			return nil
		}
	}
	a.removeMarker()
	a.log.Warn("safe apply: the agent stopped inside the window; confirmed state restored", "resource", m.Resource)
	if m.Command == "" || m.Resource == "" {
		return nil
	}
	text := fmt.Sprintf(restartedText, m.Resource)
	return &wire.Report{Type: wire.ReportResult, CommandID: m.Command, Error: text,
		Items: []wire.Item{{Resource: m.Resource, Status: wire.StatusFailed, RolledBack: true, Output: text}}}
}
