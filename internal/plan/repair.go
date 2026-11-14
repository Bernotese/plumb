package plan

// Repair of a dead agent (D-372, D-383, D-385, ARCHITECTURE §19). The
// Plan is transport-neutral: a RepairTransport (SSH, T-191) carries the
// fixed bundle of agentinstall to the guest. The action set is fixed by
// the release; no request field reaches the bundle.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/link/agentinstall"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// OriginRepair is a repair Plan (D-372).
const OriginRepair = "repair"

// The steps of a repair Run, and the change of an accepted host key.
const (
	RepairStepID    = "agent:repair"
	RepairHostKeyID = "repair:host-key"
	AttrHostKey     = "host_key"
)

const (
	// RepairOfflineAfter: a repair is offered when the agent was not heard
	// for this long (or agent:silent is open), D-372.
	RepairOfflineAfter = 10 * time.Minute
	// RepairHelloWait bounds the wait for the hello of the repaired agent
	// after exit 0 (D-385).
	RepairHelloWait = 5 * time.Minute
	// RepairSessionTimeout bounds the whole transport session (D-385).
	RepairSessionTimeout = 120 * time.Second
	// MaxRepairOutput is the most guest output a Run step keeps (D-372).
	MaxRepairOutput = 4 << 10
)

var (
	// ErrRepairNotNeeded: the agent is online, the guest does not run, or
	// the agent has been offline for less than RepairOfflineAfter without
	// agent:silent (409 repair-not-needed).
	ErrRepairNotNeeded = errors.New("repair not needed")
	// ErrRepairNotReady: the transport cannot reach this guest (409
	// repair-not-ready); TargetStatus.repair says why.
	ErrRepairNotReady = errors.New("repair not ready")
)

// RepairSend is one transport session.
type RepairSend struct {
	Target string
	// AcceptHostKey is a host key fingerprint that the approved Plan
	// accepts (D-384); "" accepts only the pins.
	AcceptHostKey string
	// Bundle writes the bundle. The transport calls it only after the
	// host key check passed, so nothing reaches an unknown peer.
	Bundle func(w io.Writer) error
	// StaticIP is the static address of the Target (spec.guest.ip without
	// the prefix); "" for dhcp. Reported is the address list that the
	// agent reported last (guest data, D-216): the transport dials one of
	// them only on-link (review R5 of #290).
	StaticIP string
	Reported []string
	// GuestMAC is the MAC of the guest's NIC in Proxmox (trusted, review
	// S1 of #298): a reported address counts only when the controller's
	// neighbour table maps it to this MAC.
	GuestMAC string
	// Output receives the guest's stdout and stderr. The Run bounds and
	// escapes it; the transport may stop reading after MaxRepairOutput.
	Output io.Writer
}

// RepairTransport carries the repair bundle to a guest (T-191: SSH).
type RepairTransport interface {
	// Ready returns nil when a repair of the target can run now, or an
	// error that wraps ErrRepairNotReady and says why.
	Ready(ctx context.Context, target string) error
	// Send runs one session. sent reports whether any byte of the bundle
	// left the controller; exit is the exit code of the forced command
	// (meaningful with a nil error).
	Send(ctx context.Context, s RepairSend) (exit int, sent bool, err error)
}

// RepairStatus is TargetStatus.repair (D-384): ready, off with the
// reasons, or unknown.
type RepairStatus struct {
	State        string   `json:"state"`
	Reasons      []string `json:"reasons"`
	Port         *int     `json:"port"`
	HostKeys     []string `json:"host_keys"`
	HostKeysSeen []string `json:"host_keys_seen"`
	KeyConfirmed bool     `json:"key_confirmed"`
}

// The states of RepairStatus.
const (
	RepairReady   = "ready"
	RepairOff     = "off"
	RepairUnknown = "unknown"
)

// RepairStatuser is a transport that knows the readiness of a Target
// (T-191). nil status: no agent (TargetStatus.repair is null).
type RepairStatuser interface {
	Status(ctx context.Context, target string) (*RepairStatus, error)
}

// HostKeyChecker refuses an accepted host key that is pinned for another
// Target (review S3 of #298). Optional on a transport.
type HostKeyChecker interface {
	CheckAcceptHostKey(ctx context.Context, target, fingerprint string) error
}

