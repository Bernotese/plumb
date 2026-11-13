package plan

// Agent update and version gate (T-092; D-257 … D-259, D-266 … D-268,
// D-275 … D-278, ARCHITECTURE §14).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// OriginAgentUpdate replaces the agent binary of one Target (D-267).
const OriginAgentUpdate = "agent_update"

// AgentBuild is the agent that this controller serves at wire.PathBinary.
type AgentBuild struct {
	SHA256       string
	Version      string
	Capabilities wire.Capabilities
}

// The values of TargetStatus.agent_update.
const (
	AgentUpdateNone      = "none"
	AgentUpdateAvailable = "available"
	AgentUpdateRequired  = "required"
	AgentUpdateManual    = "manual"
	AgentUpdateUnknown   = "unknown"
)

// AgentState is the agent part of the Target status.
type AgentState struct {
	Version  string // "" before the first hello
	Update   string
	Missing  []string
	Reverted *wire.UpdateRevert
	HelloAt  time.Time // of the last hello
}

// ErrAgentOutdated: a Plan needs what an agent lacks (D-259, D-268).
var ErrAgentOutdated = errors.New(wire.GateMessage)

// AgentOutdatedError names the Targets and what they lack. The API
// answers it with 409 agent-outdated.
type AgentOutdatedError struct {
	Targets []string
	Missing []string            // over all Targets, sorted
	By      map[string][]string // target → missing
}

func (e *AgentOutdatedError) Error() string {
	parts := make([]string, 0, len(e.Targets))
	for _, t := range e.Targets {
		parts = append(parts, fmt.Sprintf("target %s needs %s", t, strings.Join(e.By[t], ", ")))
	}
	return wire.GateMessage + ": " + strings.Join(parts, "; ")
}

func (e *AgentOutdatedError) Unwrap() error { return ErrAgentOutdated }

// ErrAgentUpdate is the base of the refusals of ProposeAgentUpdate.
var (
	// ErrAgentNoUpdate: the agent runs the served binary, or an update
	// Plan is pending (409 nothing-to-do, D-277).
	ErrAgentNoUpdate = errors.New("nothing to do")
	// ErrAgentCannotUpdate: no hello yet, or an agent that cannot update
	// itself (422).
	ErrAgentCannotUpdate = errors.New("the agent cannot be updated by Plumb")
)

// --- hello ---

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// asciiText keeps printable ASCII up to max bytes; a longer text is cut
// and ends in "..." within max (review of T-090).
func asciiText(s string, max int) string {
	b := []byte(s)
	for i, c := range b {
		if c < 0x20 || c > 0x7e {
			b[i] = '?'
		}
	}
	if len(b) > max {
		return string(b[:max-3]) + "..."
	}
	return string(b)
}

// checkedHello applies the guest-text rules of ARCHITECTURE §14 to a
// hello before anything stores or shows it.
func checkedHello(h wire.Hello, now time.Time) (store.AgentHello, *wire.Capabilities, *wire.UpdateRevert) {
	out := store.AgentHello{Version: asciiText(h.Version, wire.MaxRevertVersion), At: now}
	if hex64.MatchString(h.BinarySHA256) {
		out.BinarySHA256 = h.BinarySHA256
	}
	var caps *wire.Capabilities
	if h.Capabilities != nil && h.Capabilities.Valid() {
		c := normalCaps(*h.Capabilities)
		caps = &c
		out.Capabilities, _ = json.Marshal(c)
	}
	var rev *wire.UpdateRevert
	if r := h.Reverted; r != nil && (r.Reason == wire.RevertCrashLoop || r.Reason == wire.RevertNoHello) {
		rev = &wire.UpdateRevert{From: asciiText(r.From, wire.MaxRevertVersion), To: asciiText(r.To, wire.MaxRevertVersion), Reason: r.Reason}
		if hex64.MatchString(r.ToSHA256) {
			rev.ToSHA256 = r.ToSHA256
		}
		if at, err := time.Parse(time.RFC3339, r.At); err == nil {
			rev.At = at.UTC().Format(time.RFC3339)
		}
		out.Reverted, _ = json.Marshal(rev)
	}
	return out, caps, rev
}

