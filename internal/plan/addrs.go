package plan

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// The address list that the agent reports, and the Issue on guest:address
// (D-212 … D-217). A reported address is display data: it is never used to
// authorize a call or as the target of a connection (D-216). Only Live and
// checkAddress read the stored list (source test in addrs_test.go).

const (
	// AddrsGap is the least time between two stored list changes of one
	// agent, from any report type (D-212).
	AddrsGap = 30 * time.Second
	// AddrStable is how long a mismatch or a match must hold before the
	// check opens or closes the Issue (D-215).
	AddrStable = 2 * time.Minute
)

// addrWatch is the state of the address check of one target.
type addrWatch struct {
	mismatch bool
	since    time.Time // the state holds since
	checked  time.Time // the last check that saw this state
	sent     string    // what OnDrift got last: "" nothing, "ok", or "differs <list>"
}

// storeAddrs stores the list of a Hello or an addrs report. The target is
// bound (checked by the caller, N4). A change over AddrsGap is dropped: an
// addrs report gets ErrTooOften (429); a Hello is taken without its list,
// and the stored list stays (it is not "no addrs field"). A Hello without
// a list sets the list to unknown (D-217).
func (e *Engine) storeAddrs(ctx context.Context, d link.Delivery) error {
	var list []string
	var truncated bool
	switch d.Report.Type {
	case wire.ReportHello:
		list, truncated = d.Report.Hello.Addrs, d.Report.Hello.AddrsTruncated
	case wire.ReportAddrs:
		list, truncated = d.Report.Addrs, d.Report.AddrsTruncated
		if list == nil {
			list = []string{} // a missing list in an addrs report is empty
		}
	default:
		return nil
	}
	changed, err := e.st.SetAgentAddrs(ctx, d.AgentID, list, truncated, e.cfg.Now(), AddrsGap)
	switch {
	case errors.Is(err, store.ErrAddrsRateLimited):
		if d.Report.Type == wire.ReportAddrs {
			return link.ErrTooOften
		}
		e.log.Info("address list of a hello dropped: changed too often", "target", d.Target)
		return nil
	case errors.Is(err, store.ErrNotFound):
		return nil // the agent was revoked or enrolled again meanwhile
	case err != nil:
		return err
	}
	if changed {
		// No address in the event log (D-214).
		data := map[string]any{"known": list != nil}
		if list != nil {
			data["count"], data["truncated"] = len(list), truncated
		}
		e.emit(ctx, "target.addrs", "target:"+d.Target, "agent:"+d.Target, data)
	}
	return nil
}

// reported returns the list for the status view: only when the agent is
// online and the list is known (D-214).
func (e *Engine) reported(ctx context.Context, target string, online bool) (store.ReportedAddrs, bool, error) {
	if !online {
		return store.ReportedAddrs{}, false, nil
	}
	return e.st.ReportedAddrs(ctx, target)
}

// configuredAddr returns the static address of a target in the head
// revision; ok is false for dhcp or no address.
func (e *Engine) configuredAddr(ctx context.Context, target string) (netip.Addr, bool, error) {
	r, err := e.headRender(ctx, target)
	if err != nil || r == nil {
		return netip.Addr{}, false, err
	}
	ip, _ := r.Guest.Attrs[driver.AttrIP].(string)
	p, err := netip.ParsePrefix(ip)
	if err != nil {
		return netip.Addr{}, false, nil // dhcp
	}
	return p.Addr().Unmap(), true, nil
}

// checkAddress is the address check of one bound target in the guest
// loop (D-215). It looks only when the target has a static ip, its agent
// is online, the list is known and not truncated, and no Run works on the
// target; else it forgets its state and neither opens nor closes. A
// mismatch or a match that held for AddrStable goes to OnDrift as a
// guest:address item ("differs" or "unchanged"), once per state and list.
// It compares only the target's own address with its own list.
func (e *Engine) checkAddress(ctx context.Context, target string, busy bool, now time.Time) error {
	forget := func() {
		e.mu.Lock()
		delete(e.addrs, target)
		e.mu.Unlock()
	}
	want, static, err := e.configuredAddr(ctx, target)
	if err != nil {
		return err
	}
	if busy || !static || !e.cfg.Agents.Online(target) {
		forget()
		return nil
	}
	rep, known, err := e.st.ReportedAddrs(ctx, target)
	if err != nil {
		return err
	}
	if !known || rep.Truncated {
		forget()
		return nil
	}
	mismatch := !slices.Contains(rep.List, want.String())

	e.mu.Lock()
	w := e.addrs[target]
	if w == nil {
		w = &addrWatch{mismatch: mismatch, since: now}
		e.addrs[target] = w
	} else if w.mismatch != mismatch {
		// The state flipped since the last check. When a new list did it,
		// it holds since that list arrived.
		w.mismatch, w.since = mismatch, now
		if rep.At.After(w.checked) && !rep.At.After(now) {
			w.since = rep.At
		}
	}
	w.checked = now
	stable := now.Sub(w.since) >= e.cfg.AddrStable
	sent := w.sent
	e.mu.Unlock()
	if !stable || e.cfg.OnDrift == nil {
		return nil
	}

	item := wire.Item{Resource: wire.GuestAddressID, Status: wire.StatusUnchanged}
	key := "ok"
	if mismatch {
		item.Status = wire.StatusDiffers
		item.Changes = []wire.Change{{Attr: driver.AttrIP, From: rep.List, To: want.String(), Risk: wire.RiskRead}}
		key = "differs " + strings.Join(rep.List, " ")
	}
	if key == sent {
		return nil
	}
	if err := e.cfg.OnDrift(ctx, target, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{item}}); err != nil {
		return err
	}
	if !mismatch {
		// An Issue that was opened again stays open for at least an hour
		// (D-215): the match goes to OnDrift again until it is closed.
		if _, err := e.st.OpenIssue(ctx, target, wire.GuestAddressID); err == nil {
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	e.mu.Lock()
	if e.addrs[target] == w {
		w.sent = key
	}
	e.mu.Unlock()
	return nil
}
