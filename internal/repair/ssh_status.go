package repair

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// parseReport reads a stored report; ok is false for a broken one.
func parseReport(raw json.RawMessage) (wire.RepairReport, bool) {
	var r wire.RepairReport
	if json.Unmarshal(raw, &r) != nil || wire.CheckRepairReport(&r) != nil {
		return wire.RepairReport{}, false
	}
	return r, true
}

// guestOf returns the guest id of the binding of a target, or "".
func (s *Service) guestOf(ctx context.Context, target string) (string, error) {
	b, err := s.cfg.Store.GetBinding(ctx, kindTarget, target)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	// The guest of this binding generation (T-209): pins and reports of an
	// older binding with the same name or VMID never count.
	return b.GuestKey(), err
}

// OnReport takes the checked repair report of a bound Target (D-384): it
// is stored, the host keys of the first report after a new binding are
// pinned, and keys that are not pinned show in an event once.
func (s *Service) OnReport(ctx context.Context, target string, rep wire.RepairReport) error {
	if err := wire.CheckRepairReport(&rep); err != nil {
		return nil // link checked it; a second check costs nothing
	}
	guest, err := s.guestOf(ctx, target)
	if err != nil || guest == "" {
		return err
	}
	var oldSeen []string
	if prev, err := s.cfg.Store.RepairReport(ctx, target); err == nil && prev.GuestID == guest {
		if r, ok := parseReport(prev.Report); ok {
			oldSeen = r.HostKeys
		}
	}
	raw, _ := json.Marshal(rep)
	now := s.cfg.Now().UTC()
	if err := s.cfg.Store.SetRepairReport(ctx, target, store.RepairReportRow{GuestID: guest, Report: raw, ReportedAt: now}); err != nil {
		return err
	}
	// A host key that is pinned for another Target is never pinned here
	// (review S3 of #298): the Target shows host-key-shared instead.
	shared, err := s.cfg.Store.HostKeyOwners(ctx, rep.HostKeys, target)
	if err != nil {
		return err
	}
	pinned := false
	if len(shared) == 0 {
		if pinned, err = s.cfg.Store.PinFirstHostKeys(ctx, target, guest, rep.HostKeys, now); err != nil {
			return err
		}
	}
	if pinned {
		s.log.Info("host keys pinned from the agent link", "target", target, "keys", len(rep.HostKeys))
		return nil
	}
	pins, err := s.cfg.Store.HostKeyPins(ctx, target, guest)
	if err != nil {
		return err
	}
	seen := minus(rep.HostKeys, pins)
	if len(seen) > 0 && !sameSet(minus(oldSeen, pins), seen) && s.cfg.Events != nil {
		if _, err := s.cfg.Events.Emit(ctx, "target.hostkeys", "target:"+target, "agent:"+target,
			map[string]any{"pinned": pins, "seen": seen}); err != nil {
			s.log.Error("event target.hostkeys", "err", err)
		}
	}
	return nil
}

