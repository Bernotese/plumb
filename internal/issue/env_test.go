package issue_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/link/linktest"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// env is a whole controller in one process: store, events, link, the
// plan engine and the issue service, with driver/fake as hypervisor and
// fake agents in the guests.
type env struct {
	t      *testing.T
	ctx    context.Context
	dir    string
	st     *store.Store
	events *event.Log
	drv    *fake.Driver
	link   *link.Link
	eng    *plan.Engine
	iss    *issue.Service
	clock  *clock

	// noBootHello: the fake agent does not say hello after a cold start
	// of its guest (a real agent always does, D-061).
	noBootHello bool

	wg     sync.WaitGroup
	mu     sync.Mutex
	agents map[string]*linktest.Agent
	stops  map[string]context.CancelFunc
}

type clock struct {
	mu sync.Mutex
	d  time.Duration
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return time.Now().Add(c.d) }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.d += d
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newEnv(t *testing.T, configure ...func(*plan.Config)) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, ctx: ctx, dir: dir, st: st, events: event.New(st), drv: fake.New(), clock: &clock{},
		agents: map[string]*linktest.Agent{}, stops: map[string]context.CancelFunc{}}
	e.start(configure...)
	t.Cleanup(func() {
		cancel()
		e.wg.Wait()
		e.eng.Close()
		st.Close()
	})
	return e
}

// start builds link, engine and issue service on the store, like a
// controller start.
func (e *env) start(configure ...func(*plan.Config)) {
	e.t.Helper()
	var err error
	var eng *plan.Engine
	e.link, err = link.New(link.Config{
		Dir: filepath.Join(e.dir, "link"), Store: e.st, Events: e.events, Now: e.clock.now, Logger: quiet,
		OnReport:     func(ctx context.Context, d link.Delivery) error { return eng.HandleReport(ctx, d) },
		SecretValues: func(ctx context.Context, t string) ([]string, error) { return eng.SecretValues(ctx, t) },
		Sensitive:    func(ctx context.Context, t, id string) ([]string, bool, error) { return eng.Sensitive(ctx, t, id) },
	})
	if err != nil {
		e.t.Fatal(err)
	}
	var iss *issue.Service
	cfg := plan.Config{
		Store: e.st, Events: e.events, Agents: e.link, Driver: e.drv, Logger: quiet,
		OnDrift:        func(ctx context.Context, target string, rep wire.Report) error { return iss.Intake(ctx, target, rep) },
		OnPlanDone:     func(ctx context.Context, id int64, ok bool) { iss.PlanDone(ctx, id, ok) },
		ObserveTimeout: 5 * time.Second, EnrollTimeout: 3 * time.Second, ApplyTimeout: 5 * time.Second,
	}
	for _, f := range configure {
		f(&cfg)
	}
	eng, err = plan.New(cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	iss = issue.New(e.st, e.events, eng)
	e.eng, e.iss = eng, iss
}

// agent starts the fake agent of a target. It enrolls as soon as the
// guest runs and has a token, like the real agent at first boot.
func (e *env) agent(target string) *linktest.Agent {
	e.t.Helper()
	tok := func() (string, bool) {
		b, err := e.st.GetBinding(e.ctx, state.KindTarget, target)
		if err != nil {
			return "", false
		}
		boot, ok := e.drv.Bootstrap(driver.GuestID(b.Ref))
		return boot.Token, ok
	}
	a := linktest.New(e.link.Handler(), tok)
	ctx, cancel := context.WithCancel(e.ctx)
	e.mu.Lock()
	e.agents[target], e.stops[target] = a, cancel
	e.mu.Unlock()
	e.wg.Add(2)
	go func() {
		defer e.wg.Done()
		a.Run(ctx)
	}()
	// Like the real agent, a hello after each cold start of the guest.
	go func() {
		defer e.wg.Done()
		seen := -1
		for ctx.Err() == nil {
			if b, err := e.st.GetBinding(ctx, state.KindTarget, target); err == nil {
				n := e.drv.Boots(driver.GuestID(b.Ref))
				e.mu.Lock()
				quiet := e.noBootHello
				e.mu.Unlock()
				if seen >= 0 && n > seen && !quiet && a.Enrolls() > 0 {
					a.Report(ctx, wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "test", Hostname: "fake", OS: "fake 1"}})
				}
				seen = n
			}
			sleep(ctx, 5*time.Millisecond)
		}
	}()
	return a
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// stop stops the agent of a target.
func (e *env) stop(target string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stops[target]()
}

