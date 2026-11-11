package plan

import (
	"context"
	"errors"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
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
		desired, err := e.guestSpec(b.Name, r)
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

// Live is the live status of a target: what the API shows next to the
// desired state. It is not part of the state.
type Live struct {
	Agent    string // none | online | offline
	LastSeen time.Time
	Power    string // running | stopped | unknown
	IPs      []string
	GuestID  string // "" when there is no guest
	Busy     bool   // a Run works on the target
}

// Live reads the live status of a target from the agent table, the link
// and the driver.
func (e *Engine) Live(ctx context.Context, target string) (Live, error) {
	l := Live{Agent: "none", Power: "unknown"}
	e.mu.Lock()
	l.Busy = e.busy[target] > 0
	e.mu.Unlock()
	if a, err := e.st.AgentByTarget(ctx, target); err == nil {
		l.Agent, l.LastSeen = "offline", a.LastSeenAt
		if e.cfg.Agents.Online(target) {
			l.Agent = "online"
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return l, err
	}
	b, err := e.st.GetBinding(ctx, state.KindTarget, target)
	if errors.Is(err, store.ErrNotFound) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	l.GuestID = b.Ref
	if st, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref)); err == nil {
		l.Power, l.IPs = string(st.Power), st.IPs
	}
	return l, nil
}
