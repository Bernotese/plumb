package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"sync"

	"github.com/Bernotese/plumb/blocks"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// Starter catalogue (D-328, ARCHITECTURE §17): the Blocks embedded in the
// binary. The server only lists them; a client adds or updates one with a
// normal proposal. Nothing here writes a document.

// catalogBlock is one item of GET /catalog/blocks.
type catalogBlock struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Kinds       []string        `json:"kinds"`
	SHA256      string          `json:"sha256"`
	Stored      string          `json:"stored"`
	YAML        string          `json:"yaml"`
	Document    json.RawMessage `json:"document"`
}

var (
	catalogOnce  sync.Once
	catalogItems []catalogBlock
	catalogErr   error
)

// catalog returns the parsed catalogue, sorted by name, with Stored
// empty. It is parsed once; the embedded files never change.
func catalog() ([]catalogBlock, error) {
	catalogOnce.Do(func() { catalogItems, catalogErr = loadCatalog(blocks.FS) })
	return catalogItems, catalogErr
}

// loadCatalog parses every *.yaml of fsys into a catalogue item.
func loadCatalog(fsys fs.FS) ([]catalogBlock, error) {
	paths, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return nil, err
	}
	out := make([]catalogBlock, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, err
		}
		item, err := parseCatalogBlock(raw)
		if err != nil {
			return nil, fmt.Errorf("catalogue %s: %w", p, err)
		}
		if seen[item.Name] {
			return nil, fmt.Errorf("catalogue %s: Block %q is twice in the catalogue", p, item.Name)
		}
		seen[item.Name] = true
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// parseCatalogBlock reads one Block file. It uses the YAML reader of the
// import (state.Decode: no anchors, no custom tags, numbers keep their
// literal), so a catalogue Block becomes the same document that an import
// of the same text would give. Decode wants an export header first.
func parseCatalogBlock(raw []byte) (catalogBlock, error) {
	const header = "kind: Export\nformat: 1\n---\n"
	f, err := state.Decode(append([]byte(header), raw...))
	if err != nil {
		return catalogBlock{}, err
	}
	if len(f.Docs) != 1 || f.Docs[0].Kind != state.KindBlock {
		return catalogBlock{}, fmt.Errorf("the file must hold exactly one Block")
	}
	d := f.Docs[0]
	body, err := state.Canonical(d)
	if err != nil {
		return catalogBlock{}, err
	}
	var spec struct {
		Title       string                       `json:"title"`
		Description string                       `json:"description"`
		Resources   []map[string]json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(d.Spec, &spec); err != nil {
		return catalogBlock{}, err
	}
	kinds, err := blockKinds(spec.Resources)
	if err != nil {
		return catalogBlock{}, err
	}
	return catalogBlock{
		Name:        d.Name,
		Title:       spec.Title,
		Description: spec.Description,
		Kinds:       kinds,
		SHA256:      state.Hash(body),
		YAML:        string(raw),
		Document:    json.RawMessage(body),
	}, nil
}

// blockKinds returns the Target kinds on which every resource works, in
// the order vm, lxc.
func blockKinds(resources []map[string]json.RawMessage) ([]string, error) {
	ok := map[wire.Kind]bool{wire.KindVM: true, wire.KindLXC: true}
	for _, r := range resources {
		for typ := range r {
			typeKinds, found := state.TypeKinds(typ)
			if !found {
				return nil, fmt.Errorf("unknown resource type %q", typ)
			}
			has := map[wire.Kind]bool{}
			for _, k := range typeKinds {
				has[k] = true
			}
			for k := range ok {
				ok[k] = ok[k] && has[k]
			}
		}
	}
	kinds := []string{}
	for _, k := range []wire.Kind{wire.KindVM, wire.KindLXC} {
		if ok[k] {
			kinds = append(kinds, string(k))
		}
	}
	return kinds, nil
}

// listCatalogBlocks serves GET /catalog/blocks.
func (s *Server) listCatalogBlocks(w http.ResponseWriter, r *http.Request, _ *principal) {
	items, err := catalog()
	if err != nil {
		s.internal(w, err)
		return
	}
	_, docs, err := s.headDocs(r.Context(), state.KindBlock)
	if err != nil {
		s.internal(w, err)
		return
	}
	stored := map[string]string{}
	for _, d := range docs {
		stored[d.Name] = d.Hash
	}
	out := make([]catalogBlock, len(items))
	for i, it := range items {
		switch h, ok := stored[it.Name]; {
		case !ok:
			it.Stored = "none"
		case h == it.SHA256:
			it.Stored = "same"
		default:
			it.Stored = "differs"
		}
		out[i] = it
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
