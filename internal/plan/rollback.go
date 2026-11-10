package plan

import (
	"context"
	"errors"
	"fmt"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// ErrNothingToDo is returned for a rollback to a revision whose document
// set equals the head.
var ErrNothingToDo = errors.New("nothing to do")

// Rollback plans a return to revision n (D-093): a proposal that puts and
// deletes documents until the set equals revision n. It goes through
// Propose like every proposal, so every rule holds (stale, tiers, risk,
// create-only fields, D-099). Secret values have no history. An unknown
// revision is store.ErrNotFound.
func (e *Engine) Rollback(ctx context.Context, n int64, reason, author string) (store.Plan, error) {
	head, err := e.st.Head(ctx)
	if err != nil {
		return store.Plan{}, err // no revision yet: ErrNotFound
	}
	want, err := e.st.RevisionDocs(ctx, n)
	if err != nil {
		return store.Plan{}, err
	}
	have, err := e.st.RevisionDocs(ctx, head.ID)
	if err != nil {
		return store.Plan{}, err
	}
	var p Proposal
	keep := map[DocKey]bool{}
	cur := map[DocKey]string{}
	for _, d := range have {
		cur[DocKey{d.Kind, d.Name}] = d.Hash
	}
	for _, d := range want {
		k := DocKey{d.Kind, d.Name}
		keep[k] = true
		if cur[k] == d.Hash {
			continue
		}
		doc, err := state.CheckCanonical(d.Body)
		if err != nil {
			return store.Plan{}, fmt.Errorf("plan: rollback: revision %d: %w", n, err)
		}
		p.Put = append(p.Put, doc)
	}
	for _, d := range have {
		if k := (DocKey{d.Kind, d.Name}); !keep[k] {
			p.Delete = append(p.Delete, k)
		}
	}
	if len(p.Put)+len(p.Delete) == 0 {
		return store.Plan{}, fmt.Errorf("plan: revision %d has the same documents as the head revision %d: %w", n, head.ID, ErrNothingToDo)
	}
	return e.Propose(ctx, Input{Proposal: p, Reason: reason, Author: author, Origin: OriginRollback, Base: head.ID, RollbackTo: n})
}
