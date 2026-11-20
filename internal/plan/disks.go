package plan

// Data disks of a Target (slice 8, T-238; D-423, D-436, D-437,
// ARCHITECTURE §21). The document lists them in guest.disks by slot; the
// driver diffs them against the observed slots. This file holds the plan
// rules: the 422s at the disk's pointer, allow_restart, the notes that say
// what data a Plan loses, and the Issue guest:volume-left.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// GuestVolumeLeftID is the Issue of a Target whose data disk volume could
// not be freed (D-437 (4)). Only the controller writes it.
const GuestVolumeLeftID = "guest:volume-left"

// ErrDisksNotAdoptable is wrapped when an adopted Target has guest.disks
// (422 disks-not-adoptable, as D-404).
var ErrDisksNotAdoptable = errors.New("disks-not-adoptable")

// docDisks reads guest.disks of a rendered Target. set is false when the
// field is missing: Plumb then manages no data disk (D-436).
func docDisks(target string, r *state.Rendered) (disks []driver.Disk, set bool, err error) {
	raw, ok := r.Guest.Attrs["disks"]
	if !ok || raw == nil {
		return nil, false, nil
	}
	if r.Adopted != nil {
		return nil, false, &ProposalError{Target: target, Err: ErrDisksNotAdoptable,
			Msg: "an adopted Target has no data disks of Plumb; remove guest.disks"}
	}
	list, _ := raw.([]any)
	if len(list) > driver.MaxDisks {
		return nil, false, &ProposalError{Target: target, Msg: fmt.Sprintf("guest.disks has %d disks; at most %d", len(list), driver.MaxDisks)}
	}
	for i, item := range list {
		m, _ := item.(map[string]any)
		slot, err1 := driver.IntValue(m["slot"])
		gb, err2 := driver.IntValue(m["size_gb"])
		if err1 != nil || err2 != nil || slot < 1 || slot > driver.MaxDisks || gb < 1 {
			return nil, false, &ProposalError{Target: target, Msg: fmt.Sprintf("guest.disks[%d] is not a disk", i)}
		}
		path, _ := m["path"].(string)
		disks = append(disks, driver.Disk{Slot: slot, SizeGB: gb, Path: path})
	}
	return disks, true, nil
}

// diskPointer is the JSON pointer of the disk in slot n in the document,
// or of guest.disks when the list does not name it.
func diskPointer(r *state.Rendered, n int) string {
	list, _ := r.Guest.Attrs["disks"].([]any)
	for i, item := range list {
		m, _ := item.(map[string]any)
		if s, err := driver.IntValue(m["slot"]); err == nil && s == n {
			return fmt.Sprintf("/spec/guest/disks/%d", i)
		}
	}
	return "/spec/guest/disks"
}

// diskError is a 422 at the pointer of the disk in slot n.
func diskError(target string, r *state.Rendered, n int, path, msg string) error {
	p := diskPointer(r, n)
	if path != "" {
		p += "/" + path
	}
	return state.Errors{&state.Error{Kind: state.KindTarget, Name: target, Path: p, Msg: msg}}
}

// diskDiffError turns a data disk error of Diff into a 422 at the disk's
// pointer; nil when err is no such error.
func diskDiffError(target string, r *state.Rendered, err error) error {
	var se *driver.ShrinkError
	var fe *driver.DiskForeignError
	var pe *driver.DiskPathError
	switch {
	case errors.As(err, &se) && se.Slot > 0:
		return diskError(target, r, se.Slot, "size_gb", fmt.Sprintf("disk %d has %d GiB and the proposal %d GiB; a disk cannot shrink", se.Slot, se.Have, se.Want))
	case errors.As(err, &fe):
		return diskError(target, r, fe.Slot, "", fe.Error())
	case errors.As(err, &pe):
		return diskError(target, r, pe.Slot, "path", pe.Error())
	}
	return nil
}

