package issue

import (
	"context"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// GuestValuesID is the Issue of a flagged Target (plan.GuestValuesID).
const GuestValuesID = plan.GuestValuesID

// recordGuestValues stores the keyed hashes of the reported values of a
// drift report (D-331), before Rules and "ignore once" look at it: drift
// that a Rule covers or that is ignored counts too. Past the cap nothing
// new is stored (no row is evicted), and a value with more elements than
// state.MaxGuestElems is stored only in part (review K1 of #248); both
// flag the Target for plan.GuestValueWindow and open agent:values.
func (s *Service) recordGuestValues(ctx context.Context, target string, rep wire.Report) error {
	var vals []store.GuestValue
	over := false
	for _, it := range rep.Items {
		v, more := s.plans.GuestValues(it)
		vals, over = append(vals, v...), over || more
	}
	overflow, err := s.st.RecordGuestValues(ctx, target, vals)
	if err != nil {
		return err
	}
	if !overflow && !over {
		return nil
	}
	if err := s.st.FlagGuestValues(ctx, target, s.st.Now().Add(plan.GuestValueWindow)); err != nil {
		return err
	}
	return s.Intake(ctx, target, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: GuestValuesID, Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "reported_values", To: "cap reached", Risk: wire.RiskRead}}}}})
}

// PruneGuestValues runs the daily clean-up of the copy rule: rows 30 days
// after their drift ended go, and an ended flag resolves its agent:values
// Issue as gone.
func (s *Service) PruneGuestValues(ctx context.Context) error {
	now := s.st.Now()
	if _, err := s.st.PruneGuestValues(ctx, now.Add(-plan.GuestValueWindow)); err != nil {
		return err
	}
	ended, err := s.st.EndGuestValueFlags(ctx, now)
	if err != nil {
		return err
	}
	for _, t := range ended {
		if err := s.Intake(ctx, t, wire.Report{Type: wire.ReportDrift,
			Items: []wire.Item{{Resource: GuestValuesID, Status: wire.StatusUnchanged}}}); err != nil {
			return err
		}
	}
	return nil
}

// RunGuestValuePrune calls PruneGuestValues at once and then every
// interval (serve: a day) until ctx ends.
func (s *Service) RunGuestValuePrune(ctx context.Context, interval time.Duration, logf func(err error)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.PruneGuestValues(ctx); err != nil && ctx.Err() == nil && logf != nil {
			logf(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