// Status is TargetStatus.repair without the firewall check (plan adds
// it). nil: the Target has no guest or no agent.
func (s *Service) Status(ctx context.Context, target string) (*plan.RepairStatus, error) {
	guest, err := s.guestOf(ctx, target)
	if err != nil || guest == "" {
		return nil, err
	}
	a, err := s.cfg.Store.AgentByTarget(ctx, target)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := &plan.RepairStatus{State: plan.RepairUnknown, Reasons: []string{}, HostKeys: []string{}, HostKeysSeen: []string{}}
	row, err := s.cfg.Store.RepairReport(ctx, target)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	rep, ok := wire.RepairReport{}, false
	if err == nil && row.GuestID == guest {
		rep, ok = parseReport(row.Report)
	}
	if !ok {
		if a.Hello != nil {
			// An agent that said hello and sent no repair report: an agent
			// before T-191, or one that could not write its line.
			st.State, st.Reasons = plan.RepairOff, []string{"no-line"}
		}
		return st, nil
	}
	keys, err := s.cfg.Store.RepairKeys(ctx)
	if err != nil {
		return nil, err
	}
	if st.HostKeys, err = s.cfg.Store.HostKeyPins(ctx, target, guest); err != nil {
		return nil, err
	}
	st.HostKeysSeen = minus(rep.HostKeys, st.HostKeys)
	st.HostKeyTypes = rep.HostKeyTypes
	valid, stale := false, false
	for i, k := range keys {
		if contains(rep.Lines, k.ID) {
			valid = true
			if i == 0 {
				st.KeyConfirmed = true
			}
		}
	}
	for _, l := range rep.Lines {
		known := false
		for _, k := range keys {
			known = known || k.ID == l
		}
		stale = stale || !known
	}
	// A line of a revoked key is still in the guest (review S2 of #298):
	// the Target counts as on the old key until its agent removed it.
	st.KeyConfirmed = st.KeyConfirmed && !stale
	switch {
	case len(s.cfg.From) == 0:
		st.Reasons = append(st.Reasons, "no-from")
	case len(keys) == 0:
		st.Reasons = append(st.Reasons, "no-key")
	case !valid && len(rep.Lines) > 0:
		st.Reasons = append(st.Reasons, "old-key")
	case !valid:
		st.Reasons = append(st.Reasons, "no-line")
	}
	shared, err := s.cfg.Store.HostKeyOwners(ctx, rep.HostKeys, target)
	if err != nil {
		return nil, err
	}
	if len(shared) > 0 {
		st.Reasons = append(st.Reasons, "host-key-shared")
	}
	switch rep.SSHD.Status {
	case wire.SSHDAbsent:
		st.Reasons = append(st.Reasons, "no-sshd")
	case wire.SSHDRootRefused:
		st.Reasons = append(st.Reasons, "root-refused")
	}
	if rep.SSHD.Status != wire.SSHDAbsent {
		p := rep.SSHD.Port
		st.Port = &p
	}
	if len(st.HostKeys) == 0 {
		st.Reasons = append(st.Reasons, "no-pin")
	}
	st.State = plan.RepairReady
	if len(st.Reasons) > 0 {
		st.State = plan.RepairOff
	}
	switch {
	case len(st.HostKeysSeen) > 0 && len(st.HostKeysSeen) == len(rep.HostKeys):
		// No reported host key is pinned any more (T-207): a repair runs
		// only when a person accepts one of the new keys in the Plan.
		st.KeysChangedOnly = st.State == plan.RepairReady
		st.Reasons = append(st.Reasons, plan.ReasonHostKeyChanged)
		st.State = plan.RepairOff
	case len(st.HostKeysSeen) > 0:
		// A warning: a pinned key is still there and the repair checks
		// only that one; the others can be accepted.
		st.Reasons = append(st.Reasons, plan.ReasonHostKeyNew)
	}
	if stale && valid {
		// A warning, not off: the valid line still repairs, and a dead
		// agent can drop the old line only after its repair.
		st.Reasons = append(st.Reasons, "old-key")
	}
	return st, nil
}

// Ready implements plan.RepairTransport; plan checks Status first.
func (s *Service) Ready(ctx context.Context, target string) error {
	st, err := s.Status(ctx, target)
	if err != nil {
		return err
	}
	// Off only for new host keys: plan lets it run only with an accepted
	// key (T-207).
	if st == nil || (st.State != plan.RepairReady && !st.KeysChangedOnly) {
		return fmt.Errorf("no repair report of a ready guest: %w", plan.ErrRepairNotReady)
	}
	return nil
}

// minus returns the elements of a that are not in b, sorted.
func minus(a, b []string) []string {
	out := []string{}
	for _, x := range a {
		if !contains(b, x) {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !contains(b, x) {
			return false
		}
	}
	return true
}