// RepairTokens makes and drops repair enrollment tokens (link.Link).
type RepairTokens interface {
	NewRepairToken(ctx context.Context, target string) (link.RepairToken, error)
	DropRepairToken(ctx context.Context, target string, t link.RepairToken) error
}

// RepairConfig is what a repair Run needs from the controller.
type RepairConfig struct {
	Transport RepairTransport // nil: no repair (409 repair-not-ready)
	Tokens    RepairTokens
	// AgentBinary is the binary of GET /agent/v1/binary; CA the Plumb CA
	// PEM; AgentURL the agent listener URL that guests use.
	AgentBinary []byte
	CA          []byte
	AgentURL    string
	// Watch returns the repair key set for every watch command (D-383);
	// nil or a nil result: none. OnReport receives the checked repair
	// report of a bound Target (D-384).
	Watch    func(ctx context.Context) *wire.RepairWatch
	OnReport func(ctx context.Context, target string, r wire.RepairReport) error
	// HelloWait and SessionTimeout override RepairHelloWait and
	// RepairSessionTimeout (tests).
	HelloWait, SessionTimeout time.Duration
}

func (e *Engine) repairConfig() RepairConfig { return e.cfg.Repair }

// repairNeeded checks the offer rule of D-385: the guest runs, and its
// agent is offline for RepairOfflineAfter or agent:silent is open.
func (e *Engine) repairNeeded(ctx context.Context, target string) error {
	if e.cfg.Agents.Online(target) {
		return fmt.Errorf("plan: the agent of %s is online: %w", target, ErrRepairNotNeeded)
	}
	b, err := e.st.GetBinding(ctx, state.KindTarget, target)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("plan: %s has no guest: %w", target, ErrRepairNotNeeded)
	}
	if err != nil {
		return err
	}
	st, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
	if err != nil {
		return fmt.Errorf("plan: the guest of %s cannot be read: %w", target, err)
	}
	if st.Power != driver.PowerRunning {
		return fmt.Errorf("plan: the guest of %s does not run; start it first: %w", target, ErrRepairNotNeeded)
	}
	if _, err := e.st.OpenIssue(ctx, target, AgentSilentID); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	a, err := e.st.AgentByTarget(ctx, target)
	if errors.Is(err, store.ErrNotFound) {
		// Never enrolled, or re-enrollment pending: no agent to repair
		// and, without one that ran, no repair line.
		return fmt.Errorf("plan: %s has no enrolled agent: %w", target, ErrRepairNotReady)
	}
	if err != nil {
		return err
	}
	seen := a.LastSeenAt
	if seen.IsZero() {
		seen = a.EnrolledAt
	}
	if d := e.cfg.Now().Sub(seen); d < RepairOfflineAfter {
		return fmt.Errorf("plan: the agent of %s is offline for %v only (repair after %v): %w",
			target, d.Truncate(time.Second), RepairOfflineAfter, ErrRepairNotNeeded)
	}
	return nil
}

// RepairStatus is TargetStatus.repair: the transport's readiness, and
// "firewall" when the Target's firewall keeps the controller from the SSH
// port (D-384). nil: no transport that knows it, or no agent.
func (e *Engine) RepairStatus(ctx context.Context, target string) (*RepairStatus, error) {
	st, ok := e.cfg.Repair.Transport.(RepairStatuser)
	if !ok {
		return nil, nil
	}
	rs, err := st.Status(ctx, target)
	if err != nil || rs == nil || rs.State == RepairUnknown {
		return rs, err
	}
	r, err := e.headRender(ctx, target)
	if err != nil {
		return nil, err
	}
	if r != nil && firewallBlocksRepair(r.Resources, e.repairFrom(ctx)) {
		rs.Reasons = append(rs.Reasons, "firewall")
		rs.State = RepairOff
	}
	return rs, nil
}

// repairFrom is the From list of the repair key set.
func (e *Engine) repairFrom(ctx context.Context) []string {
	if w := e.repairWatch(ctx); w != nil {
		return w.From
	}
	return nil
}

