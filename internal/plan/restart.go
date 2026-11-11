package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// AttrAllowRestart is the Plan-only change of spec.allow_restart on
// guest:config (D-194). The driver never gets it.
const AttrAllowRestart = "allow_restart"

// Texts of T-056 (D-194, D-196).
const (
	// TextReenrollRefused: a re-enrollment of a running VM with
	// allow_restart false, at the proposal (D-194).
	TextReenrollRefused = "Re-enrollment restarts the VM, and this Target does not allow a restart by a Run. " +
		"Stop the VM in Proxmox and re-enroll again (the Run starts it). " +
		"Or set allow_restart first (its own Plan), then re-enroll."
	textReenrollRuns = "the guest runs now, and this Target does not allow a restart by a Run. " +
		"Stop the VM or set allow_restart, then plan again"
	textStartedOutside = "the VM was started outside Plumb during the Run, so the new token was written while it ran: " +
		"it stays pending in smbios1 and is not active. Plan the re-enrollment again"
	textRestartNotPlanned = "the guest runs now, and the change needs a restart that the Plan does not show; plan again"
	textBridgeStuck       = "Proxmox did not apply the bridge change live; check the VM in Proxmox"
	textWaitsOutput       = "%s: set in Proxmox, waits for a stop and start of the VM"
)

// userText is an error whose text is a sentence for the user, as the
// card words it (it may start with a capital and end with a period).
type userText string

func (t userText) Error() string { return string(t) }

// restartRule is the one rule of Plan and Run for a guest that may need
// a cold start (D-194, table C). changes are the changes of DiffGuest,
// waiting the values of WaitingGuest. It returns the changes to show or
// record and whether the guest restarts. The enroll_token change is not
// part of it; the caller adds it.
func restartRule(changes, waiting []wire.Change, power driver.Power, allow, reenroll bool) ([]wire.Change, bool, error) {
	out := append([]wire.Change(nil), changes...)
	if power != driver.PowerRunning {
		return out, false, nil // a stopped guest takes every value at its next start
	}
	if reenroll {
		if !allow {
			return nil, false, userText(TextReenrollRefused)
		}
		return out, true, nil // the one restart of the re-enrollment makes all of them active
	}
	needs := len(waiting) > 0
	for i, c := range out {
		if c.Pending || !driver.IsColdStart(c.Attr) {
			continue // removing a pending value changes nothing that runs (D-191)
		}
		if allow {
			needs = true
		} else {
			out[i].WaitsForRestart = true
		}
	}
	if !allow || !needs {
		return out, false, nil
	}
	out = append(out, waiting...)
	out = append(out, wire.Change{Attr: wire.AttrRestart, Risk: wire.RiskDisruptive})
	return out, true, nil
}

// allowRestartChange is the Plan-only change of allow_restart between the
// render of the base and of the proposal, or nil (risk read, D-194).
func allowRestartChange(before, after *state.Rendered) *wire.Change {
	if before == nil || after == nil || before.AllowRestart == after.AllowRestart {
		return nil
	}
	return &wire.Change{Attr: AttrAllowRestart, From: before.AllowRestart, To: after.AllowRestart, Risk: wire.RiskRead}
}

// driverChanges are the changes that Update gets: none of restart,
// allow_restart or a read change (a value that already waits).
func driverChanges(changes []wire.Change) []wire.Change {
	var out []wire.Change
	for _, c := range changes {
		if c.Attr == wire.AttrRestart || c.Attr == AttrAllowRestart || c.Risk == wire.RiskRead {
			continue
		}
		out = append(out, c)
	}
	return out
}

func hasAttr(changes []wire.Change, attr string) bool {
	for _, c := range changes {
		if c.Attr == attr {
			return true
		}
	}
	return false
}

// coldValue returns the running and the next value of cores or memory.
func coldValue(st driver.GuestState, attr string) (run, next int) {
	n := st.Next()
	if attr == driver.AttrCores {
		return st.Spec.Cores, n.Cores
	}
	return st.Spec.MemoryMB, n.MemoryMB
}