// diskPlan applies the plan rules to the data disk changes of an existing
// guest: allow_restart, the LXC risk of D-437, the open guest:volume-left,
// and a note per change that says what happens to the data.
func (e *Engine) diskPlan(ctx context.Context, target string, r *state.Rendered, observed driver.GuestState, changes []wire.Change) ([]wire.Change, []string, error) {
	running := observed.Power == driver.PowerRunning
	lxc := observed.Spec.Kind == wire.KindLXC
	var notes []string
	var left *store.Issue
	for i, c := range changes {
		n := driver.DiskIndex(c.Attr)
		if n == 0 {
			continue
		}
		have := observed.Spec.DiskAt(n)
		want, err := driver.ParseDiskValue(c.To)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case want != nil && have == nil: // an add
			if left == nil {
				if left, err = e.openVolumeLeft(ctx, target); err != nil {
					return nil, nil, err
				}
			}
			if left.ID != 0 {
				return nil, nil, diskError(target, r, n, "", fmt.Sprintf("a volume of an earlier add waits for its removal: %s (Issue %d)", volumeOf(*left), left.ID))
			}
			if lxc && running {
				if !r.AllowRestart {
					return nil, nil, diskError(target, r, n, "", "adding a container disk stops and starts the container: set allow_restart")
				}
				changes[i].Risk = wire.MaxRisk([]wire.Change{c, {Risk: wire.RiskDisruptive}})
				notes = append(notes, fmt.Sprintf("Disk %d: the container is stopped, the disk is added, and the container is started again. If it does not start with the disk, Plumb removes the disk again.", n))
			} else if lxc {
				notes = append(notes, fmt.Sprintf("Disk %d: the container is stopped, so the disk is written now and proven only at its next start; if that start fails, remove the disk with a Plan.", n))
			}
		case want == nil && have != nil: // a remove or a detach
			if running && !r.AllowRestart {
				return nil, nil, diskError(target, r, n, "", fmt.Sprintf("removing disk %d of a running guest can stop and start it: set allow_restart", n))
			}
			if have.Foreign {
				notes = append(notes, fmt.Sprintf("Disk %d (%s) is detached; Plumb does not free it.", n, have.Raw))
			} else {
				notes = append(notes, fmt.Sprintf("The data on disk %d (%s, %d GiB) is lost.", n, have.Volume, have.SizeGB))
			}
		}
	}
	return changes, notes, nil
}

// openVolumeLeft returns the open guest:volume-left Issue of target, or
// an Issue with ID 0.
func (e *Engine) openVolumeLeft(ctx context.Context, target string) (*store.Issue, error) {
	iss, err := e.st.OpenIssue(ctx, target, GuestVolumeLeftID)
	if errors.Is(err, store.ErrNotFound) {
		return &store.Issue{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &iss, nil
}

// volumeOf is the volume that a guest:volume-left Issue names.
func volumeOf(iss store.Issue) string {
	var ch []wire.Change
	_ = json.Unmarshal(iss.Changes, &ch)
	for _, c := range ch {
		if c.Attr == "volume" {
			if s, ok := c.From.(string); ok {
				return s
			}
		}
	}
	return "an unattached volume of this guest"
}

// volumeLeftIssue opens guest:volume-left for a volume that a failed free
// left (D-437 (4)): no revert, no accept; ignore once for a human admin.
func (e *Engine) volumeLeftIssue(ctx context.Context, target string, ve *driver.VolumeLeftError) error {
	way := fmt.Sprintf("as root: `qm set <vmid> --delete unusedN` (`pct set` for a container; the unused line that names %s), or `pvesm free %s` when no unused line names it; or a Plumb Target delete frees it", ve.Volume, ve.Volume)
	if ve.InUse() { // T-253 F3: Proxmox keeps it because it is still in use
		way = driver.SnapshotHint + " A Plumb Target delete frees it too."
	}
	return e.restoreIssue(ctx, target, GuestVolumeLeftID, wire.StatusDiffers, []wire.Change{
		{Attr: "volume", From: ve.Volume, To: nil, Risk: wire.RiskDestructive},
		// F2 of #375: the poll ends the Issue when the guest config no
		// longer names the volume, so the way out removes its unused line.
		{Attr: "way_out", To: way, Risk: wire.RiskRead},
	})
}

// checkVolumeLeft resolves an open guest:volume-left when its volume is
// gone from the guest (the guest poll).
func (e *Engine) checkVolumeLeft(ctx context.Context, target string, observed driver.GuestState) error {
	if e.cfg.OnDrift == nil {
		return nil
	}
	iss, err := e.openVolumeLeft(ctx, target)
	if err != nil || iss.ID == 0 {
		return err
	}
	vol := volumeOf(*iss)
	for _, s := range []driver.GuestSpec{observed.Spec, observed.Next()} {
		for _, u := range s.Unused {
			if u == vol {
				return nil
			}
		}
		for _, d := range s.Disks {
			if d.Volume == vol || strings.HasPrefix(d.Raw, vol+",") {
				return nil
			}
		}
	}
	return e.cfg.OnDrift(ctx, target, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: GuestVolumeLeftID,
		Status: wire.StatusUnchanged, Output: "the volume " + vol + " is gone from the guest"}}})
}

