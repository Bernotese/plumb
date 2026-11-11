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

// CheckGuests runs one round of the guest poll. It compares only guests
// whose config digest changed since the last round or whose waiting list
// is not empty (a cold start need not move the digest, review S8). It
// writes the waiting list first (D-195), then reports drift on
// guest:config to OnDrift: "differs" with the changes, or "unchanged"
// when the guest matches again. Targets with a running Run are skipped;
// the Run stores the new digest.
func (e *Engine) CheckGuests(ctx context.Context) error {
	binds, err := e.st.Bindings(ctx, state.KindTarget)
	if err != nil {
		return err
	}
	var errs []error
	now := e.cfg.Now()
	seen := map[string]bool{}
	for _, b := range binds {
		seen[b.Name] = true
		e.mu.Lock()
		busy := e.busy[b.Name] > 0
		e.mu.Unlock()
		if err := e.checkAddress(ctx, b.Name, busy, now); err != nil {
			errs = append(errs, err)
		}
		if busy {
			continue
		}
		observed, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if observed.Digest == b.Digest && len(b.Waiting) == 0 {
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
		// The waiting list before the drift report: an Issue that the
		// report resolves says when a value still waits (D-195).
		if e.putWaiting(ctx, &b, driver.WaitingGuest(desired, observed)) {
			if err := e.st.PutBinding(ctx, b); err != nil {
				errs = append(errs, err)
				continue
			}
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
	e.mu.Lock()
	for t := range e.addrs {
		if !seen[t] {
			delete(e.addrs, t) // the target lost its guest
		}
	}
	e.mu.Unlock()
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
	// ImageVersion is the built version the guest was cloned from, 0 for
	// a stock Image or unknown (D-200).
	ImageVersion int
	Busy         bool // a Run works on the target
	// Waiting are the values that wait for a stop and start (D-195).
	Waiting []store.Waiting
	// Reported is the address list of the agent, only while it is online
	// and the list is known (D-214). Display data: never a connect target
	// and never used to authorize (D-216). IPs stays the configured address.
	Reported     []string
	ReportedAt   time.Time
	ReportedMore bool // the guest has more than wire.MaxAddrs addresses
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
		r, known, err := e.reported(ctx, target, l.Agent == "online")
		if err != nil {
			return l, err
		}
		if known {
			l.Reported, l.ReportedAt, l.ReportedMore = r.List, r.At, r.Truncated
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
	l.GuestID, l.Waiting = b.Ref, b.Waiting
	if st, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref)); err == nil {
		l.Power, l.IPs, l.ImageVersion = string(st.Power), st.IPs, st.Spec.ImageVersion
	}
	return l, nil
}
