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
	// Write names the secrets of the bundle that the import creates or
	// replaces. WriteSecrets writes them and returns the Plan warnings; it
	// runs inside Propose after every check, so a refused import writes
	// nothing (D-153). nil writes nothing.
	Write        []string
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
	if err := e.checkSecretsPresent(ctx, next, next.Targets(), in.Write); err != nil {
		return store.Plan{}, err
	}
	return e.Propose(ctx, Input{Proposal: p, Reason: in.Reason, Author: in.Author, Origin: OriginImport, Base: headID,
		Imported: in.Write, BeforeStore: in.WriteSecrets})
}
