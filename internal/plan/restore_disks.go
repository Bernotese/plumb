package plan

// The restore Plan names the data disks that the backup lacks (F3 (a) of
// #359, T-244 D9, ARCHITECTURE §21 "Restore of a backup older than a
// disk"): it reads the backup's config (BackupConfigReader of T-241) and
// the guest's observed config, managed or not.

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// restoreDiskChanges returns a change and a note for each observed data
// disk (slots 1 … 3), and a note for each unused volume, that the backup
// does not hold. Without a reader, or when the backup's config cannot be
// read, one note says so: the Plan stays destructive either way.
// named are the slots that the document names: only there does the next
// Run attach a new empty disk (L2 of the #373 review).
func (e *Engine) restoreDiskChanges(ctx context.Context, b store.Binding, v RestoreVolume, st driver.GuestState, named map[int]bool) ([]wire.Change, []string) {
	if len(st.Spec.Disks) == 0 && len(st.Spec.Unused) == 0 {
		return nil, nil
	}
	reader, ok := e.cfg.GuestRestore.(BackupConfigReader)
	if !ok {
		return nil, []string{"Plumb cannot read which disks the backup holds: a data disk that the backup lacks is lost (an LXC) or detached (a VM)."}
	}
	cfg, err := reader.BackupConfig(ctx, b.Ref, v)
	if err != nil {
		return nil, []string{"Plumb could not read which disks the backup holds (" + err.Error() + "): a data disk that the backup lacks is lost (an LXC) or detached (a VM)."}
	}
	lxc := st.Spec.Kind == wire.KindLXC
	var changes []wire.Change
	var notes []string
	for _, d := range st.Spec.Disks {
		key := "scsi" + strconv.Itoa(d.Slot)
		if lxc {
			key = "mp" + strconv.Itoa(d.Slot)
		}
		if _, ok := cfg[key]; ok {
			continue
		}
		what := d.Volume
		if d.Foreign {
			what = d.Raw
		}
		if lxc {
			changes = append(changes, wire.Change{Attr: driver.AttrDiskN(d.Slot), From: (&d).Value(), To: "lost: not in the backup", Risk: wire.RiskDestructive})
			notes = append(notes, fmt.Sprintf("Disk %d (%s) is not in this backup: the data on disk %d is lost (the safety backup holds it).", d.Slot, what, d.Slot))
		} else {
			changes = append(changes, wire.Change{Attr: driver.AttrDiskN(d.Slot), From: (&d).Value(), To: "detached: not in the backup", Risk: wire.RiskDestructive})
			note := fmt.Sprintf("Disk %d (%s) is not in this backup: it is detached and kept as an unused volume; free it by hand when it is not needed.", d.Slot, what)
			if named[d.Slot] {
				note = fmt.Sprintf("Disk %d (%s) is not in this backup: it is detached and kept as an unused volume; the next Run attaches a new empty disk in slot %d; free the old volume by hand.", d.Slot, what, d.Slot)
			}
			notes = append(notes, note)
		}
	}
	for _, u := range st.Spec.Unused {
		notes = append(notes, fmt.Sprintf("The unused volume %s of the guest is not in the backup: Proxmox may remove it with the restore.", u))
	}
	return changes, notes
}
