package plan

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Slice 4, Proxmox day 2 (D-291, D-299, D-308 … D-310): power, tags and
// the disk grow of guest:config.

// Texts of T-115.
const (
	textShrink = "the disk cannot shrink: the guest has %d GiB and the desired state %d GiB (Proxmox cannot shrink a disk). " +
		"Keep the size, or accept the bigger disk into the Target"
	textStopNeedsRestart = "power: stopped needs allow_restart on the Target (D-291): a Run may stop the guest only then"
	textGrowNotRunning   = "a disk grow needs a running guest (D-299): set power: running, or start the guest, then plan again"
	textGrowAgent        = "the disk is %d GiB in Proxmox now, but the agent did not grow the root file system: %s. " +
		"The bigger disk stays (it cannot shrink); fix the cause in the guest, then plan again"
	textStoppedSkip = "Target stopped"
	textNotStopped  = "not stopped: %s; the guest keeps running. Plan again"
)

// rulePower is the power state that the restart rule of D-194 sees: a
// guest that the Run stops at its end takes every waiting value at its
// next start, so it never restarts for one (D-308). A re-enrollment keeps
// its own rule.
func rulePower(desired driver.GuestSpec, observed driver.GuestState, reenroll bool) driver.Power {
	if !reenroll && desired.Power == driver.PowerStopped {
		return driver.PowerStopped
	}
	return observed.Power
}

// runsAfter says whether the guest runs after the guest step: the desired
// power, or the observed one when power is not managed.
func runsAfter(desired driver.GuestSpec, observed driver.GuestState) bool {
	switch desired.Power {
	case driver.PowerRunning:
		return true
	case driver.PowerStopped:
		return false
	}
	return observed.Power == driver.PowerRunning
}

// powerTo returns the target of a power change in changes, or "".
func powerTo(changes []wire.Change) driver.Power {
	for _, c := range changes {
		if c.Attr == driver.AttrPower {
			to, _ := c.To.(string)
			return driver.Power(to)
		}
	}
	return ""
}

// checkDay2 is the Plan check of the day-2 changes of an existing guest:
// a stop needs allow_restart, a grow a guest that runs after the guest
// step and, for a VM, an agent with the command grow (D-299).
func (e *Engine) checkDay2(ctx context.Context, target string, desired driver.GuestSpec, observed driver.GuestState, changes []wire.Change, allow bool) error {
	if powerTo(changes) == driver.PowerStopped && !allow {
		return &ProposalError{Target: target, Msg: textStopNeedsRestart}
	}
	if driver.Grow(changes) == nil {
		return nil
	}
	if !runsAfter(desired, observed) {
		return &ProposalError{Target: target, Msg: textGrowNotRunning}
	}
	if desired.Kind != wire.KindVM {
		return nil // Proxmox grows the file system of a container
	}
	if m := e.lacksGrow(ctx, target); m != "" {
		return &AgentOutdatedError{Targets: []string{target}, Missing: []string{m}, By: map[string][]string{target: {m}}}
	}
	return nil
}

// lacksGrow returns the gate entry "command:grow" when the agent of the
// target does not run the command grow, else "". An agent without a
// stored hello is the baseline (D-276).
func (e *Engine) lacksGrow(ctx context.Context, target string) string {
	caps, _, known, err := e.agentCaps(ctx, target)
	if err != nil || !known {
		caps = wire.BaselineCapabilities()
	}
	return caps.LacksCommand(wire.CommandGrow)
}

// checkNoShrink refuses a proposal that lowers the rendered disk of a
// bound Target below the base revision (422 at /spec/guest/disk, D-299).
func (e *Engine) checkNoShrink(ctx context.Context, base, next *state.Set, names []string) error {
	var errs state.Errors
	for _, t := range names {
		before, err := renderOrNil(base, t)
		if err != nil || before == nil {
			continue
		}
		after, err := renderOrNil(next, t)
		if err != nil || after == nil {
			continue
		}
		have, err1 := driver.IntValue(before.Guest.Attrs[driver.AttrDisk])
		want, err2 := driver.IntValue(after.Guest.Attrs[driver.AttrDisk])
		if err1 != nil || err2 != nil || want >= have {
			continue
		}
		if _, err := e.st.GetBinding(ctx, state.KindTarget, t); errors.Is(err, store.ErrNotFound) {
			continue // no guest yet: the disk is not made
		} else if err != nil {
			return err
		}
		errs = append(errs, &state.Error{Kind: state.KindTarget, Name: t, Path: "/spec/guest/disk",
			Msg: fmt.Sprintf("disk %d GiB is smaller than %d GiB of the current revision; a disk cannot shrink (D-299)", want, have)})
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// checkDay2Written checks the tags and the disk after the writes of the
// guest step.
func checkDay2Written(changes []wire.Change, desired driver.GuestSpec, after driver.GuestState) error {
	for _, c := range changes {
		switch c.Attr {
		case driver.AttrTags:
			if have := driver.NormTags(after.Spec.Tags); !slices.Equal(have, desired.Tags) {
				return userText(fmt.Sprintf("Proxmox did not take the tags: the guest has %v, desired %v", have, desired.Tags))
			}
		case driver.AttrDisk:
			if after.Spec.DiskGB < desired.DiskGB {
				return userText(fmt.Sprintf("Proxmox did not grow the disk: the guest has %d GiB, desired %d GiB", after.Spec.DiskGB, desired.DiskGB))
			}
		}
	}
	return nil
}

// growRoot asks the agent of a VM to grow its root partition and file
// system after the driver grew the disk to gib (D-299). The gate comes
// first: no command that the agent does not run.
func (e *Engine) growRoot(ctx context.Context, planID int64, target string, gib int) error {
	if m := e.lacksGrow(ctx, target); m != "" {
		return &AgentOutdatedError{Targets: []string{target}, Missing: []string{m}, By: map[string][]string{target: {m}}}
	}
	rep, err := e.command(ctx, target, wire.CommandGrow, wire.GrowBody{PlanID: planID, MinBytes: DiskBytes(gib)}, e.cfg.ApplyTimeout, nil)
	if err != nil {
		return fmt.Errorf(textGrowAgent, gib, err.Error())
	}
	if rep.Error != "" {
		return fmt.Errorf(textGrowAgent, gib, rep.Error)
	}
	for _, it := range rep.Items {
		if it.Resource != wire.GrowItemID {
			continue
		}
		switch it.Status {
		case wire.StatusApplied, wire.StatusUnchanged:
			return nil
		}
		out := it.Output
		if out == "" {
			out = string(it.Status)
		}
		return fmt.Errorf(textGrowAgent, gib, out)
	}
	return fmt.Errorf(textGrowAgent, gib, "the agent did not report "+wire.GrowItemID)
}

// RevertShrinks reports whether a revert of these guest:config drift
// changes would shrink a disk (a disk grown in Proxmox): the Issue then
// offers no revert (D-299).
func RevertShrinks(changes []wire.Change) bool {
	for _, c := range changes {
		from, err1 := driver.IntValue(c.From)
		to, err2 := driver.IntValue(c.To)
		if c.Attr == driver.AttrDisk && err1 == nil && err2 == nil && to < from {
			return true
		}
	}
	return false
}

// pollKey is what the guest poll compares (CheckGuests): the config
// digest and the power state, so that a start or a stop outside Plumb is
// seen as drift of power (D-299, D-309). A Binding stores it as Digest.
func pollKey(st driver.GuestState) string {
	return st.Digest + " " + string(st.Power)
}
