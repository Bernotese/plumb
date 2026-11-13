package api

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Raw mode in the views (D-296, D-301): Issue.source, Issue.raw and
// TargetView.raw. Read only: edit and "Back to the Block" are normal
// proposals on spec.overrides.

// sourceView is ResourceSource: the Block use that renders a resource.
type sourceView struct {
	Block string `json:"block"`
	Use   string `json:"use"`
}

// rawView is RawResource: one entry of spec.overrides. Attribute names
// only, never values, so no secret can show.
type rawView struct {
	Resource string      `json:"resource"`
	Attrs    []string    `json:"attrs"`
	Source   *sourceView `json:"source"`
}

// rawCache holds the sources of the head revision. A revision never
// changes, so the cache is valid while the head is the same.
type rawCache struct {
	mu      sync.Mutex
	rev     int64
	set     *state.Set
	sources map[string]map[string]state.Source // target → resource → source
}

// headRaw runs fn with the Set of the head revision and the sources of
// target, under the cache lock. No head: fn is not called.
func (s *Server) headRaw(ctx context.Context, target string, fn func(set *state.Set, src map[string]state.Source)) error {
	head, err := s.st.Head(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	c := &s.raw
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.set == nil || c.rev != head.ID {
		set, err := state.Load(ctx, s.st, head.ID)
		if err != nil {
			return err
		}
		c.rev, c.set, c.sources = head.ID, set, map[string]map[string]state.Source{}
	}
	src, ok := c.sources[target]
	if !ok {
		// A Target that is gone or does not render has no sources.
		src, _ = c.set.Sources(target)
		c.sources[target] = src
	}
	fn(c.set, src)
	return nil
}

func toSourceView(src map[string]state.Source, id string) *sourceView {
	if typ, _, _ := wire.SplitID(id); typ == state.GuestType || typ == wire.AgentItemType {
		return nil
	}
	if so, ok := src[id]; ok {
		return &sourceView{Block: so.Block, Use: so.Use}
	}
	return nil
}

// issueSource sets Issue.source and Issue.raw.
func (s *Server) issueSource(ctx context.Context, v *issueView) error {
	return s.headRaw(ctx, v.Target, func(_ *state.Set, src map[string]state.Source) {
		v.Source = toSourceView(src, v.Resource)
		v.Raw = v.Source != nil && slices.Contains(v.Actions, "accept")
	})
}

// targetRaw returns TargetView.raw: the overrides, sorted, never nil.
func (s *Server) targetRaw(ctx context.Context, target string) ([]rawView, error) {
	out := []rawView{}
	err := s.headRaw(ctx, target, func(set *state.Set, src map[string]state.Source) {
		for _, o := range set.Overrides(target) {
			out = append(out, rawView{Resource: o.Resource, Attrs: o.Attrs, Source: toSourceView(src, o.Resource)})
		}
	})
	return out, err
}
