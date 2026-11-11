// Package plan is the one change flow of Plumb (ARCHITECTURE §4, D-022):
// proposal → Plan → approval → Revision + Run. It also routes agent
// reports, backs the redaction lists of link with the head revision
// (D-062) and polls the Proxmox level for drift (§6).
package plan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Origins of a Plan (the plan.origin column).
const (
	OriginProposal = "proposal"
	OriginRevert   = "revert"
	OriginAccept   = "accept"
	OriginReenroll = "reenroll"
	// Slice 2 (migration plan_origin, D-096).
	OriginRollback   = "rollback"
	OriginImport     = "import"
	OriginIgnoreRule = "ignore_rule"
	// Image builds (migration image_version, D-199): a manual or scheduled
	// build with no document change. Used from T-062.
	OriginBuild = "build"
)

// DocKey names a document.
type DocKey struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Proposal is what a client wants to change. It is stored with the Plan,
// so approval rebuilds exactly the planned document set.
type Proposal struct {
	Put    []state.Doc `json:"put,omitempty"`
	Delete []DocKey    `json:"delete,omitempty"`
	// Targets are planned even when their documents do not change
	// (revert of drift).
	Targets []string `json:"targets,omitempty"`
	// Reenroll lists targets that get a new enrollment token and a
	// restart (D-038). Their agents may be offline.
	Reenroll []string `json:"reenroll,omitempty"`
	// Notes are warnings of the Plan that come from its origin, for
	// example the secrets that an import wrote (D-095). They stay with the
	// stored proposal, also when the Plan is dropped.
	Notes []string `json:"notes,omitempty"`
}

// Input is a request to plan.
type Input struct {
	Proposal Proposal
	Reason   string
	Author   string // principal
	Origin   string // default OriginProposal
	IssueID  int64
	// RollbackTo is the revision of a rollback (D-131). Only Rollback sets it.
	RollbackTo int64
	// Imported names the secrets that an import writes (D-153). They
	// count as present, a resource that uses one is not observed (as
	// D-156), and BeforeStore writes them after every check, just before
	// the Plan is stored. Its notes become Plan warnings.
	Imported    []string
	BeforeStore func(ctx context.Context) ([]string, error)
	// Base, when not 0, is the head revision that the proposal was built
	// against. Propose fails with store.ErrPlanStale when the head moved.
	Base int64
}

// TargetPlan is the planned work on one target. The Plan stores a list
// of these as its changes (migration 0001: [{target, resources}]).
type TargetPlan struct {
	Target string `json:"target"`
	// Guest is what happens to the guest: "create", "update", "delete",
	// "reenroll" or "" (no guest change).
	Guest     string           `json:"guest,omitempty"`
	Resources []ResourceChange `json:"resources"`
	// Secrets are the secrets of the desired state of this target, with
	// the versions that the Plan was made with (D-089).
	Secrets []PlannedSecret `json:"secrets,omitempty"`
}

// ResourceChange is the planned change of one resource.
type ResourceChange struct {
	Resource string        `json:"resource"`
	Action   wire.Action   `json:"action,omitempty"`
	Changes  []wire.Change `json:"changes,omitempty"`
	Risk     wire.Risk     `json:"risk"`
	// Unverified marks a resource of a new target: the guest does not
	// exist yet, so nobody could observe it. It is verified at the first
	// apply after the first boot.
	Unverified bool `json:"unverified,omitempty"`
	// After are the resource ids that this resource comes after in the
	// apply (wire.Resource.After, D-171). For display.
	After []string `json:"after,omitempty"`
	// RestartBy names, for a service with a planned restart, the resources
	// of its restart_on list that cause it: they change in this Plan or
	// nobody observed them (D-171). For display; the apply does not use it.
	RestartBy []string `json:"restart_by,omitempty"`
}

// risk is the highest risk of a target plan.
func (t TargetPlan) risk() wire.Risk {
	r := wire.RiskRead
	for _, c := range t.Resources {
		if c.Risk.Exceeds(r) {
			r = c.Risk
		}
	}
	return r
}

