package plan

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// Bounds of Plan.documents (T-170): past either, the remaining entries
// keep kind, name and op without the document text. Variables only so
// that a test can lower them.
var (
	MaxPlanDocuments     = 500
	MaxPlanDocumentBytes = 8 << 20
)

// DocumentChange is one entry of Plan.documents (api DocumentChange).
type DocumentChange struct {
	Kind   string          `json:"kind"`
	Name   string          `json:"name"`
	Op     store.DiffOp    `json:"op"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

// plannedSets rebuilds the base set of a Plan and the set that its
// approval commits: the proposal applied on the base revision of the Plan.
// Approve and PlanDocuments use it, so the screen shows exactly what an
// approval commits.
func (e *Engine) plannedSets(ctx context.Context, p store.Plan) (base, next *state.Set, err error) {
	var prop Proposal
	if err := json.Unmarshal(p.Proposal, &prop); err != nil {
		return nil, nil, err
	}
	if p.BaseRevision == 0 {
		base, err = state.Validate(nil)
	} else {
		base, err = e.load(ctx, p.BaseRevision)
	}
	if err != nil {
		return nil, nil, err
	}
	next, err = apply(base, prop)
	return base, next, err
}

// PlanDocuments returns the document changes that approving p commits
// (T-170), sorted by kind and name, and whether the size bound cut the
// document text of some entries. A build or an agent update commits no
// document: none.
func (e *Engine) PlanDocuments(ctx context.Context, p store.Plan) ([]DocumentChange, bool, error) {
	if p.Origin == OriginBuild || p.Origin == OriginAgentUpdate {
		return []DocumentChange{}, false, nil
	}
	base, next, err := e.plannedSets(ctx, p)
	if err != nil {
		return nil, false, err
	}
	before, err := state.Rows(base)
	if err != nil {
		return nil, false, err
	}
	after, err := state.Rows(next)
	if err != nil {
		return nil, false, err
	}
	type key struct{ kind, name string }
	old := map[key]store.DocRow{}
	for _, r := range before {
		old[key{r.Kind, r.Name}] = r
	}
	var out []DocumentChange
	seen := map[key]bool{}
	for _, r := range after {
		k := key{r.Kind, r.Name}
		seen[k] = true
		prev, had := old[k]
		switch {
		case !had:
			out = append(out, DocumentChange{Kind: r.Kind, Name: r.Name, Op: store.DocAdded, Before: nil, After: r.Body})
		case prev.Hash != r.Hash:
			out = append(out, DocumentChange{Kind: r.Kind, Name: r.Name, Op: store.DocChanged, Before: prev.Body, After: r.Body})
		}
	}
	for k, r := range old {
		if !seen[k] {
			out = append(out, DocumentChange{Kind: r.Kind, Name: r.Name, Op: store.DocRemoved, Before: r.Body})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	truncated := false
	size := 0
	for i := range out {
		size += len(out[i].Before) + len(out[i].After)
		if i >= MaxPlanDocuments || size > MaxPlanDocumentBytes {
			out[i].Before, out[i].After = nil, nil
			truncated = true
		}
	}
	for i := range out { // JSON null, as RevisionDiff
		if out[i].Before == nil {
			out[i].Before = json.RawMessage("null")
		}
		if out[i].After == nil {
			out[i].After = json.RawMessage("null")
		}
	}
	if out == nil {
		out = []DocumentChange{}
	}
	return out, truncated, nil
}