// firewallBlocksRepair reports whether a present firewall resource keeps
// every From address from the SSH port: allow_no_ssh, or ssh_sources
// that hold none of them.
func firewallBlocksRepair(rs []wire.Resource, from []string) bool {
	for _, r := range rs {
		if r.Type != firewallType || r.Attrs[wire.AttrState] == wire.StateAbsent {
			continue
		}
		noSSH, _ := r.Attrs["allow_no_ssh"].(bool)
		src, _ := r.Attrs["ssh_sources"].(string)
		if noSSH || !SourcesHold(src, from) {
			return true
		}
	}
	return false
}

// SourcesHold reports whether a sources list ("a/n,b/m"; "" = from
// everywhere) holds one of the addresses.
func SourcesHold(sources string, addrs []string) bool {
	if strings.TrimSpace(sources) == "" {
		return true
	}
	for _, s := range strings.Split(sources, ",") {
		_, n, err := net.ParseCIDR(strings.TrimSpace(s))
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil && n.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// RepairFirewallWarning is the Plan warning of a change that turns the
// SSH repair off by the firewall (D-384), or "".
func (e *Engine) RepairFirewallWarning(ctx context.Context, tp TargetPlan) string {
	from := e.repairFrom(ctx)
	if len(from) == 0 {
		return ""
	}
	for _, rc := range tp.Resources {
		typ, _, _ := wire.SplitID(rc.Resource)
		if typ != firewallType {
			continue
		}
		for _, c := range rc.Changes {
			off := false
			switch c.Attr {
			case "allow_no_ssh":
				off = c.To == true
			case "ssh_sources":
				s, ok := c.To.(string)
				off = ok && !SourcesHold(s, from)
			}
			if off {
				return fmt.Sprintf("%s: the firewall does not let the controller (%s) reach SSH, so the repair fallback is off.",
					tp.Target, strings.Join(from, ", "))
			}
		}
	}
	return ""
}

// repairReady asks the transport.
func (e *Engine) repairReady(ctx context.Context, target string) error {
	rc := e.repairConfig()
	if rc.Transport == nil || rc.Tokens == nil {
		return fmt.Errorf("plan: this controller has no repair transport: %w", ErrRepairNotReady)
	}
	if len(rc.AgentBinary) == 0 {
		return fmt.Errorf("plan: this build has no agent binary: %w", ErrRepairNotReady)
	}
	if rs, err := e.RepairStatus(ctx, target); err != nil {
		return err
	} else if rs != nil && rs.State != RepairReady {
		reasons := strings.Join(rs.Reasons, ", ")
		if reasons == "" {
			reasons = "no repair report yet"
		}
		return fmt.Errorf("plan: the repair of %s is off (%s): %w", target, reasons, ErrRepairNotReady)
	}
	if err := rc.Transport.Ready(ctx, target); err != nil {
		if errors.Is(err, ErrRepairNotReady) {
			return err
		}
		return fmt.Errorf("plan: %v: %w", err, ErrRepairNotReady)
	}
	return nil
}

// ProposeRepair makes a repair Plan for one Target (D-385): origin
// repair, risk disruptive, human-only repair. acceptHostKey is a
// fingerprint that the person accepts (checked for its form by the API).
func (e *Engine) ProposeRepair(ctx context.Context, target, reason, acceptHostKey, author string) (store.Plan, error) {
	if author == "" {
		return store.Plan{}, errors.New("plan: a proposal needs an author")
	}
	if e.restoreActive() {
		return store.Plan{}, ErrRestoredMode
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
	if err := e.repairNeeded(ctx, target); err != nil {
		return store.Plan{}, err
	}
	if err := e.repairReady(ctx, target); err != nil {
		return store.Plan{}, err
	}
	if err := e.checkAcceptHostKey(ctx, target, acceptHostKey); err != nil {
		return store.Plan{}, err
	}
	if err := e.checkIdle([]string{target}); err != nil {
		return store.Plan{}, err
	}
	if reason == "" {
		reason = "repair the agent of " + target
	}
	tp := TargetPlan{Target: target, Resources: []ResourceChange{}}
	if acceptHostKey != "" {
		tp.Resources = append(tp.Resources, ResourceChange{Resource: RepairHostKeyID, Action: wire.ActionUpdate,
			Risk: wire.RiskDisruptive, Changes: []wire.Change{{Attr: AttrHostKey, To: acceptHostKey, Risk: wire.RiskDisruptive}}})
	}
	changes, _ := json.Marshal([]TargetPlan{tp})
	proposal, _ := json.Marshal(Proposal{Targets: []string{target}})
	p, err := e.st.CreatePlan(ctx, store.Plan{BaseRevision: head.ID, Origin: OriginRepair, Proposal: proposal,
		Changes: changes, Risk: string(wire.RiskDisruptive), Reason: reason, Author: author,
		HumanOnly: []string{HumanOnlyRepair}})
	if err != nil {
		return store.Plan{}, err
	}
	e.emit(ctx, "plan.created", fmt.Sprintf("plan:%d", p.ID), author,
		map[string]any{"origin": OriginRepair, "risk": wire.RiskDisruptive, "targets": []string{target}})
	return p, nil
}

// repairTarget reads the one Target of a repair Plan.
func repairTarget(p store.Plan) (TargetPlan, error) {
	var tps []TargetPlan
	if err := json.Unmarshal(p.Changes, &tps); err != nil {
		return TargetPlan{}, err
	}
	if len(tps) != 1 {
		return TargetPlan{}, fmt.Errorf("plan: repair Plan %d has %d targets", p.ID, len(tps))
	}
	return tps[0], nil
}

// acceptedHostKey is the fingerprint that the Plan accepts, or "".
func (tp TargetPlan) acceptedHostKey() string {
	for _, r := range tp.Resources {
		if r.Resource != RepairHostKeyID {
			continue
		}
		for _, c := range r.Changes {
			if s, ok := c.To.(string); ok && c.Attr == AttrHostKey {
				return s
			}
		}
	}
	return ""
}

// approveRepair approves a repair Plan: the offer rule and the transport
// are checked again (D-385); no revision, the Run is pinned to the head.
func (e *Engine) approveRepair(ctx context.Context, p store.Plan, principal string) (store.Run, error) {
	tp, err := repairTarget(p)
	if err != nil {
		return store.Run{}, err
	}
	names := []string{tp.Target}
	if r, err := e.headRender(ctx, tp.Target); err != nil {
		return store.Run{}, err
	} else if r == nil {
		return store.Run{}, &ProposalError{Target: tp.Target, Msg: "no such target"}
	}
	if err := e.repairNeeded(ctx, tp.Target); err != nil {
		return store.Run{}, err
	}
	if err := e.repairReady(ctx, tp.Target); err != nil {
		return store.Run{}, err
	}
	if err := e.checkAcceptHostKey(ctx, tp.Target, tp.acceptedHostKey()); err != nil {
		return store.Run{}, err
	}
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
		e.start(p, run, set, []TargetPlan{tp}, nil, nil)
		return run, nil
	}
}

// runRepair is the Run of a repair Plan (D-385). The old credential is
// never touched here: it ends only when the guest enrolls with the new
// token (review R1 of #290). Every failure drops the unused token.
func (e *Engine) runRepair(ctx context.Context, p store.Plan, run store.Run, set *state.Set, tp TargetPlan) bool {
	s := &stepper{e: e, run: run, target: tp.Target, seq: map[string]int64{}, final: map[string]bool{}, ok: true}
	s.add(ctx, RepairStepID)
	rc := e.repairConfig()
	subject := "target:" + tp.Target
	result := "failed"
	defer func() {
		e.emit(context.WithoutCancel(ctx), "repair.finished", subject, "system", map[string]any{"plan": p.ID, "run": run.ID, "result": result})
		// The agent watches its list again (the hellos during the Run sent
		// none: the Target was busy).
		if r, err := renderOrNil(set, tp.Target); err == nil && r != nil {
			if err := e.watch(ctx, tp.Target, r); err != nil {
				e.log.Info("watch after the repair", "target", tp.Target, "err", err)
			}
		}
	}()
	fail := func(msg string) bool {
		s.set(ctx, RepairStepID, wire.StatusFailed, nil, msg)
		return false
	}
	if rc.Transport == nil || rc.Tokens == nil {
		return fail("this controller has no repair transport")
	}
	s.set(ctx, RepairStepID, wire.StatusRunning, nil, "")
	e.emit(ctx, "repair.started", subject, "system", map[string]any{"plan": p.ID, "run": run.ID})

	old, err := e.st.AgentByTarget(ctx, tp.Target)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fail(err.Error())
	}
	tok, err := rc.Tokens.NewRepairToken(ctx, tp.Target)
	if err != nil {
		return fail("the repair token could not be made: " + err.Error())
	}
	drop := func() {
		if err := rc.Tokens.DropRepairToken(context.WithoutCancel(ctx), tp.Target, tok); err != nil {
			e.log.Error("drop the repair token", "target", tp.Target, "err", err)
		}
	}
	bundle := agentinstall.Bundle{Agent: rc.AgentBinary, CA: rc.CA, URL: rc.AgentURL, Token: tok.Token}
	out := &boundedBuffer{max: MaxRepairOutput}
	session := rc.SessionTimeout
	if session <= 0 {
		session = RepairSessionTimeout
	}
	sess, cancel := context.WithTimeout(ctx, session)
	start := e.cfg.Now()
	static, reported := e.repairAddrs(ctx, set, tp.Target)
	exit, sent, err := rc.Transport.Send(sess, RepairSend{Target: tp.Target, AcceptHostKey: tp.acceptedHostKey(),
		StaticIP: static, Reported: reported, GuestMAC: e.guestMAC(ctx, tp.Target),
		Bundle: func(w io.Writer) error { return agentinstall.WriteBundle(w, bundle) }, Output: out})
	cancel()
	tok.Token = "" // not needed past the session
	text := repairOutput(exit, err == nil, out.Bytes())
	if err != nil {
		drop()
		if !sent {
			return fail("nothing was sent; the agent stays as it was: " + escapeOutput([]byte(err.Error()), 512) + text)
		}
		return fail("the session failed: " + escapeOutput([]byte(err.Error()), 512) + text)
	}
	if exit != 0 {
		drop()
		return fail("the install in the guest failed; the agent stays as it was" + text)
	}

	// Exit 0: wait for a hello of the new agent (a new agent id).
	wait := rc.HelloWait
	if wait <= 0 {
		wait = RepairHelloWait
	}
	w, stop := e.waitHellos(tp.Target)
	defer stop()
	timeout := time.NewTimer(wait)
	defer timeout.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		a, err := e.st.AgentByTarget(ctx, tp.Target)
		if err == nil && a.ID != old.ID && a.Hello != nil && !a.Hello.At.Before(start) {
			s.set(ctx, RepairStepID, wire.StatusApplied, nil, "the agent enrolled again and said hello"+text)
			result = "succeeded"
			return true
		}
		select {
		case <-w.ch:
		case <-tick.C:
		case <-timeout.C:
			drop()
			return fail(fmt.Sprintf("no hello of the repaired agent within %v; the old credential stays valid until a new enrollment", wait) + text)
		case <-ctx.Done():
			drop()
			return fail(ctx.Err().Error())
		}
	}
}

