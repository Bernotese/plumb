package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// ErrRestoreFailed: the Target has an open Issue guest:restore-failed
// (T-234, host finding F3). Its guest may be empty, so nothing but a
// restore (of the safety backup or another backup) or, human-only, a
// delete of the Target may touch it: a revert would re-create or rewrite
// the empty guest, an Issue action would act on its empty config.
var ErrRestoreFailed = errors.New("the last restore of the Target failed")

// HumanOnlyRestoreFailed: the Plan deletes a Target whose last restore
// failed (T-234). Its guest may still be brought back by a restore, so a
// person decides to delete it instead.
const HumanOnlyRestoreFailed = "restore-failed"

// RestoreFailedIssue returns the id of the open guest:restore-failed Issue
// of target, or 0 when there is none. "Open" is every status but
// resolved: a person ends it with "ignore once" (then the Target is free
// again, by that person's decision) or a restore that works ends it.
func (e *Engine) RestoreFailedIssue(ctx context.Context, target string) (int64, error) {
	iss, err := e.st.OpenIssue(ctx, target, GuestRestoreFailedID)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return iss.ID, nil
}

// restoreFailedError is the refusal of a Plan for a Target whose last
// restore failed. It names the Issue and the way back.
func restoreFailedError(target string, issueID int64) error {
	return &ProposalError{Target: target, Err: ErrRestoreFailed, Msg: fmt.Sprintf(
		"the last restore failed and the guest may have no disk (Issue %d, %s). Only a restore (POST /targets/%s/restore, of the safety backup that the Issue names or of another backup) or a delete of the Target by a person is allowed until a restore works",
		issueID, GuestRestoreFailedID, target)}
}

// restoreFailedGate checks the Targets of a Plan of the general kind
// (T-234): a Target with an open guest:restore-failed refuses the Plan,
// except a delete of that Target, which then needs a person (the second
// result is true).
func (e *Engine) restoreFailedGate(ctx context.Context, plans []TargetPlan) (humanOnly bool, err error) {
	for _, tp := range plans {
		id, err := e.RestoreFailedIssue(ctx, tp.Target)
		if err != nil {
			return false, err
		}
		if id == 0 {
			continue
		}
		if tp.Guest != "delete" && tp.Guest != "unbind" { // an adopted Target's unbind leaves the guest as it is
			return false, restoreFailedError(tp.Target, id)
		}
		humanOnly = true
	}
	return humanOnly, nil
}

// restoreFailedNames is restoreFailedGate before the planning: a Target
// that the next set no longer renders is a delete.
func (e *Engine) restoreFailedNames(ctx context.Context, next *state.Set, names []string) (humanOnly bool, err error) {
	plans := make([]TargetPlan, 0, len(names))
	for _, t := range names {
		tp := TargetPlan{Target: t}
		r, err := e.renderOrNil(next, t)
		if err != nil {
			return false, err
		}
		if r == nil {
			tp.Guest = "delete"
		}
		plans = append(plans, tp)
	}
	return e.restoreFailedGate(ctx, plans)
}

// restoreFailedOne is the gate of a Plan kind with one Target and no
// delete (agent update, repair, re-enroll).
func (e *Engine) restoreFailedOne(ctx context.Context, target string) error {
	id, err := e.RestoreFailedIssue(ctx, target)
	if err != nil || id == 0 {
		return err
	}
	return restoreFailedError(target, id)
}

// approveRestoreFailed is the gate again at approval (T-234): the Issue
// may have opened after the Plan was made. A restore Plan passes (its own
// checks run), a build has no Target. A delete gets the human-only reason
// in p, so the approval check sees it.
func (e *Engine) approveRestoreFailed(ctx context.Context, p *store.Plan) error {
	if p.Origin == OriginRestore || p.Origin == OriginBuild {
		return nil
	}
	var targets []TargetPlan
	if err := json.Unmarshal(p.Changes, &targets); err != nil {
		return err
	}
	human, err := e.restoreFailedGate(ctx, targets)
	if err != nil {
		return err
	}
	if human && !contains(p.HumanOnly, HumanOnlyRestoreFailed) {
		p.HumanOnly = append(append([]string{}, p.HumanOnly...), HumanOnlyRestoreFailed)
	}
	return nil
}

// staleConfigComment is the comment on a guest:config Issue that a restore
// that worked ends (T-234).
const staleConfigComment = "A restore replaced the guest, so this Issue showed a guest that is gone (during a failed restore: the empty guest). The next guest poll compares the restored guest again and opens a new Issue if it differs."

// endStaleConfigIssue resolves the open guest:config Issue of target by
// system after a restore that worked, with a comment (T-234). An Issue
// that a Plan resolves (resolving) is left to that Plan.
func (e *Engine) endStaleConfigIssue(ctx context.Context, target string) {
	iss, err := e.st.OpenIssue(ctx, target, wire.GuestConfigID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		e.log.Warn("read the guest config Issue after a restore", "target", target, "err", err)
		return
	}
	if iss.Status != store.IssueOpen {
		return
	}
	if _, err := e.st.AddComment(ctx, store.Comment{IssueID: iss.ID, Author: "system", Body: staleConfigComment}); err != nil {
		e.log.Warn("comment the guest config Issue after a restore", "target", target, "err", err)
	}
	if err := e.restoreIssue(ctx, target, wire.GuestConfigID, wire.StatusUnchanged, nil); err != nil {
		e.log.Warn("end the guest config Issue after a restore", "target", target, "err", err)
	}
}