// Agents is what the engine needs from link. *link.Link implements it.
type Agents interface {
	Send(target string, typ wire.CommandType, body any) (string, error)
	Online(target string) bool
	NewEnrollToken(ctx context.Context, target string) (string, time.Time, error)
	// Revoke removes the agent and the enrollment token of a target.
	Revoke(ctx context.Context, target string) error
}

// Config configures an Engine.
type Config struct {
	Store  *store.Store
	Events *event.Log
	Agents Agents
	Driver driver.Driver
	// Secrets is the secret store. nil: no secret exists, and a target
	// that refers to one cannot be planned.
	Secrets *secret.Store
	// OnDrift receives drift reports of agents and of the guest poll.
	// The issue package handles them.
	OnDrift func(ctx context.Context, target string, rep wire.Report) error
	// OnPlanDone is called when the Run of a Plan ended (ok = succeeded)
	// or a pending Plan went stale (ok = false).
	OnPlanDone func(ctx context.Context, planID int64, ok bool)
	// Timeouts. Defaults: observe 60 s, enroll 10 min, apply 15 min.
	ObserveTimeout time.Duration
	EnrollTimeout  time.Duration
	ApplyTimeout   time.Duration
	Logger         *slog.Logger
	// Now returns the time for the address list and its check (D-212,
	// D-215). Default time.Now.
	Now func() time.Time
	// AddrStable is the stable time of the address check; default
	// AddrStable. Only the dev demo shortens it.
	AddrStable time.Duration
	// Timeouts of an image build (T-039 Plan §2). Defaults: enroll 20 min,
	// seal 5 min (the apply uses ApplyTimeout).
	BuildEnrollTimeout time.Duration
	SealTimeout        time.Duration
	// PowerPoll is how often a build lane reads the power state after
	// seal; default 2 s.
	PowerPoll time.Duration
	// DiskLimitPercent is the limit of the disk gate for creates and
	// builds (D-219); default DefaultDiskLimitPercent.
	DiskLimitPercent int
}

// Engine runs the change flow.
type Engine struct {
	cfg Config
	st  *store.Store
	log *slog.Logger

	ctx    context.Context // runs live as long as the engine
	cancel context.CancelFunc
	runs   sync.WaitGroup

	mu      sync.Mutex
	results map[string]chan wire.Report // command id → reports; only waiters create them
	busy    map[string]int              // targets with a running or starting Run
	hello   map[string]int              // hellos per target since start (a new boot)
	active  map[int64]bool              // Runs of this process
	cache   *sensitiveCache
	warned  map[string]bool       // "revision/target": the cycle warning is in the log
	addrs   map[string]*addrWatch // target → state of the address check (D-215)
	disk    *DiskGate             // creates and builds (T-074)
	// versions in use (review S1 of #133): a create pins the version it
	// clones from the pick to the end of the clone; retention marks the
	// version it removes. Each side skips what the other holds.
	pinned   map[imageKey]int
	removing map[imageKey]bool
}

// Disk is the disk gate of the engine. Image builds (T-062) reserve their
// disk with it, so builds and creates count together.
func (e *Engine) Disk() *DiskGate { return e.disk }