// normalCaps sorts a capability list, so two lists compare with DeepEqual.
func normalCaps(c wire.Capabilities) wire.Capabilities {
	out := wire.Capabilities{Commands: slices.Clone(c.Commands), Resources: map[string][]string{}}
	slices.Sort(out.Commands)
	for t, a := range c.Resources {
		a = slices.Clone(a)
		sort.Strings(a)
		out.Resources[t] = a
	}
	return out
}

// agentHello is a checked hello for the waiters of an update step.
// afterResult: the hello came after a result of the target, so it is not
// a reconnect of the old agent before the swap. taken: the agent had
// taken the update command when the hello came (T-102). The agent drops
// its reverted marker when it starts the command and sends its reports
// one after the other, so a reverted hello with taken set is of this
// update, not one left from an earlier update.
type agentHello struct {
	sha         string
	reverted    *wire.UpdateRevert
	afterResult bool
	taken       bool
}

// helloWaiter is one update step that waits for hellos.
type helloWaiter struct {
	ch          chan agentHello
	afterResult bool   // a result of the target came in (under e.mu)
	cmdID       string // the update command, once sent (under e.mu)
}

// recordHello stores the checked hello of a bound Target, wakes an update
// step that waits for it, and lets system propose an update (D-257).
func (e *Engine) recordHello(ctx context.Context, d link.Delivery) error {
	if d.Report.Hello == nil {
		return nil
	}
	h, caps, rev := checkedHello(*d.Report.Hello, e.cfg.Now())
	if err := e.st.SetAgentHello(ctx, d.AgentID, h); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil // revoked or enrolled again meanwhile
		}
		return err
	}
	e.mu.Lock()
	for _, w := range e.helloWait[d.Target] {
		// Lock order e.mu → link (as in sendCommand).
		taken := w.cmdID != "" && e.cfg.Agents.Taken(d.Target, w.cmdID)
		select {
		case w.ch <- agentHello{sha: h.BinarySHA256, reverted: rev, afterResult: w.afterResult, taken: taken}:
		default:
		}
	}
	e.mu.Unlock()
	e.maybeProposeUpdate(ctx, d.Target, h, caps, rev)
	return nil
}

// waitHellos registers a waiter for the hellos of a target.
func (e *Engine) waitHellos(target string) (*helloWaiter, func()) {
	w := &helloWaiter{ch: make(chan agentHello, 16)}
	e.mu.Lock()
	if e.helloWait == nil {
		e.helloWait = map[string][]*helloWaiter{}
	}
	e.helloWait[target] = append(e.helloWait[target], w)
	e.mu.Unlock()
	return w, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.helloWait[target] = slices.DeleteFunc(e.helloWait[target], func(x *helloWaiter) bool { return x == w })
		if len(e.helloWait[target]) == 0 {
			delete(e.helloWait, target)
		}
	}
}

// resultSeen marks the hello waiters of a target: a result came in, so
// the next hellos are of an agent that may have restarted. The caller
// holds e.mu.
func (e *Engine) resultSeen(target string) {
	for _, w := range e.helloWait[target] {
		w.afterResult = true
	}
}

// maybeProposeUpdate: system proposes an update when the capabilities
// differ from the served agent's, the agent can update itself, its binary
// differs, that binary did not revert here already (D-275), and no update
// Plan of the Target is pending. A failure is logged only.
func (e *Engine) maybeProposeUpdate(ctx context.Context, target string, h store.AgentHello, caps *wire.Capabilities, rev *wire.UpdateRevert) {
	served := e.cfg.Agent
	if served.SHA256 == "" || caps == nil || !caps.CanUpdate() || h.BinarySHA256 == served.SHA256 {
		return
	}
	if reflect.DeepEqual(*caps, normalCaps(served.Capabilities)) {
		return
	}
	if rev != nil && rev.ToSHA256 == served.SHA256 {
		return // D-275
	}
	if pending, err := e.pendingAgentUpdate(ctx, target); err != nil || pending != 0 {
		return
	}
	if discarded, err := e.discardedAgentUpdate(ctx, target, served.SHA256); err != nil || discarded {
		return // a person discarded this update once (review L1 of #196)
	}
	_, err := e.ProposeAgentUpdate(ctx, target,
		"The agent can do other things than the agent of this controller.", PrincipalSystem)
	if err != nil && !errors.Is(err, ErrAgentNoUpdate) && !errors.Is(err, ErrTargetBusy) {
		e.log.Warn("system could not propose an agent update", "target", target, "err", err)
	}
}

// --- gate ---

