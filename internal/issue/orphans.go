package issue

import (
	"context"
	"errors"
	"strings"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// SweepDeletedTargets ends the Issues and marks of Targets that were
// deleted before D-397 (T-212): a target name with an open Issue, an
// "Ignore once" memory or a record of dropped drift, which has no Target
// binding and is no Target of the head revision, gets what a delete does
// now (TargetDeleted: each open Issue resolved gone by system with the
// comment and one event; issue_ignore and ignored_drift dropped; the copy
// rule rows stay; a "resolving" Issue whose Run is not running is ended
// too). serve runs it at start, before the listeners, unless the
// controller is in restored mode (D-303: system work waits); then the
// restore confirm runs it (plan.Config.AfterRestoreConfirm, review R1 of
// #319).
// It is a start sweep, not a migration: a migration cannot emit the
// events, and the sweep is idempotent (a second run changes nothing). It
// returns the names where it ended something.
func (s *Service) SweepDeletedTargets(ctx context.Context) ([]string, error) {
	names, err := s.st.MarkedTargets(ctx)
	if err != nil || len(names) == 0 {
		return nil, err
	}
	exists := map[string]bool{}
	binds, err := s.st.Bindings(ctx, state.KindTarget)
	if err != nil {
		return nil, err
	}
	for _, b := range binds {
		exists[b.Name] = true
	}
	head, err := s.st.Head(ctx)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		docs, err := s.st.RevisionDocs(ctx, head.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			if d.Kind == state.KindTarget {
				exists[d.Name] = true
			}
		}
	}
	var swept []string
	for _, t := range names {
		// A Target of the head (also one without a guest yet, or after a
		// restore) or with a binding exists; an image build lane is no
		// Target.
		if exists[t] || strings.HasPrefix(t, wire.BuildLinkPrefix) {
			continue
		}
		n, err := s.endTarget(ctx, t, plan.TargetDeletedReason, true)
		if err != nil {
			return swept, err
		}
		if n > 0 {
			swept = append(swept, t)
		}
	}
	return swept, nil
}

// SweepAtStart is the start of serve: the sweep, unless the controller is
// in restored mode (D-303: system work waits for the confirm, which runs
// the sweep through plan.Config.AfterRestoreConfirm; review R1 of #319).
func (s *Service) SweepAtStart(ctx context.Context, restored bool) ([]string, error) {
	if restored {
		return nil, nil
	}
	return s.SweepDeletedTargets(ctx)
}
