package state

import (
	"context"
	"fmt"

	"github.com/Bernotese/plumb/internal/store"
)

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
// stored body must be in canonical form and match its hash. One rule is
// softer than in Validate: a Target whose apply order has a cycle loads,
// and Set.Cycles names it (D-168). Such a revision is from before the
// save-time check; if Load refused it, no proposal could repair it.
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
	set, err := validate(docs, DefaultLimits, true)
	if err != nil {
		return nil, fmt.Errorf("state: revision %d is no longer valid: %w", revision, err)
	}
	return set, nil
}