// agentCaps returns the capabilities of the last hello of a target's
// agent; known is false when there is no agent or no hello. A hello
// without a list (or with a list over the limits) is the baseline.
func (e *Engine) agentCaps(ctx context.Context, target string) (caps wire.Capabilities, a store.Agent, known bool, err error) {
	a, err = e.st.AgentByTarget(ctx, target)
	if errors.Is(err, store.ErrNotFound) {
		return wire.Capabilities{}, a, false, nil
	}
	if err != nil || a.Hello == nil {
		return wire.Capabilities{}, a, false, err
	}
	if len(a.Hello.Capabilities) == 0 {
		return wire.BaselineCapabilities(), a, true, nil
	}
	if err := json.Unmarshal(a.Hello.Capabilities, &caps); err != nil {
		return wire.BaselineCapabilities(), a, true, nil
	}
	return caps, a, true, nil
}

// gateOrigin reports whether Plans of an origin are gated (D-268).
func gateOrigin(origin string) bool {
	return origin != OriginAgentUpdate && origin != OriginReenroll && origin != OriginBuild
}

// checkGate refuses a set when a target's agent lacks what the target
// needs (D-259). A target without an agent is not gated here (D-276); an
// agent without a stored hello counts as the baseline.
func (e *Engine) checkGate(ctx context.Context, set *state.Set, targets []string) error {
	out := &AgentOutdatedError{By: map[string][]string{}}
	all := map[string]bool{}
	for _, t := range targets {
		r, err := renderOrNil(set, t)
		if err != nil {
			return err
		}
		if r == nil {
			continue
		}
		caps, a, known, err := e.agentCaps(ctx, t)
		if err != nil {
			return err
		}
		if !known {
			if a.ID == "" {
				continue // no agent yet (a new Target): the Run checks it (D-276)
			}
			// An enrolled agent without a stored hello: the baseline, as
			// in the Run (review L2 of #196).
			caps = wire.BaselineCapabilities()
		}
		if m := caps.Missing(r.Resources); len(m) > 0 {
			out.Targets = append(out.Targets, t)
			out.By[t] = m
			for _, x := range m {
				all[x] = true
			}
		}
	}
	if len(out.Targets) == 0 {
		return nil
	}
	sort.Strings(out.Targets)
	for x := range all {
		out.Missing = append(out.Missing, x)
	}
	sort.Strings(out.Missing)
	return out
}

// runGate is the gate of a Run, right before its first command to the
// agent (D-276). Without a stored hello the agent is taken as the baseline.
func (e *Engine) runGate(ctx context.Context, target string, rs []wire.Resource) error {
	caps, _, known, err := e.agentCaps(ctx, target)
	if err != nil {
		return err
	}
	if !known {
		caps = wire.BaselineCapabilities()
	}
	if m := caps.Missing(rs); len(m) > 0 {
		return &AgentOutdatedError{Targets: []string{target}, Missing: m, By: map[string][]string{target: m}}
	}
	return nil
}

// watchable leaves out what the target's agent lacks: no watch list holds
// a resource that the agent cannot handle (ARCHITECTURE §14).
func (e *Engine) watchable(ctx context.Context, target string, rs []wire.Resource) []wire.Resource {
	caps, _, known, err := e.agentCaps(ctx, target)
	if err != nil || !known {
		caps = wire.BaselineCapabilities()
	}
	out := make([]wire.Resource, 0, len(rs))
	for _, r := range rs {
		if len(caps.Missing([]wire.Resource{r})) == 0 {
			out = append(out, r)
		}
	}
	return out
}

// --- status ---

// AgentStatus returns the agent part of the Target status.
func (e *Engine) AgentStatus(ctx context.Context, target string) (AgentState, error) {
	caps, a, known, err := e.agentCaps(ctx, target)
	if err != nil {
		return AgentState{}, err
	}
	st := AgentState{Update: AgentUpdateUnknown, Missing: []string{}}
	if !known {
		return st, nil
	}
	st.Version, st.HelloAt = a.Hello.Version, a.Hello.At
	if len(a.Hello.Reverted) > 0 {
		var rev wire.UpdateRevert
		if json.Unmarshal(a.Hello.Reverted, &rev) == nil {
			st.Reverted = &rev
		}
	}
	r, err := e.headRender(ctx, target)
	if err != nil {
		return AgentState{}, err
	}
	if r != nil {
		if m := caps.Missing(r.Resources); len(m) > 0 {
			st.Missing = m
		}
	}
	switch {
	case e.cfg.Agent.SHA256 != "" && a.Hello.BinarySHA256 == e.cfg.Agent.SHA256:
		st.Update = AgentUpdateNone
	case !caps.CanUpdate():
		st.Update = AgentUpdateManual
	case len(st.Missing) > 0:
		st.Update = AgentUpdateRequired
	case e.cfg.Agent.SHA256 == "":
		st.Update = AgentUpdateNone // a build without an agent binary
	default:
		st.Update = AgentUpdateAvailable
	}
	return st, nil
}