// repairAddrs returns the static address of the Target and the address
// list that its agent reported last.
func (e *Engine) repairAddrs(ctx context.Context, set *state.Set, target string) (string, []string) {
	var static string
	if r, err := renderOrNil(set, target); err == nil && r != nil {
		if ip, _ := r.Guest.Attrs[driver.AttrIP].(string); ip != "" && ip != "dhcp" {
			static, _, _ = strings.Cut(ip, "/")
		}
	}
	rep, known, err := e.st.ReportedAddrs(ctx, target)
	if err != nil || !known {
		return static, nil
	}
	return static, rep.List
}

// guestMAC is the MAC of the Target's guest NIC in Proxmox, or "".
func (e *Engine) guestMAC(ctx context.Context, target string) string {
	b, err := e.st.GetBinding(ctx, state.KindTarget, target)
	if err != nil {
		return ""
	}
	st, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
	if err != nil {
		return ""
	}
	return st.MAC
}

// checkAcceptHostKey refuses an accepted fingerprint that is pinned for
// another Target (review S3 of #298).
func (e *Engine) checkAcceptHostKey(ctx context.Context, target, fp string) error {
	if fp == "" {
		return nil
	}
	c, ok := e.cfg.Repair.Transport.(HostKeyChecker)
	if !ok {
		return nil
	}
	if err := c.CheckAcceptHostKey(ctx, target, fp); err != nil {
		return fmt.Errorf("plan: %v: %w", err, ErrRepairNotReady)
	}
	return nil
}

