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
// Issue that a Plan is resolving stays for its Run. The engine calls it
// after the delete step.
func (s *Service) TargetDeleted(ctx context.Context, target, reason string) error {
	list, err := s.st.Issues(ctx, target)
	if err != nil {
		return err
	}
	for _, iss := range list {
		if iss.Status != store.IssueOpen {
			continue
		}
		if err := s.st.ResolveIssue(ctx, iss.ID, "gone", "system"); err != nil {
			return err
		}
		if _, err := s.st.AddComment(ctx, store.Comment{IssueID: iss.ID, Author: "system", Body: "Resolved: " + reason + "."}); err != nil {
			return err
		}
		s.emit(ctx, "issue.resolved", iss.ID, "system", map[string]any{"resolution": "gone", "reason": reason})
	}
	return s.st.ForgetTargetMarks(ctx, target)
}
