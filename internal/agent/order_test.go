package agent

// Tests of after edges and the skip rule of an apply (T-048, D-170).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// svcRes is a service resource. restartOn may be nil.
func svcRes(block, name string, after []string, restartOn ...any) wire.Resource {
	attrs := map[string]any{"name": name}
	if restartOn != nil {
		attrs["restart_on"] = restartOn
	}
	return wire.Resource{ID: "service:" + name, Type: "service", Block: block, Attrs: attrs, After: after}
}

// withAfter returns r with After set.
func withAfter(r wire.Resource, after ...string) wire.Resource {
	r.After = after
	return r
}

// units runs fn for the first systemctl call of each named service; later
// calls (the drift check after the apply) answer at once. A service that is
// not in fns answers at once.
func units(fns map[string]func(ctx context.Context, verb string) error) func(context.Context, string, string) error {
	var mu sync.Mutex
	seen := map[string]bool{}
	return func(ctx context.Context, verb, unit string) error {
		name := strings.TrimSuffix(unit, ".service")
		mu.Lock()
		first := !seen[name]
		seen[name] = true
		mu.Unlock()
		if fn := fns[name]; fn != nil && first {
			return fn(ctx, verb)
		}
		return nil
	}
}

// applyNow runs one apply on the agent and checks N1: every skipped item
// names its cause.
func applyNow(t *testing.T, ctx context.Context, ta *testAgent, list []wire.Resource, max wire.Risk) []wire.Item {
	t.Helper()
	items, err := ta.a.apply(ctx, "c1", wire.ApplyBody{Resources: list, Planned: unverified(list...), MaxRisk: max})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	for i, it := range items {
		if it.Resource != list[i].ID {
			t.Fatalf("item %d is %s, want %s", i, it.Resource, list[i].ID)
		}
		if it.Status == wire.StatusSkipped && !strings.HasPrefix(it.Output, "not applied: ") {
			t.Errorf("skipped %s without a cause: %q", it.Resource, it.Output)
		}
	}
	return items
}

// want checks the status and output of items, by resource id.
func want(t *testing.T, items []wire.Item, exp map[string][2]string) {
	t.Helper()
	got := map[string]wire.Item{}
	for _, it := range items {
		got[it.Resource] = it
	}
	for id, e := range exp {
		it := got[id]
		if string(it.Status) != e[0] || (e[1] != "" && it.Output != e[1]) {
			t.Errorf("%s = %s %q, want %s %q", id, it.Status, it.Output, e[0], e[1])
		}
	}
}

// reversed returns the list in the other order (the order of uses must
// not matter).
func reversed(list []wire.Resource) []wire.Resource {
	out := make([]wire.Resource, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		out = append(out, list[i])
	}
	return out
}

// bothOrders runs fn with the list and with the uses in reverse order.
// Resources of one use keep their order.
func bothOrders(t *testing.T, list []wire.Resource, fn func(t *testing.T, list []wire.Resource)) {
	var uses []string
	byUse := map[string][]wire.Resource{}
	for _, r := range list {
		if byUse[r.Block] == nil {
			uses = append(uses, r.Block)
		}
		byUse[r.Block] = append(byUse[r.Block], r)
	}
	var back []wire.Resource
	for i := len(uses) - 1; i >= 0; i-- {
		back = append(back, byUse[uses[i]]...)
	}
	t.Run("listed", func(t *testing.T) { fn(t, list) })
	t.Run("reversed", func(t *testing.T) { fn(t, back) })
}

// A step with after starts only when its dependency ended.
func TestApplyAfterWaits(t *testing.T) {
	bothOrders(t, []wire.Resource{
		svcRes("pkg", "a", nil),
		svcRes("site", "b", []string{"service:a"}),
	}, func(t *testing.T, list []wire.Resource) {
		ca := newCA(t)
		ctl := newController(t, ca, "tok-1")
		ta := startAgent(t, ctl, ca, "tok-1")
		var mu sync.Mutex
		aDone, bSawA := false, false
		ta.runner.onUnit = units(map[string]func(context.Context, string) error{
			"a": func(context.Context, string) error {
				time.Sleep(50 * time.Millisecond)
				mu.Lock()
				aDone = true
				mu.Unlock()
				return nil
			},
			"b": func(context.Context, string) error {
				mu.Lock()
				bSawA = aDone
				mu.Unlock()
				return nil
			},
		})
		items := applyNow(t, context.Background(), ta, list, wire.RiskDisruptive)
		want(t, items, map[string][2]string{"service:a": {"unchanged", ""}, "service:b": {"unchanged", ""}})
		if !bSawA {
			t.Error("service:b was observed before service:a ended")
		}
	})
}

