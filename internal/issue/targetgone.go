package issue

import (
	"context"

	"github.com/Bernotese/plumb/internal/store"
)

// TargetDeleted ends the Issues and marks of a Target whose binding a
// delete dropped (T-209 sweep): no report can resolve them any more (a
// report without a binding is dropped, D-211), and a new Target of the
// same name must not inherit them. Every open Issue is resolved "gone" by
// system, with the reason as a comment and an issue.resolved event. An
// Issue that a Plan is resolving stays for its Run.
//
// The copy rule keeps what it needs (D-397, pl-lead's caution): the value
// cap flag (D-353) and its agent:values Issue stay until the flag ends, so
// a Target that is deleted and made again under the name stays human-only
// for the values that were never stored; the stored value hashes stay
// for their normal retention (D-331) and still match a copy into any
// Target. The engine calls it after the delete step.
func (s *Service) TargetDeleted(ctx context.Context, target, reason string) error {
	_, err := s.endTarget(ctx, target, reason)
	return err
}

// endTarget does TargetDeleted and returns how many Issues and marks it
// ended (T-212: the start sweep logs only real work).
func (s *Service) endTarget(ctx context.Context, target, reason string) (int64, error) {
	var n int64
	list, err := s.st.Issues(ctx, target)
	if err != nil {
		return 0, err
	}
	flagged, err := s.st.GuestValueFlagged(ctx, []string{target}, s.st.Now())
	if err != nil {
		return 0, err
	}
	for _, iss := range list {
		if iss.Status != store.IssueOpen {
			continue
		}
		if iss.Resource == GuestValuesID && len(flagged) > 0 {
			continue // the end of the flag resolves it (PruneGuestValues)
		}
		if err := s.st.ResolveIssue(ctx, iss.ID, "gone", "system"); err != nil {
			return n, err
		}
		if _, err := s.st.AddComment(ctx, store.Comment{IssueID: iss.ID, Author: "system", Body: "Resolved: " + reason + "."}); err != nil {
			return n, err
		}
		s.emit(ctx, "issue.resolved", iss.ID, "system", map[string]any{"resolution": "gone", "reason": reason})
		n++
	}
	m, err := s.st.ForgetTargetMarks(ctx, target)
	return n + m, err
}