// checkWritten looks at the guest after Update (D-194): each written
// value of cores or memory is active (no flag) or set and waiting (flag);
// anything else fails. A bridge that still waits fails.
func checkWritten(recorded []wire.Change, desired driver.GuestSpec, after driver.GuestState) ([]wire.Change, error) {
	out := append([]wire.Change(nil), recorded...)
	for i, c := range out {
		switch {
		case c.Attr == driver.AttrBridge:
			if after.Spec.Bridge != desired.Bridge {
				return out, userText(textBridgeStuck)
			}
		case driver.IsColdStart(c.Attr):
			want, _ := driver.IntValue(c.To)
			run, next := coldValue(after, c.Attr)
			switch {
			case run == want:
				out[i].WaitsForRestart = false
			case next == want:
				out[i].WaitsForRestart = true
			default:
				return out, userText(fmt.Sprintf("Proxmox did not take the value of %s: the VM has %d, after the next stop and start %d, desired %d", c.Attr, run, next, want))
			}
		}
	}
	return out, nil
}

// waitingOutput is the step output for the changes that wait.
func waitingOutput(changes []wire.Change) string {
	out := ""
	for _, c := range changes {
		if c.WaitsForRestart {
			if out != "" {
				out += "\n"
			}
			out += fmt.Sprintf(textWaitsOutput, c.Attr)
		}
	}
	return out
}

// restartFailed builds the text of a restart that failed half way: it
// names the power state that the guest has now (D-194).
func (e *Engine) restartFailed(ctx context.Context, id driver.GuestID, cause error) error {
	st, err := e.cfg.Driver.Observe(ctx, id)
	switch {
	case err != nil:
		return fmt.Errorf("the restart failed, and the guest cannot be read (%v). Check the VM in Proxmox, then plan again: %w", err, cause)
	case st.Power == driver.PowerStopped:
		return fmt.Errorf("the restart failed: the guest is stopped. Start it in Proxmox, then plan again: %w", cause)
	default:
		return fmt.Errorf("the restart failed: the guest still runs with the old value. Check the VM in Proxmox, then plan again: %w", cause)
	}
}

// --- the state "restart needed" (D-195) ---

// putWaiting writes the waiting list of a binding: entries keep their
// since while attr and to stay. It emits one target.status event when the
// list changed. The caller stores the binding; putWaiting changes b only.
func (e *Engine) putWaiting(ctx context.Context, b *store.Binding, waiting []wire.Change) bool {
	now := time.Now().UTC()
	var next []store.Waiting
	for _, c := range waiting {
		from, _ := driver.IntValue(c.From)
		to, _ := driver.IntValue(c.To)
		w := store.Waiting{Attr: c.Attr, From: from, To: to, Since: now}
		for _, old := range b.Waiting {
			if old.Attr == w.Attr && old.To == w.To {
				w.Since = old.Since
			}
		}
		next = append(next, w)
	}
	changed := len(next) != len(b.Waiting)
	for i := range next {
		if changed {
			break
		}
		o := b.Waiting[i]
		changed = o.Attr != next[i].Attr || o.From != next[i].From || o.To != next[i].To || !o.Since.Equal(next[i].Since)
	}
	b.Waiting = next
	if changed {
		e.emit(ctx, "target.status", "target:"+b.Name, "system", map[string]any{"waiting": len(next)})
	}
	return changed
}

// recordWaiting is write point (b) of D-195: at the end of every Run,
// succeeded or failed, each of its Targets with a guest gets the waiting
// list of the desired state of the Run's revision. The digest stays as
// the guest step stored it.
func (e *Engine) recordWaiting(ctx context.Context, set *state.Set, targets []TargetPlan) {
	for _, tp := range targets {
		r, err := renderOrNil(set, tp.Target)
		if err != nil || r == nil {
			continue
		}
		b, err := e.st.GetBinding(ctx, state.KindTarget, tp.Target)
		if err != nil {
			continue
		}
		desired, err := e.guestSpec(tp.Target, r)
		if err != nil {
			continue
		}
		observed, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
		if err != nil {
			e.log.Error("observe for the waiting list", "target", tp.Target, "err", err)
			continue
		}
		if e.putWaiting(ctx, &b, driver.WaitingGuest(desired, observed)) {
			if err := e.st.PutBinding(ctx, b); err != nil {
				e.log.Error("waiting list", "target", tp.Target, "err", err)
			}
		}
	}
}

// waitsOfRun counts the changes of a Run's steps that wait for a restart.
func (e *Engine) waitsOfRun(ctx context.Context, runID int64) int {
	steps, err := e.st.Steps(ctx, runID)
	if err != nil {
		return 0
	}
	n := 0
	for _, s := range steps {
		var ch []wire.Change
		if json.Unmarshal(s.Changes, &ch) != nil {
			continue
		}
		for _, c := range ch {
			if c.WaitsForRestart {
				n++
			}
		}
	}
	return n
}