// A use with no relation to the dependency runs while the dependency blocks.
func TestApplyUnrelatedUseRunsWhileDependencyBlocks(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	release := make(chan struct{})
	ta.runner.onUnit = units(map[string]func(context.Context, string) error{
		"a": func(context.Context, string) error { <-release; return nil },
	})
	list := []wire.Resource{
		svcRes("pkg", "a", nil),
		withAfter(fileRes("site", "/srv/site", map[string]any{"content": "s"}), "service:a"),
		fileRes("other", "/srv/other", map[string]any{"content": "o"}),
	}
	done := make(chan []wire.Item)
	go func() { done <- applyNow(t, context.Background(), ta, list, wire.RiskDisruptive) }()
	waitFor(t, "the unrelated use", func() bool {
		_, err := os.Stat(filepath.Join(ta.root, "srv/other"))
		return err == nil
	})
	if _, err := os.Stat(filepath.Join(ta.root, "srv/site")); err == nil {
		t.Error("the dependent ran while its dependency blocked")
	}
	close(release)
	want(t, <-done, map[string][2]string{"service:a": {"unchanged", ""}, "file:/srv/site": {"applied", ""}, "file:/srv/other": {"applied", ""}})
}

// A dependency that failed, was refused or was skipped skips the whole
// dependent use, with the cause.
func TestApplyAfterDependencyNotOK(t *testing.T) {
	failA := func(context.Context, string) error { return errors.New("unit is broken") }
	tests := []struct {
		name string
		dep  []wire.Resource // the use "dep"; its last resource is the dependency
		max  wire.Risk
		st   string
	}{
		{"failed", []wire.Resource{svcRes("dep", "a", nil)}, wire.RiskDisruptive, "failed"},
		// A delete is destructive: refused above disruptive.
		{"refused", []wire.Resource{fileRes("dep", "/srv/old", map[string]any{"state": "absent"})}, wire.RiskDisruptive, "refused"},
		{"skipped", []wire.Resource{svcRes("dep", "a", nil), fileRes("dep", "/srv/x", map[string]any{"content": "x"})}, wire.RiskDisruptive, "skipped"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			depID := tt.dep[len(tt.dep)-1].ID
			list := append(append([]wire.Resource{}, tt.dep...),
				withAfter(fileRes("site", "/srv/d1", map[string]any{"content": "1"}), depID),
				fileRes("site", "/srv/d2", map[string]any{"content": "2"}),
			)
			bothOrders(t, list, func(t *testing.T, list []wire.Resource) {
				ca := newCA(t)
				ctl := newController(t, ca, "tok-1")
				ta := startAgent(t, ctl, ca, "tok-1")
				write(t, filepath.Join(ta.root, "srv/old"), "old")
				ta.runner.onUnit = units(map[string]func(context.Context, string) error{"a": failA})
				items := applyNow(t, context.Background(), ta, list, tt.max)
				want(t, items, map[string][2]string{
					depID:           {tt.st, ""},
					"file:/srv/d1": {"skipped", "not applied: " + depID + " was " + tt.st},
					"file:/srv/d2": {"skipped", "not applied: file:/srv/d1 was skipped"},
				})
				for _, p := range []string{"srv/d1", "srv/d2"} {
					if _, err := os.Stat(filepath.Join(ta.root, p)); err == nil {
						t.Errorf("%s was written", p)
					}
				}
			})
		})
	}
}

// A skip passes down a chain of three uses; N1: a skip after a failure in
// the own use names that resource.
func TestApplyAfterChain(t *testing.T) {
	bothOrders(t, []wire.Resource{
		svcRes("u1", "a", nil),
		fileRes("u1", "/srv/u1", map[string]any{"content": "1"}),
		withAfter(fileRes("u2", "/srv/u2", map[string]any{"content": "2"}), "file:/srv/u1"),
		withAfter(fileRes("u3", "/srv/u3", map[string]any{"content": "3"}), "file:/srv/u2"),
		fileRes("free", "/srv/free", map[string]any{"content": "f"}),
	}, func(t *testing.T, list []wire.Resource) {
		ca := newCA(t)
		ctl := newController(t, ca, "tok-1")
		ta := startAgent(t, ctl, ca, "tok-1")
		ta.runner.onUnit = units(map[string]func(context.Context, string) error{
			"a": func(context.Context, string) error { return errors.New("broken") },
		})
		items := applyNow(t, context.Background(), ta, list, wire.RiskDisruptive)
		want(t, items, map[string][2]string{
			"service:a":       {"failed", ""},
			"file:/srv/u1":    {"skipped", "not applied: service:a was failed"},
			"file:/srv/u2":    {"skipped", "not applied: file:/srv/u1 was skipped"},
			"file:/srv/u3":    {"skipped", "not applied: file:/srv/u2 was skipped"},
			"file:/srv/free": {"applied", ""},
		})
	})
}