// --- the Plan ---

// pendingAgentUpdate returns the id of a pending update Plan of the
// target, or 0.
func (e *Engine) pendingAgentUpdate(ctx context.Context, target string) (int64, error) {
	plans, err := e.st.PendingPlans(ctx, OriginAgentUpdate)
	if err != nil {
		return 0, err
	}
	for _, p := range plans {
		var tps []TargetPlan
		if json.Unmarshal(p.Changes, &tps) == nil && len(tps) == 1 && tps[0].Target == target {
			return p.ID, nil
		}
	}
	return 0, nil
}

// discardedAgentUpdate reports whether a person discarded an update Plan
// of the target to the binary sha. Only a discard makes an agent_update
// Plan stale (D-278).
func (e *Engine) discardedAgentUpdate(ctx context.Context, target, sha string) (bool, error) {
	plans, err := e.st.StalePlans(ctx, OriginAgentUpdate)
	if err != nil {
		return false, err
	}
	for _, p := range plans {
		var prop Proposal
		if json.Unmarshal(p.Proposal, &prop) == nil && prop.AgentSHA256 == sha && slices.Equal(prop.Targets, []string{target}) {
			return true, nil
		}
	}
	return false, nil
}

// ProposeAgentUpdate makes a Plan of origin agent_update for one Target
// (D-257, D-267): one step agent:binary, risk disruptive.
func (e *Engine) ProposeAgentUpdate(ctx context.Context, target, reason, author string) (store.Plan, error) {
	if author == "" {
		return store.Plan{}, errors.New("plan: a proposal needs an author")
	}
	head, err := e.st.Head(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return store.Plan{}, &ProposalError{Target: target, Msg: "no such target"}
	}
	if err != nil {
		return store.Plan{}, err
	}
	if r, err := e.headRender(ctx, target); err != nil {
		return store.Plan{}, err
	} else if r == nil {
		return store.Plan{}, &ProposalError{Target: target, Msg: "no such target"}
	}
	st, err := e.AgentStatus(ctx, target)
	if err != nil {
		return store.Plan{}, err
	}
	switch st.Update {
	case AgentUpdateUnknown:
		return store.Plan{}, &ProposalError{Target: target, Err: ErrAgentCannotUpdate,
			Msg: "the agent of this Target never said hello; start the guest and wait for its agent"}
	case AgentUpdateManual:
		return store.Plan{}, &ProposalError{Target: target, Err: ErrAgentCannotUpdate,
			Msg: "this agent is older than slice 3 and cannot update itself; run the one-time install in the guest (ARCHITECTURE §5)"}
	case AgentUpdateNone:
		return store.Plan{}, fmt.Errorf("plan: the agent of %s runs the binary of this controller: %w", target, ErrAgentNoUpdate)
	}
	if id, err := e.pendingAgentUpdate(ctx, target); err != nil {
		return store.Plan{}, err
	} else if id != 0 {
		return store.Plan{}, fmt.Errorf("plan: the update Plan %d of %s is pending: %w", id, target, ErrAgentNoUpdate)
	}
	if err := e.checkIdle([]string{target}); err != nil {
		return store.Plan{}, err
	}
	if reason == "" {
		reason = "update the agent of " + target
	}
	tp := TargetPlan{Target: target, Resources: []ResourceChange{{
		Resource: wire.AgentBinaryID, Action: wire.ActionUpdate, Risk: wire.RiskDisruptive,
		Changes: []wire.Change{{Attr: wire.AttrAgentVersion, From: st.Version, To: e.cfg.Agent.Version, Risk: wire.RiskDisruptive}},
	}}}
	changes, _ := json.Marshal([]TargetPlan{tp})
	proposal, _ := json.Marshal(Proposal{Targets: []string{target}, AgentSHA256: e.cfg.Agent.SHA256})
	p, err := e.st.CreatePlan(ctx, store.Plan{BaseRevision: head.ID, Origin: OriginAgentUpdate, Proposal: proposal,
		Changes: changes, Risk: string(wire.RiskDisruptive), Reason: reason, Author: author})
	if err != nil {
		return store.Plan{}, err
	}
	e.emit(ctx, "plan.created", fmt.Sprintf("plan:%d", p.ID), author,
		map[string]any{"origin": OriginAgentUpdate, "risk": wire.RiskDisruptive, "targets": []string{target}})
	return p, nil
}