// diskSteps writes the data disk changes of a guest step (after the other
// settings), inside the disk gate of D-219: an add or a grow reserves its
// bytes until the step ends (M1 of #360). A failed free opens
// guest:volume-left (D-437 (4)); a failed container start with a new
// disk was undone by the driver and fails the step with its text.
func (e *Engine) diskSteps(ctx context.Context, target string, id driver.GuestID, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	if need := dataDiskGrowth(changes); need > 0 {
		release, err := e.disk.Reserve(ctx, DiskBytes(need), fmt.Sprintf("the %d GiB of data disks of %s", need, target))
		if err != nil {
			return fmt.Errorf("%w; no data disk was added or grown: free space on the storage or use less", err)
		}
		defer release()
	}
	err := e.cfg.Driver.Update(ctx, id, changes, nil)
	var ve *driver.VolumeLeftError
	if errors.As(err, &ve) {
		if ierr := e.volumeLeftIssue(ctx, target, ve); ierr != nil {
			e.log.Error("open guest:volume-left", "target", target, "err", ierr)
		}
		return userText(ve.Error())
	}
	var se *driver.DiskStartError
	if errors.As(err, &se) {
		return userText(se.Error())
	}
	if err != nil {
		return err
	}
	after, err := e.cfg.Driver.Observe(ctx, id)
	if err != nil {
		return err
	}
	for _, c := range changes {
		n := driver.DiskIndex(c.Attr)
		want, _ := driver.ParseDiskValue(c.To)
		have := after.Next().DiskAt(n)
		switch {
		case want == nil && have != nil:
			return userText(fmt.Sprintf("disk %d is still attached after its remove", n))
		case want != nil && (have == nil || have.Foreign || have.SizeGB != want.SizeGB || have.Path != want.Path):
			return userText(fmt.Sprintf("Proxmox did not take disk %d: it has %v, desired %v", n, have.Value(), want.Value()))
		}
	}
	return nil
}

// dataDiskGrowth is the GiB that the data disk changes add to the storage
// (adds and grows); the gate counts them (D-219, M1 of #360).
func dataDiskGrowth(changes []wire.Change) int {
	gb := 0
	for _, c := range changes {
		if driver.DiskIndex(c.Attr) == 0 {
			continue
		}
		from, err1 := driver.ParseDiskValue(c.From)
		to, err2 := driver.ParseDiskValue(c.To)
		switch {
		case err1 != nil || err2 != nil || to == nil:
		case from == nil:
			gb += to.SizeGB
		case to.SizeGB > from.SizeGB:
			gb += to.SizeGB - from.SizeGB
		}
	}
	return gb
}

// deleteDiskNotes names each observed data disk of a guest that a Target
// delete removes (M1 of #360): the guest config, not the document, so a
// missing disks field hides nothing. A guest that cannot be read gets no
// note (the delete step reports it).
func (e *Engine) deleteDiskNotes(ctx context.Context, ref string) []string {
	st, err := e.cfg.Driver.Observe(ctx, driver.GuestID(ref))
	if err != nil {
		return nil
	}
	var notes []string
	for _, d := range st.Spec.Disks {
		switch {
		case d.Foreign && strings.HasPrefix(d.Raw, "/"): // a bind mount or a device (H1 of #373)
			notes = append(notes, fmt.Sprintf("Mount point %d (%s, a host path or device) goes with the guest; what it holds on the host stays.", d.Slot, d.Raw))
		case d.Foreign:
			notes = append(notes, fmt.Sprintf("Disk %d (%s, not made by Plumb) is lost with the guest.", d.Slot, d.Raw))
		default:
			notes = append(notes, fmt.Sprintf("Disk %d (%s, %d GiB) is lost with the guest.", d.Slot, d.Volume, d.SizeGB))
		}
	}
	for _, u := range st.Spec.Unused {
		notes = append(notes, fmt.Sprintf("The unused volume %s of the guest is lost with it.", u))
	}
	return notes
}