// RewatchAll sends the watch list, with the current repair key set, to
// the agent of every bound Target that is online and idle (review S2 of
// #298: a rotation or a revoke reaches the guests now, not at their next
// hello). A Target that is busy gets it at the end of its Run.
func (e *Engine) RewatchAll(ctx context.Context) {
	if e.restoreActive() {
		return
	}
	head, err := e.st.Head(ctx)
	if err != nil {
		return
	}
	set, err := e.load(ctx, head.ID)
	if err != nil {
		e.log.Error("rewatch: load the head", "err", err)
		return
	}
	for _, d := range set.Docs() {
		if d.Kind != state.KindTarget || !e.cfg.Agents.Online(d.Name) {
			continue
		}
		if bound, err := e.bound(ctx, d.Name); err != nil || !bound {
			continue
		}
		e.mu.Lock()
		busy := e.busy[d.Name] > 0
		e.mu.Unlock()
		if busy {
			continue
		}
		r, err := renderOrNil(set, d.Name)
		if err != nil || r == nil {
			continue
		}
		if err := e.watch(ctx, d.Name, r); err != nil {
			e.log.Warn("rewatch after a repair key change", "target", d.Name, "err", err)
		}
	}
}

// repairWatch is the repair key set of a watch command, or nil.
func (e *Engine) repairWatch(ctx context.Context) *wire.RepairWatch {
	if e.cfg.Repair.Watch == nil {
		return nil
	}
	return e.cfg.Repair.Watch(ctx)
}