// approveAgentUpdate approves an update Plan: no revision; the Run is
// pinned to the head (D-267, D-278).
func (e *Engine) approveAgentUpdate(ctx context.Context, p store.Plan, principal string) (store.Run, error) {
	var targets []TargetPlan
	if err := json.Unmarshal(p.Changes, &targets); err != nil {
		return store.Run{}, err
	}
	if len(targets) != 1 {
		return store.Run{}, fmt.Errorf("plan: update Plan %d has %d targets", p.ID, len(targets))
	}
	names := []string{targets[0].Target}
	if err := e.reserve(names); err != nil {
		return store.Run{}, err
	}
	for attempt := 0; ; attempt++ {
		head, err := e.st.Head(ctx)
		if err != nil {
			e.release(names)
			return store.Run{}, err
		}
		set, err := e.load(ctx, head.ID)
		if err != nil {
			e.release(names)
			return store.Run{}, err
		}
		run, err := e.st.ApproveBuildPlan(ctx, p.ID, principal, head.ID)
		if errors.Is(err, store.ErrHeadMoved) && attempt < 3 {
			continue
		}
		if err != nil {
			e.release(names)
			return store.Run{}, err
		}
		e.emit(ctx, "plan.approved", fmt.Sprintf("plan:%d", p.ID), principal, map[string]any{"run": run.ID, "pinned_revision": head.ID})
		e.start(p, run, set, targets, nil, nil)
		return run, nil
	}
}

