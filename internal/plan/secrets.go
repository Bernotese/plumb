package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Secret delivery (D-089). Clear values exist only in the body of an
// observe, apply or watch command; everything that is stored holds the
// markers of the render or the names.

// PlannedSecret is a secret that a target gets, with the version that the
// Plan was made with (TargetPlan.Secrets).
type PlannedSecret struct {
	Name    string `json:"name"`
	Version int64  `json:"version"`
}

// MissingSecretsError is returned by Propose when a touched target refers
// to a secret that does not exist (422 with the pointers).
type MissingSecretsError struct {
	Names  []string
	Errors state.Errors // one per reference, with the pointer of the parameter
}

func (e *MissingSecretsError) Error() string {
	return "plan: missing secrets: " + strings.Join(e.Names, ", ")
}

// ErrSecretChanged is wrapped when a Run meets a secret with another
// version than planned, or a missing one.
var ErrSecretChanged = errors.New("a secret changed or is gone since the Plan")

// checkSecrets returns a MissingSecretsError for the references of the
// targets that do not exist, or nil.
func (e *Engine) checkSecrets(ctx context.Context, set *state.Set, targets []string) error {
	var refs []state.SecretRef
	owner := map[int]string{}
	for _, t := range targets {
		for _, r := range set.SecretRefs(t) {
			owner[len(refs)] = t
			refs = append(refs, r)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.Name
	}
	have, err := e.versions(ctx, names)
	if err != nil {
		return err
	}
	missing := map[string]bool{}
	var errs state.Errors
	for i, r := range refs {
		if _, ok := have[r.Name]; !ok {
			missing[r.Name] = true
			errs = append(errs, &state.Error{Kind: state.KindTarget, Name: owner[i], Path: r.Path,
				Msg: fmt.Sprintf("secret %q does not exist; create it with PUT /secrets/%s first", r.Name, r.Name)})
		}
	}
	if len(errs) == 0 {
		return nil
	}
	list := make([]string, 0, len(missing))
	for n := range missing {
		list = append(list, n)
	}
	sort.Strings(list)
	return &MissingSecretsError{Names: list, Errors: errs}
}

func (e *Engine) versions(ctx context.Context, names []string) (map[string]int64, error) {
	if e.cfg.Secrets == nil || len(names) == 0 {
		return map[string]int64{}, nil
	}
	return e.cfg.Secrets.Versions(ctx, names)
}

// resolve returns the clear values of the secrets of a render. With
// planned, every version must be exactly the planned one (D-089, D-130:
// "equal", never "not lower"); without, the current values are used.
func (e *Engine) resolve(ctx context.Context, names []string, planned []PlannedSecret) (map[string]string, []PlannedSecret, error) {
	if len(names) == 0 {
		return nil, nil, nil
	}
	if e.cfg.Secrets == nil {
		return nil, nil, fmt.Errorf("plan: %w: no secret store", ErrSecretChanged)
	}
	vals, err := e.cfg.Secrets.Resolve(ctx, names)
	var me *secret.MissingError
	if errors.As(err, &me) {
		return nil, nil, fmt.Errorf("plan: %w: missing %s", ErrSecretChanged, strings.Join(me.Names, ", "))
	}
	if err != nil {
		return nil, nil, err
	}
	want := map[string]int64{}
	for _, p := range planned {
		want[p.Name] = p.Version
	}
	out := make(map[string]string, len(vals))
	var used []PlannedSecret
	for _, n := range names {
		v := vals[n]
		if planned != nil {
			if w, ok := want[n]; !ok || w != v.Version {
				return nil, nil, fmt.Errorf("plan: %w: %s", ErrSecretChanged, n)
			}
		}
		out[n] = v.Text
		used = append(used, PlannedSecret{Name: n, Version: v.Version})
	}
	return out, used, nil
}

// withSecrets returns resources for a command body: the markers replaced
// by the values. The result must never be stored or logged.
func (e *Engine) withSecrets(ctx context.Context, rs []wire.Resource, planned []PlannedSecret) ([]wire.Resource, []PlannedSecret, error) {
	names := map[string]bool{}
	for _, r := range rs {
		for _, n := range state.ResourceSecrets(r) {
			names[n] = true
		}
	}
	if len(names) == 0 {
		return rs, nil, nil
	}
	list := make([]string, 0, len(names))
	for n := range names {
		list = append(list, n)
	}
	sort.Strings(list)
	vals, used, err := e.resolve(ctx, list, planned)
	if err != nil {
		return nil, nil, err
	}
	body, err := state.WithSecrets(rs, vals)
	if err != nil {
		return nil, nil, fmt.Errorf("plan: %w", ErrSecretChanged)
	}
	return body, used, nil
}

// newBinding reports whether a resource of the next render gets a secret
// that the same resource did not get in the base render (D-099).
func newBinding(before, after *state.Rendered) bool {
	if after == nil {
		return false
	}
	had := map[string]map[string]bool{}
	if before != nil {
		for _, r := range before.All() {
			had[r.ID] = map[string]bool{}
			for _, n := range state.ResourceSecrets(r) {
				had[r.ID][n] = true
			}
		}
	}
	for _, r := range after.All() {
		for _, n := range state.ResourceSecrets(r) {
			if !had[r.ID][n] {
				return true
			}
		}
	}
	return false
}

// SecretUsage is the usage hook of the secret store (D-108): the targets
// of the head revision and the pending Plans that use each secret.
func (e *Engine) SecretUsage(ctx context.Context) (map[string]secret.Usage, error) {
	out := map[string]secret.Usage{}
	add := func(name string, target string, plan int64) {
		u := out[name]
		if target != "" && !contains(u.Targets, target) {
			u.Targets = append(u.Targets, target)
		}
		if plan != 0 {
			u.Plans = append(u.Plans, plan)
		}
		out[name] = u
	}
	head, err := e.st.Head(ctx)
	if err == nil {
		set, err := state.Load(ctx, e.st, head.ID)
		if err != nil {
			return nil, err
		}
		for _, t := range set.Targets() {
			for _, r := range set.SecretRefs(t) {
				add(r.Name, t, 0)
			}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	pending, err := e.pendingPlans(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pending {
		seen := map[string]bool{}
		for _, tp := range p.targets {
			for _, s := range tp.Secrets {
				if !seen[s.Name] {
					seen[s.Name] = true
					add(s.Name, "", p.ID)
				}
			}
		}
	}
	return out, nil
}

type pendingPlan struct {
	ID      int64
	targets []TargetPlan
}

func (e *Engine) pendingPlans(ctx context.Context) ([]pendingPlan, error) {
	var out []pendingPlan
	var before int64
	for {
		page, err := e.st.ListPlans(ctx, store.PlanPending, before, 200)
		if err != nil {
			return nil, err
		}
		for _, p := range page {
			var tps []TargetPlan
			if err := json.Unmarshal(p.Changes, &tps); err != nil {
				return nil, err
			}
			out = append(out, pendingPlan{ID: p.ID, targets: tps})
			before = p.ID
		}
		if len(page) < 200 {
			return out, nil
		}
	}
}

// SecretChanged marks every pending Plan that uses the secret stale: a
// new value or a delete makes them stale (D-089). PUT and DELETE
// /secrets call it.
func (e *Engine) SecretChanged(ctx context.Context, name string) error {
	pending, err := e.pendingPlans(ctx)
	if err != nil {
		return err
	}
	for _, p := range pending {
		uses := false
		for _, tp := range p.targets {
			for _, s := range tp.Secrets {
				uses = uses || s.Name == name
			}
		}
		if !uses {
			continue
		}
		if ok, err := e.st.MarkPlanStale(ctx, p.ID); err != nil {
			return err
		} else if ok {
			e.emit(ctx, "plan.stale", fmt.Sprintf("plan:%d", p.ID), "system", nil)
			if e.cfg.OnPlanDone != nil {
				e.cfg.OnPlanDone(ctx, p.ID, false)
			}
		}
	}
	return nil
}

// checkPlannedVersions fails with ErrStale when a planned secret has
// another version now or is gone.
func (e *Engine) checkPlannedVersions(ctx context.Context, targets []TargetPlan) error {
	var names []string
	for _, tp := range targets {
		for _, s := range tp.Secrets {
			names = append(names, s.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	have, err := e.versions(ctx, names)
	if err != nil {
		return err
	}
	for _, tp := range targets {
		for _, s := range tp.Secrets {
			if have[s.Name] != s.Version {
				return fmt.Errorf("plan: secret %q has another version or is gone: %w", s.Name, ErrStale)
			}
		}
	}
	return nil
}

// plannedOrEmpty makes "no planned secrets" an empty, non-nil list, so
// resolve checks versions also then: a secret that appeared after the
// Plan is not sent.
func plannedOrEmpty(p []PlannedSecret) []PlannedSecret {
	if p == nil {
		return []PlannedSecret{}
	}
	return p
}