// handleRepairReport hands the repair report of a bound Target to the
// transport side (D-384). link checked its limits.
func (e *Engine) handleRepairReport(ctx context.Context, d link.Delivery) error {
	if bound, err := e.bound(ctx, d.Target); err != nil || !bound {
		return err
	}
	if e.cfg.Repair.OnReport == nil || d.Report.Repair == nil {
		return nil
	}
	return e.cfg.Repair.OnReport(ctx, d.Target, *d.Report.Repair)
}

// boundedBuffer keeps the first max bytes written to it and drops the
// rest; it never fails a write, so the session reads on.
type boundedBuffer struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	max  int
	more bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
		if len(p) > room {
			b.more = true
		}
	} else if len(p) > 0 {
		b.more = true
	}
	return len(p), nil
}

// Bytes returns the kept bytes.
func (b *boundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

// repairOutput is the step text: the exit code and the escaped output,
// together at most MaxRepairOutput bytes. Nothing in it is parsed.
func repairOutput(exit int, ran bool, out []byte) string {
	var b strings.Builder
	if ran {
		b.WriteString("\nexit code " + strconv.Itoa(exit))
	}
	if len(out) > 0 {
		b.WriteString("\noutput of the guest:\n")
		b.WriteString(escapeOutput(out, MaxRepairOutput-1024))
	}
	return b.String()
}

// escapeOutput writes guest bytes as text: newline and tab stay, every
// other control character, DEL and invalid UTF-8 become \xNN. The result
// is at most limit bytes and ends in "..." when cut.
func escapeOutput(p []byte, limit int) string {
	var b strings.Builder
	for len(p) > 0 {
		r, n := utf8.DecodeRune(p)
		var piece string
		switch {
		case r == utf8.RuneError && n <= 1:
			piece = fmt.Sprintf(`\x%02x`, p[0])
			n = 1
		case r == '\n' || r == '\t':
			piece = string(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == ' ' || r == ' ' || (r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩'):
			piece = fmt.Sprintf(`\u{%x}`, r)
		case r == '\\':
			piece = `\\`
		default:
			piece = string(p[:n])
		}
		if b.Len()+len(piece) > limit-3 {
			b.WriteString("...")
			return b.String()
		}
		b.WriteString(piece)
		p = p[n:]
	}
	return b.String()
}