// runAgentUpdate is the Run of an update Plan on one Target.
func (e *Engine) runAgentUpdate(ctx context.Context, p store.Plan, run store.Run, set *state.Set, tp TargetPlan) bool {
	s := &stepper{e: e, run: run, target: tp.Target, seq: map[string]int64{}, final: map[string]bool{}, ok: true}
	s.add(ctx, wire.AgentBinaryID)
	var planned []wire.Change
	if len(tp.Resources) == 1 {
		planned = tp.Resources[0].Changes
	}
	// The agent watches its list again at the end, also after a failure:
	// the hellos during the Run sent none (the Target is busy).
	defer func() {
		r, err := renderOrNil(set, tp.Target)
		if err == nil && r != nil {
			err = e.watch(ctx, tp.Target, r)
		}
		if err != nil {
			e.log.Error("watch", "target", tp.Target, "err", err)
		}
	}()
	_, before, _, err := e.agentCaps(ctx, tp.Target)
	if err != nil {
		s.failRest(ctx, err.Error())
		return false
	}
	var oldSHA string
	// oldRevert is the revert that the agent reported before this step
	// (review B1 of #202): a reconnect that lost the poll answer with the
	// command sends it again, also when the link counts the command as
	// taken. Both are checked hellos, so equal fields mean the same revert.
	var oldRevert *wire.UpdateRevert
	if before.Hello != nil {
		oldSHA = before.Hello.BinarySHA256
		if len(before.Hello.Reverted) > 0 {
			var r wire.UpdateRevert
			if json.Unmarshal(before.Hello.Reverted, &r) == nil {
				oldRevert = &r
			}
		}
	}
	if !e.cfg.Agents.Online(tp.Target) {
		if err := e.waitOnline(ctx, tp.Target, -1); err != nil {
			s.failRest(ctx, err.Error())
			return false
		}
	}
	s.set(ctx, wire.AgentBinaryID, wire.StatusRunning, nil, "")
	served := e.cfg.Agent.SHA256
	w, stop := e.waitHellos(tp.Target)
	defer stop()
	id, results, err := e.sendCommand(tp.Target, wire.CommandUpdate, wire.UpdateBody{PlanID: p.ID, SHA256: served},
		func(id string) { w.cmdID = id })
	if err != nil {
		s.set(ctx, wire.AgentBinaryID, wire.StatusFailed, nil, err.Error())
		return false
	}
	defer e.dropChan(id)
	fail := func(msg string) bool {
		s.set(ctx, wire.AgentBinaryID, wire.StatusFailed, nil, msg)
		return false
	}

	// One wait for the result and the hellos (T-102). The result can be
	// lost when the link breaks at the swap: the old agent exits after
	// restartWait without it. Then the hellos tell the outcome: the
	// served sha (passed), or a revert of this update (failed). When the
	// agent took the command and stays silent (no poll) for
	// UpdateHelloWait, the step fails as with no hello after a result.
	// ApplyTimeout stays the limit while the command waits untaken.
	cmdTimeout := time.NewTimer(e.cfg.ApplyTimeout)
	defer cmdTimeout.Stop()
	var helloTimeout <-chan time.Time // set by the result
	check := time.NewTicker(min(5*time.Second, max(e.cfg.UpdateHelloWait/4, 10*time.Millisecond)))
	defer check.Stop()
	gotResult := false
	oldAfterResult := false // a hello with the old sha came after the result
	var silentSince time.Time
	for {
		select {
		case rep := <-results:
			if rep.Type == wire.ReportProgress || gotResult {
				// A second result is the old agent sending its result
				// again after a lost reply (T-108): the first decides.
				continue
			}
			if rep.Error != "" {
				return fail(fmt.Sprintf("plan: agent of %s: %s", tp.Target, rep.Error))
			}
			if len(rep.Items) != 1 {
				return fail("the agent did not report the update")
			}
			switch it := rep.Items[0]; it.Status {
			case wire.StatusUnchanged:
				s.set(ctx, wire.AgentBinaryID, wire.StatusUnchanged, nil, "the agent runs this binary already")
				return true
			case wire.StatusApplied:
			default:
				return fail(it.Output)
			}
			gotResult = true
			cmdTimeout.Stop()
			t := time.NewTimer(e.cfg.UpdateHelloWait)
			defer t.Stop()
			helloTimeout = t.C
		case h := <-w.ch:
			switch {
			case h.reverted != nil && (oldRevert == nil || *h.reverted != *oldRevert) && (h.afterResult || h.taken) &&
				(h.reverted.ToSHA256 == served || h.reverted.ToSHA256 == ""):
				return fail(fmt.Sprintf("the new agent failed in the guest (%s); the agent went back to its old binary", h.reverted.Reason))
			case h.sha == served:
				s.set(ctx, wire.AgentBinaryID, wire.StatusApplied, planned, "")
				return true
			case h.afterResult && oldSHA != "" && h.sha == oldSHA:
				// Either the agent restarted on the old binary, or the old
				// process reconnects before it exits: its result reached
				// us but the reply did not, so it says hello and sends the
				// result again (T-106 F3). The hello cannot tell the two
				// apart, so it decides nothing: the new binary's hello
				// passes the step, else helloTimeout fails it (T-108).
				oldAfterResult = true
			}
			// Before a result and without a revert: a reconnect of the old
			// agent; its result follows.
		case <-check.C:
			if gotResult || !e.cfg.Agents.Taken(tp.Target, id) || e.cfg.Agents.Online(tp.Target) {
				silentSince = time.Time{}
				continue
			}
			now := e.cfg.Now()
			if silentSince.IsZero() {
				silentSince = now
			} else if now.Sub(silentSince) >= e.cfg.UpdateHelloWait {
				return fail(fmt.Sprintf("the agent took the update but sent no result and no hello, and polled no more for %v; the guest goes back to the old binary by itself",
					e.cfg.UpdateHelloWait))
			}
		case <-helloTimeout:
			if oldAfterResult {
				return fail(fmt.Sprintf("the agent came back with its old binary (no hello of the new agent within %v)", e.cfg.UpdateHelloWait))
			}
			return fail(fmt.Sprintf("no hello of the new agent within %v; the guest goes back to the old binary by itself", e.cfg.UpdateHelloWait))
		case <-cmdTimeout.C:
			return fail(fmt.Errorf("%w (%s, %v)", ErrTimeout, tp.Target, e.cfg.ApplyTimeout).Error())
		case <-ctx.Done():
			return fail(ctx.Err().Error())
		}
	}
}
