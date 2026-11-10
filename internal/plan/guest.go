package plan

import (
	"context"
	"errors"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// PollGuests checks the Proxmox level of every bound target every
// interval (ARCHITECTURE §6: 60 s) until ctx ends.
func (e *Engine) PollGuests(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := e.CheckGuests(ctx); err != nil && ctx.Err() == nil {
			e.log.Error("guest poll", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// CheckGuests runs one round of the guest poll. It calls Driver.Diff only
// for guests whose config digest changed since the last round, and
// reports drift on guest:config to OnDrift: "differs" with the changes,
// or "unchanged" when the guest matches again. Targets with a running
// Run are skipped; the Run stores the new digest.
func (e *Engine) CheckGuests(ctx context.Context) error {
	binds, err := e.st.Bindings(ctx, state.KindTarget)
	if err != nil {
		return err
	}
	var errs []error
	for _, b := range binds {
		e.mu.Lock()
		busy := e.busy[b.Name] > 0
		e.mu.Unlock()
		if busy {
			continue
		}
		observed, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if observed.Digest == b.Digest {
			continue
		}
		r, err := e.headRender(ctx, b.Name)
		if err != nil || r == nil {
			errs = append(errs, err)
			continue
		}
		desired, err := e.guestSpec(ctx, b.Name, r)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		item := wire.Item{Resource: wire.GuestConfigID, Status: wire.StatusUnchanged}
		if changes := driver.DriftGuest(desired, observed); len(changes) > 0 {
			item.Status, item.Changes = wire.StatusDiffers, changes
		}
		if e.cfg.OnDrift != nil {
			rep := wire.Report{Type: wire.ReportDrift, Items: []wire.Item{item}}
			if err := e.cfg.OnDrift(ctx, b.Name, rep); err != nil {
				errs = append(errs, err)
				continue // keep the old digest, so the next round reports again
			}
		}
		b.Digest = observed.Digest
		if err := e.st.PutBinding(ctx, b); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
