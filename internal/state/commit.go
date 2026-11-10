package state

import (
	"context"
	"fmt"

	"github.com/Bernotese/plumb/internal/store"
)

// CommitInfo describes who commits a revision and why.
type CommitInfo struct {
	Author string // principal
	Reason string
	PlanID int64 // 0: no plan (seed revision)
}

// Commit stores the documents of a validated Set as a new revision. Each
// document is stored by the hash of its canonical body (Canonical, Hash),
// so documents that did not change are shared with older revisions.
func Commit(ctx context.Context, st *store.Store, set *Set, info CommitInfo) (store.Revision, error) {
	if set == nil {
		return store.Revision{}, fmt.Errorf("state: commit needs a validated set")
	}
	rows, err := Rows(set)
	if err != nil {
		return store.Revision{}, err
	}
	return st.CommitRevision(ctx, store.NewRevision{
		Author: info.Author, Reason: info.Reason, PlanID: info.PlanID, Docs: rows,
	})
}

// Rows returns the store rows of a validated Set: canonical body and hash
// of each document. This is the only way documents reach the store.
func Rows(set *Set) ([]store.DocRow, error) {
	rows := make([]store.DocRow, 0, len(set.docs))
	for _, d := range set.docs {
		body, err := Canonical(d)
		if err != nil {
			return nil, err
		}
		rows = append(rows, store.DocRow{Kind: d.Kind, Name: d.Name, Hash: Hash(body), Body: body})
	}
	return rows, nil
}

// Load reads the documents of a revision and validates them again. Every
// stored body must be in canonical form and match its hash.
func Load(ctx context.Context, st *store.Store, revision int64) (*Set, error) {
	rows, err := st.RevisionDocs(ctx, revision)
	if err != nil {
		return nil, err
	}
	docs := make([]Doc, 0, len(rows))
	for _, r := range rows {
		d, err := CheckCanonical(r.Body)
		if err != nil {
			return nil, fmt.Errorf("state: revision %d: %w", revision, err)
		}
		if Hash(r.Body) != r.Hash || d.Kind != r.Kind || d.Name != r.Name {
			return nil, fmt.Errorf("state: revision %d: %s %q does not match its hash", revision, r.Kind, r.Name)
		}
		docs = append(docs, d)
	}
	set, err := Validate(docs)
	if err != nil {
		return nil, fmt.Errorf("state: revision %d is no longer valid: %w", revision, err)
	}
	return set, nil
}