func doc(kind, name, spec string) state.Doc {
	return state.Doc{Kind: kind, Name: name, Spec: json.RawMessage(spec)}
}

const (
	sum   = "0000000000000000000000000000000000000000000000000000000000000000"
	image = `{"type":"vm","url":"https://images.invalid/u.img","sha256":"` + sum + `"}`
	motd  = `{"params":{"type":"object","properties":{"text":{"type":"string","default":"hello"}}},` +
		`"resources":[{"file":{"path":"/etc/motd","content":"{{ .text }}","mode":"0644"}}]}`
	web = `{"resources":[{"package":{"name":"nginx"}},{"file":{"path":"/etc/nginx/site","content":"x"}},` +
		`{"service":{"name":"nginx","restart_on":["file:/etc/nginx/site"]}}]}`
)

func target(name, text string, disk int) state.Doc {
	return doc(state.KindTarget, name, `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":`+
		itoa(disk)+`,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"`+text+`"}}]}`)
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// propose plans and fails the test on an error.
func (e *env) propose(p plan.Proposal) store.Plan {
	e.t.Helper()
	pl, err := e.eng.Propose(e.ctx, plan.Input{Proposal: p, Reason: "test", Author: "user:admin"})
	if err != nil {
		e.t.Fatalf("propose: %v", err)
	}
	return pl
}

// approve approves a Plan and waits for its Run.
func (e *env) approve(id int64) store.Run {
	e.t.Helper()
	run, err := e.eng.Approve(e.ctx, id, "user:admin", nil)
	if err != nil {
		e.t.Fatalf("approve %d: %v", id, err)
	}
	e.eng.Wait()
	run, err = e.st.GetRun(e.ctx, run.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return run
}

// changes decodes the changes of a Plan.
func changes(t *testing.T, p store.Plan) map[string]plan.TargetPlan {
	t.Helper()
	var list []plan.TargetPlan
	if err := json.Unmarshal(p.Changes, &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]plan.TargetPlan{}
	for _, tp := range list {
		out[tp.Target] = tp
	}
	return out
}

// eventually waits until fn is true.
func eventually(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// openIssue waits for the open Issue of a resource.
func (e *env) openIssue(target, resource string) store.Issue {
	e.t.Helper()
	var iss store.Issue
	eventually(e.t, "issue for "+resource, func() bool {
		var err error
		iss, err = e.st.OpenIssue(e.ctx, target, resource)
		return err == nil
	})
	return iss
}

// online waits until the target's agent is online.
func (e *env) online(target string) {
	e.t.Helper()
	eventually(e.t, target+" online", func() bool { return e.link.Online(target) })
}

// seed creates the image, the Blocks and the targets, and applies them.
func (e *env) seed(targets ...string) {
	e.t.Helper()
	put := []state.Doc{doc(state.KindImage, "ubuntu", image), doc(state.KindBlock, "motd", motd), doc(state.KindBlock, "web", web)}
	for _, t := range targets {
		put = append(put, target(t, "hello "+t, 10))
		e.agent(t)
	}
	run := e.approve(e.propose(plan.Proposal{Put: put}).ID)
	if run.Status != "succeeded" {
		steps, _ := e.st.Steps(e.ctx, run.ID)
		e.t.Fatalf("seed run %s: %+v", run.Status, steps)
	}
	for _, t := range targets {
		e.online(t)
	}
}
