package link

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// A parked agent (D-396, T-205): the agent of a deleted adopted Target
// whose unbind was not confirmed. Its credential stays valid so that it
// can take unbind at its next contact, and that is the one thing it can
// do: Send queues nothing else for it, its poll gets nothing else, and
// every report except the result (or progress) of its unbind command is
// dropped without a delivery (a hello is taken, so the agent goes on to
// poll, but its data is not used). No new enrollment token is made for a
// parked target; an admin first forgets the guest's agent.

// ErrParked: the target has a parked agent (D-396).
var ErrParked = errors.New("link: the target has a parked agent that only takes unbind")

func (l *Link) parkedTarget(ctx context.Context, target string) (bool, error) {
	if l.cfg.Store == nil {
		return false, nil
	}
	return l.cfg.Store.IsParkedTarget(ctx, target)
}

func (l *Link) parkedAgent(ctx context.Context, a store.Agent) (bool, error) {
	if l.cfg.Store == nil {
		return false, nil
	}
	return l.cfg.Store.IsParked(ctx, a.Target, a.ID)
}

// checkSendParked refuses every command but unbind to a parked target.
func (l *Link) checkSendParked(target string, typ wire.CommandType) error {
	if typ == wire.CommandUnbind {
		return nil
	}
	parked, err := l.parkedTarget(context.Background(), target)
	if err != nil {
		return err
	}
	if parked {
		return fmt.Errorf("%w: no %s command to %s", ErrParked, typ, target)
	}
	return nil
}

// checkTokenParked refuses a new enrollment or repair token for a parked
// target.
func (l *Link) checkTokenParked(ctx context.Context, target string) error {
	parked, err := l.parkedTarget(ctx, target)
	if err != nil {
		return err
	}
	if parked {
		return fmt.Errorf("%w: forget the old agent of %s first", ErrParked, target)
	}
	return nil
}

// parkedReport decides a report of a parked agent: true when the handler
// is done (the report was dropped with 204); false when the report is
// the result or progress of the agent's unbind command, which goes on as
// usual.
func (l *Link) parkedReport(w http.ResponseWriter, r *http.Request, a store.Agent, rep wire.Report) bool {
	parked, err := l.parkedAgent(r.Context(), a)
	if err != nil {
		storeUnavailable(w)
		return true
	}
	if !parked {
		return false
	}
	if rep.Type == wire.ReportResult || rep.Type == wire.ReportProgress {
		l.mu.Lock()
		typ := l.target(a.Target).outstanding[rep.CommandID]
		l.mu.Unlock()
		if typ == wire.CommandUnbind {
			return false
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}
