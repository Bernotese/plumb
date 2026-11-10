package plan

import (
	"context"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// ImportInput is an import of an export file (D-095).
type ImportInput struct {
	Docs    []state.Doc
	Replace bool // mode replace: documents that are not in the file are deleted
	Reason  string
	Author  string
	// Bundled names the secrets that WriteSecrets creates; they count as
	// existing for the missing-secret check.
	Bundled []string
	// WriteSecrets writes the secrets of the bundle and returns the
	// warnings for the Plan. It runs only after the documents are valid
	// and every reference resolves, so a refused import writes nothing.
	// nil writes nothing.
	WriteSecrets func(ctx context.Context) ([]string, error)
}

// Import plans an import: a proposal that puts every document of the file
// and, in mode replace, deletes the others. It goes through Propose with
// origin import and the head as expected base, so every rule holds (D-022,
// D-099).
func (e *Engine) Import(ctx context.Context, in ImportInput) (store.Plan, error) {
	headID, base, err := e.base(ctx)
	if err != nil {
		return store.Plan{}, err
	}
	p := Proposal{Put: in.Docs}
	if in.Replace {
		keep := map[DocKey]bool{}
		for _, d := range in.Docs {
			keep[DocKey{d.Kind, d.Name}] = true
		}
		for _, d := range base.Docs() {
			if k := (DocKey{d.Kind, d.Name}); !keep[k] {
				p.Delete = append(p.Delete, k)
			}
		}
	}
	next, err := apply(base, p)
	if err != nil {
		return store.Plan{}, err
	}
	if err := e.checkSecretsPresent(ctx, next, next.Targets(), in.Bundled); err != nil {
		return store.Plan{}, err
	}
	if in.WriteSecrets != nil {
		notes, err := in.WriteSecrets(ctx)
		if err != nil {
			return store.Plan{}, err
		}
		p.Notes = notes
	}
	return e.Propose(ctx, Input{Proposal: p, Reason: in.Reason, Author: in.Author, Origin: OriginImport, Base: headID})
}