// load reads a stored revision. A Target whose apply order has a cycle
// loads (D-168); the log gets one warning per revision and Target.
func (e *Engine) load(ctx context.Context, revision int64) (*state.Set, error) {
	set, err := state.Load(ctx, e.st, revision)
	if err != nil {
		return nil, err
	}
	cycles := set.Cycles()
	targets := make([]string, 0, len(cycles))
	for t := range cycles {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	for _, t := range targets {
		key := fmt.Sprintf("%d/%s", revision, t)
		e.mu.Lock()
		seen := e.warned[key]
		if e.warned == nil {
			e.warned = map[string]bool{}
		}
		e.warned[key] = true
		e.mu.Unlock()
		if !seen {
			e.log.Warn("the apply order of a Target has a cycle: its applies fail, and the next proposal must repair it",
				"revision", revision, "target", t, "cycle", cycles[t])
		}
	}
	return set, nil
}

// New returns an Engine. Close stops its Runs.
func New(cfg Config) (*Engine, error) {
	if cfg.Store == nil || cfg.Agents == nil || cfg.Driver == nil {
		return nil, errors.New("plan: Store, Agents and Driver are required")
	}
	if cfg.ObserveTimeout == 0 {
		cfg.ObserveTimeout = time.Minute
	}
	if cfg.EnrollTimeout == 0 {
		cfg.EnrollTimeout = 10 * time.Minute
	}
	if cfg.ApplyTimeout == 0 {
		cfg.ApplyTimeout = 15 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.BuildEnrollTimeout == 0 {
		cfg.BuildEnrollTimeout = DefaultBuildEnrollTimeout
	}
	if cfg.SealTimeout == 0 {
		cfg.SealTimeout = DefaultSealTimeout
	}
	if cfg.PowerPoll == 0 {
		cfg.PowerPoll = 2 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.AddrStable <= 0 {
		cfg.AddrStable = AddrStable
	}
	if cfg.DiskLimitPercent < 0 || cfg.DiskLimitPercent > 100 {
		return nil, fmt.Errorf("plan: disk limit %d %% is outside 1 … 100", cfg.DiskLimitPercent)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{cfg: cfg, st: cfg.Store, log: cfg.Logger, ctx: ctx, cancel: cancel,
		results: map[string]chan wire.Report{}, busy: map[string]int{}, hello: map[string]int{}, active: map[int64]bool{},
		addrs: map[string]*addrWatch{}, pinned: map[imageKey]int{}, removing: map[imageKey]bool{}, disk: NewDiskGate(cfg.Driver, cfg.DiskLimitPercent)}, nil
}

// Close cancels all Runs and waits for them.
func (e *Engine) Close() {
	e.cancel()
	e.runs.Wait()
}

// Hellos returns how many Hellos the agent of a target sent since the
// engine started. A Run uses the count to see a restarted agent. For tests.
func (e *Engine) Hellos(target string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hello[target]
}

// Wait waits until no Run is active. For tests.
func (e *Engine) Wait() { e.runs.Wait() }

func (e *Engine) emit(ctx context.Context, typ, subject, actor string, data any) {
	if e.cfg.Events == nil {
		return
	}
	if _, err := e.cfg.Events.Emit(ctx, typ, subject, actor, data); err != nil {
		e.log.Error("event", "type", typ, "err", err)
	}
}

// --- reports ---

// HandleReport is the link.Config.OnReport of the controller. link has
// validated and redacted the report. Results and progress go to the
// waiting Run or Plan; drift goes to OnDrift; a hello makes the engine
// send the watch list again (D-061: after a restart of either side).
func (e *Engine) HandleReport(ctx context.Context, d link.Delivery) error {
	switch d.Report.Type {
	case wire.ReportResult, wire.ReportProgress:
		e.mu.Lock()
		ch, ok := e.results[d.Report.CommandID]
		e.mu.Unlock()
		if ok {
			select {
			case ch <- d.Report:
			default: // the waiter does not read; drop it
			}
		}
		return nil // a report without a waiter (it timed out) is dropped
	case wire.ReportDrift:
		if bound, err := e.bound(ctx, d.Target); err != nil || !bound {
			return err // no Target: no Issue, no event (D-211)
		}
		if e.cfg.OnDrift != nil {
			return e.cfg.OnDrift(ctx, d.Target, d.Report)
		}
		return nil
	case wire.ReportHello:
		// A Run that works on the target sends the watch list when it is
		// done; a watch before the first apply would report every resource
		// of a new guest as drift.
		e.mu.Lock()
		e.hello[d.Target]++
		busy := e.busy[d.Target] > 0
		e.mu.Unlock()
		if bound, err := e.bound(ctx, d.Target); err != nil || !bound {
			return err // no guest: no watch list, so no secret value, and no address list (D-211, D-214)
		}
		if err := e.storeAddrs(ctx, d); err != nil {
			return err
		}
		if busy {
			return nil
		}
		return e.sendWatch(ctx, d.Target)
	case wire.ReportAddrs:
		// Not a hello: the Hello count shows a restarted agent (D-212).
		if bound, err := e.bound(ctx, d.Target); err != nil || !bound {
			return err
		}
		return e.storeAddrs(ctx, d)
	}
	return nil
}

// bound reports whether a target has a guest binding. Agent reports of a
// target without one are dropped (D-211).
func (e *Engine) bound(ctx context.Context, target string) (bool, error) {
	if strings.HasPrefix(target, wire.BuildLinkPrefix) {
		return false, nil // a build guest: no watch, no drift, no address list (D-203)
	}
	_, err := e.st.GetBinding(ctx, state.KindTarget, target)
	if errors.Is(err, store.ErrNotFound) {
		e.log.Warn("agent report for a target without a guest dropped", "target", target)
		return false, nil
	}
	return err == nil, err
}

func (e *Engine) dropChan(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.results, id)
}

// ErrTimeout is returned when an agent did not answer in time.
var ErrTimeout = errors.New("plan: the agent did not answer in time")

// command sends a command and waits for its result. progress sees every
// progress report. A lost command (D-061) ends in ErrTimeout.
func (e *Engine) command(ctx context.Context, target string, typ wire.CommandType, body any,
	timeout time.Duration, progress func(wire.Report)) (wire.Report, error) {
	// Send and register under one lock: HandleReport takes the same lock,
	// so a fast result cannot arrive before its channel exists.
	ch := make(chan wire.Report, 1024)
	e.mu.Lock()
	id, err := e.cfg.Agents.Send(target, typ, body)
	if err == nil {
		e.results[id] = ch
	}
	e.mu.Unlock()
	if err != nil {
		return wire.Report{}, err
	}
	defer e.dropChan(id)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case rep := <-ch:
			if rep.Type == wire.ReportProgress {
				if progress != nil {
					progress(rep)
				}
				continue
			}
			if rep.Error != "" {
				return rep, fmt.Errorf("plan: agent of %s: %s", target, rep.Error)
			}
			return rep, nil
		case <-timer.C:
			return wire.Report{}, fmt.Errorf("%w (%s, %v)", ErrTimeout, target, timeout)
		case <-ctx.Done():
			return wire.Report{}, ctx.Err()
		}
	}
}

// sendWatch sends the full desired list of the head revision to a target.
func (e *Engine) sendWatch(ctx context.Context, target string) error {
	r, err := e.headRender(ctx, target)
	if err != nil || r == nil {
		return err
	}
	return e.watch(ctx, target, r)
}

// watch sends the desired list of a render with the current secret
// values. When a secret is missing it sends nothing: a marker or an empty
// value never reaches a guest (D-089).
func (e *Engine) watch(ctx context.Context, target string, r *state.Rendered) error {
	body, _, err := e.withSecrets(ctx, r.Resources, nil)
	if err != nil {
		return fmt.Errorf("plan: watch list of %s not sent: %w", target, err)
	}
	_, err = e.cfg.Agents.Send(target, wire.CommandWatch, wire.WatchBody{Resources: body})
	return err
}

// --- head revision cache (D-062) ---

// sensitiveCache holds the renders of one head revision. A new head
// replaces it; rendering is costly (D-052), so it is never done per report.
type sensitiveCache struct {
	rev     int64
	set     *state.Set
	renders map[string]*state.Rendered
}

// headRender returns the render of a target in the head revision, or nil
// when there is no revision or no such target.
func (e *Engine) headRender(ctx context.Context, target string) (*state.Rendered, error) {
	head, err := e.st.Head(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	c := e.cache
	e.mu.Unlock()
	if c == nil || c.rev != head.ID {
		set, err := e.load(ctx, head.ID)
		if err != nil {
			return nil, err
		}
		c = &sensitiveCache{rev: head.ID, set: set, renders: map[string]*state.Rendered{}}
		e.mu.Lock()
		e.cache = c
		e.mu.Unlock()
	}
	e.mu.Lock()
	r, ok := c.renders[target]
	e.mu.Unlock()
	if ok {
		return r, nil
	}
	if !contains(c.set.Targets(), target) {
		return nil, nil
	}
	r, err = c.set.Render(target)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	c.renders[target] = r
	e.mu.Unlock()
	return r, nil
}

// Sensitive is the link.Config.Sensitive of the controller: the sensitive
// attributes of a resource in the head revision.
func (e *Engine) Sensitive(ctx context.Context, target, resourceID string) ([]string, bool, error) {
	r, err := e.headRender(ctx, target)
	if err != nil || r == nil {
		return nil, false, err
	}
	for _, res := range r.All() {
		if res.ID == resourceID {
			return res.Sensitive, true, nil
		}
	}
	return nil, false, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