// restart_on keeps its rule: the service waits and still runs when a
// listed resource was skipped.
func TestApplyRestartOnSkippedStillRuns(t *testing.T) {
	bothOrders(t, []wire.Resource{
		svcRes("site", "a", nil),
		fileRes("site", "/srv/conf", map[string]any{"content": "c"}),
		svcRes("web", "web", nil, "file:/srv/conf"),
	}, func(t *testing.T, list []wire.Resource) {
		ca := newCA(t)
		ctl := newController(t, ca, "tok-1")
		ta := startAgent(t, ctl, ca, "tok-1")
		ta.runner.onUnit = units(map[string]func(context.Context, string) error{
			"a": func(context.Context, string) error { return errors.New("broken") },
		})
		items := applyNow(t, context.Background(), ta, list, wire.RiskDisruptive)
		want(t, items, map[string][2]string{
			"file:/srv/conf": {"skipped", "not applied: service:a was failed"},
			"service:web":    {"unchanged", ""},
		})
		if n := ta.runner.count("systemctl", "restart"); n != 0 {
			t.Errorf("restarts = %d", n)
		}
	})
}

// S1: after is order only. A service that is the first resource of its use
// does not restart because its after resource changed.
func TestApplyAfterDoesNotRestart(t *testing.T) {
	bothOrders(t, []wire.Resource{
		fileRes("pkg", "/srv/pkg", map[string]any{"content": "installed"}),
		svcRes("site", "web", []string{"file:/srv/pkg"}),
		fileRes("site", "/srv/site", map[string]any{"content": "s"}),
	}, func(t *testing.T, list []wire.Resource) {
		ca := newCA(t)
		ctl := newController(t, ca, "tok-1")
		ta := startAgent(t, ctl, ca, "tok-1")
		sawPkg := false
		ta.runner.onUnit = units(map[string]func(context.Context, string) error{
			"web": func(context.Context, string) error {
				_, err := os.Stat(filepath.Join(ta.root, "srv/pkg"))
				sawPkg = err == nil
				return nil
			},
		})
		items := applyNow(t, context.Background(), ta, list, wire.RiskDisruptive)
		want(t, items, map[string][2]string{
			"file:/srv/pkg":  {"applied", ""},
			"service:web":    {"unchanged", ""},
			"file:/srv/site": {"applied", ""},
		})
		if !sawPkg {
			t.Error("service:web was observed before its after resource ended")
		}
		if n := ta.runner.count("systemctl", "restart"); n != 0 {
			t.Errorf("an after resource restarted the service: %d restarts", n)
		}
	})
}

// S5: the context ends while a step waits. The step and the rest of its use
// are skipped with the cause, and apply returns. apply waits for every
// goroutine it starts, so its return means that none is left.
func TestApplyContextEndsDuringWait(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	blocking := make(chan struct{})
	ta.runner.onUnit = units(map[string]func(context.Context, string) error{
		"a": func(ctx context.Context, _ string) error {
			close(blocking)
			<-ctx.Done()
			// End a little after ctx, so the waiting step sees ctx first.
			time.Sleep(200 * time.Millisecond)
			return ctx.Err()
		},
	})
	list := []wire.Resource{
		svcRes("pkg", "a", nil),
		withAfter(fileRes("site", "/srv/d", map[string]any{"content": "d"}), "service:a"),
		fileRes("site", "/srv/e", map[string]any{"content": "e"}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan []wire.Item)
	go func() { done <- applyNow(t, ctx, ta, list, wire.RiskDisruptive) }()
	<-blocking
	cancel()
	select {
	case items := <-done:
		want(t, items, map[string][2]string{
			"service:a":   {"failed", ""},
			"file:/srv/d": {"skipped", "not applied: the apply ended while this resource waited for service:a"},
			"file:/srv/e": {"skipped", "not applied: file:/srv/d was skipped"},
		})
	case <-time.After(5 * time.Second):
		t.Fatal("apply did not return after the context ended")
	}
}

// plan takes its order from wire.Order and keeps the two kinds apart.
func TestPlanEdgeKinds(t *testing.T) {
	list := []wire.Resource{
		fileRes("a", "/x", nil),
		fileRes("b", "/y", nil),
		svcRes("c", "s", []string{"file:/x"}, "file:/y"),
	}
	steps, blocks, err := plan(list)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d", len(blocks))
	}
	s := steps[2]
	if len(s.needs) != 1 || s.needs[0] != steps[0] || len(s.restartDeps) != 1 || s.restartDeps[0] != steps[1] {
		t.Fatalf("needs %v, restartDeps %v", s.needs, s.restartDeps)
	}
	// Both lists in reverse order give the same edges.
	steps, _, err = plan(reversed(list))
	if err != nil {
		t.Fatal(err)
	}
	if s := steps[0]; len(s.needs) != 1 || s.needs[0].r.ID != "file:/x" || len(s.restartDeps) != 1 || s.restartDeps[0].r.ID != "file:/y" {
		t.Fatalf("reversed: needs %v, restartDeps %v", s.needs, s.restartDeps)
	}
}
