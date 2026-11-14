package wire

import (
	"fmt"
	"strings"
)

// EdgeKind says why one resource waits for another one in an apply.
type EdgeKind string

const (
	// EdgeBlock: the resource before it in the same Block use (D-027).
	EdgeBlock EdgeKind = "block"
	// EdgeRestart: a service waits for a resource of its restart_on list
	// and restarts when that resource changed (D-036). The service still
	// runs when the resource failed.
	EdgeRestart EdgeKind = "restart_on"
	// EdgeAfter: Resource.After (D-167). The resource runs only when the
	// other one ended unchanged or applied (D-170).
	EdgeAfter EdgeKind = "after"
)

// Edge is one wait of a resource: it starts after list[To] ended.
type Edge struct {
	To   int      // index in the list that Order got
	Kind EdgeKind // the two kinds restart_on and after have different rules, so keep them apart (D-170)
}

// Reasons of an OrderError.
const (
	OrderDuplicate   = "duplicate"    // an id is in the list twice
	OrderUnknown     = "unknown"      // an after id is not in the list
	OrderGroupFirst  = "group-first"  // same Block lists a user before its group
	OrderGroupAbsent = "group-absent" // a present user needs an absent group
	OrderCycle       = "cycle"        // resources wait for each other
)

// OrderError says why a resource list has no apply order.
type OrderError struct {
	Reason string
	// IDs: duplicate: the id. unknown: the resource and the after id it
	// names. cycle: the resources of the cycle; each waits for the next
	// one and the last waits for the first. Group errors name the user and group.
	IDs []string
	// Blocks are the Block uses of IDs, at the same index ("" for an
	// unknown id).
	Blocks []string
}

func (e *OrderError) Error() string {
	name := func(i int) string { return fmt.Sprintf("%s (use %q)", e.IDs[i], e.Blocks[i]) }
	switch e.Reason {
	case OrderGroupFirst:
		return fmt.Sprintf("user %s needs group %s first: list the group before the user", strings.TrimPrefix(e.IDs[0], "user:"), strings.TrimPrefix(e.IDs[1], "group:"))
	case OrderGroupAbsent:
		return fmt.Sprintf("user %s needs group %s, but the managed group is absent", strings.TrimPrefix(e.IDs[0], "user:"), strings.TrimPrefix(e.IDs[1], "group:"))
	case OrderDuplicate:
		return fmt.Sprintf("resource %s is listed twice", e.IDs[0])
	case OrderUnknown:
		return fmt.Sprintf("%s comes after %s, but %s is not in the resource list", name(0), e.IDs[1], e.IDs[1])
	}
	var b strings.Builder
	b.WriteString("apply order has a cycle: ")
	b.WriteString(name(0))
	for i := 1; i < len(e.IDs); i++ {
		b.WriteString(" waits for " + name(i) + ", which")
	}
	if len(e.IDs) == 1 {
		b.WriteString(" waits for itself")
	} else {
		b.WriteString(" waits for " + e.IDs[0])
	}
	b.WriteString("; check after, restarts, restart_on and the order of the resources in each Block")
	return b.String()
}

// Order builds the wait graph of the resource list of one target. This is
// the one rule for the apply order (D-168): state.Render uses it to refuse
// a state that can never apply, and the agent uses it to run an apply.
//
// The result has one entry per resource, at the same index: the resources
// that it waits for.
//
//   - EdgeBlock: the resource before it with the same Block.
//   - EdgeRestart: for a service, each id of its restart_on list. An id
//     that is not in the list, or the service itself, gives no edge
//     (state.Render checks restart_on ids, D-053).
//   - EdgeAfter: each id of After. An id that is not in the list is an
//     error, and so is the resource itself (a cycle of one).
//
// A pair of resources can have more than one edge, of different kinds.
// Resources with no path between them have no order: they run in parallel.
// The error is an *OrderError: a duplicate id, an unknown after id, a group
// requirement that cannot apply, or a cycle.
func Order(list []Resource) ([][]Edge, error) {
	byID := make(map[string]int, len(list))
	for i, r := range list {
		if _, dup := byID[r.ID]; dup {
			return nil, &OrderError{Reason: OrderDuplicate, IDs: []string{r.ID}, Blocks: []string{r.Block}}
		}
		byID[r.ID] = i
	}
	edges := make([][]Edge, len(list))
	add := func(from, to int, kind EdgeKind) {
		for _, e := range edges[from] {
			if e.To == to && e.Kind == kind {
				return
			}
		}
		edges[from] = append(edges[from], Edge{To: to, Kind: kind})
	}
	last := map[string]int{} // Block use → index of its latest resource so far
	for i, r := range list {
		if prev, ok := last[r.Block]; ok {
			add(i, prev, EdgeBlock)
		}
		last[r.Block] = i
	}
	for i, r := range list {
		// Present users need present managed groups before them.
		if r.Type == "user" && r.Attrs[AttrState] != StateAbsent {
			var names []string
			switch v := r.Attrs["groups"].(type) {
			case []string:
				names = v
			case []any:
				for _, n := range v {
					if name, ok := n.(string); ok {
						names = append(names, name)
					}
				}
			}
			for _, name := range names {
				if to, ok := byID[ID("group", name)]; ok {
					reason := ""
					if list[to].Attrs[AttrState] == StateAbsent {
						reason = OrderGroupAbsent
					} else if r.Block == list[to].Block && i < to {
						reason = OrderGroupFirst
					}
					if reason != "" {
						return nil, &OrderError{Reason: reason, IDs: []string{r.ID, list[to].ID}, Blocks: []string{r.Block, list[to].Block}}
					}
					add(i, to, EdgeAfter)
				}
			}
		}
		if r.Type == "service" {
			for _, id := range RestartOn(r) {
				if to, ok := byID[id]; ok && to != i {
					add(i, to, EdgeRestart)
				}
			}
		}
		for _, id := range r.After {
			to, ok := byID[id]
			if !ok {
				return nil, &OrderError{Reason: OrderUnknown, IDs: []string{r.ID, id}, Blocks: []string{r.Block, ""}}
			}
			add(i, to, EdgeAfter)
		}
	}

	// Depth-first search; a grey resource that is reached again closes a cycle.
	const (
		white = iota
		grey
		black
	)
	color := make([]int, len(list))
	var path []int
	var visit func(int) []int
	visit = func(i int) []int {
		color[i] = grey
		path = append(path, i)
		for _, e := range edges[i] {
			switch color[e.To] {
			case grey:
				for k, p := range path {
					if p == e.To {
						return append([]int(nil), path[k:]...)
					}
				}
			case white:
				if c := visit(e.To); c != nil {
					return c
				}
			}
		}
		path = path[:len(path)-1]
		color[i] = black
		return nil
	}
	for i := range list {
		if color[i] != white {
			continue
		}
		if c := visit(i); c != nil {
			err := &OrderError{Reason: OrderCycle}
			for _, k := range c {
				err.IDs = append(err.IDs, list[k].ID)
				err.Blocks = append(err.Blocks, list[k].Block)
			}
			return nil, err
		}
	}
	return edges, nil
}

// RestartOn returns the restart_on list of a resource: the ids whose change
// restarts a service (D-036). It reads the JSON form ([]any of strings) and
// the Go form ([]string) and leaves out values that are not strings. The
// result is a new slice: a caller can change it and not the resource. Every
// reader of restart_on uses it, so both forms always give the same result
// (D-172).
func RestartOn(r Resource) []string {
	switch v := r.Attrs["restart_on"].(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
