// Package agent is the agent loop inside a guest: enroll, poll, run
// commands and report drift (docs/ARCHITECTURE.md §5, §6).
//
// Rules for everything the agent sends: wire.Item.Output and every error
// text about a resource go through resource.SafeOutput; changes come from
// resource Diff, which already redacts sensitive attributes. An observed
// State never leaves the guest.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// Options configure an Agent. Zero values mean the production defaults.
type Options struct {
	Config   Config
	Root     string          // guest root for resources; default "/"
	Runner   resource.Runner // default resource.ExecRunner
	Version  string
	Hostname string // default os.Hostname
	Log      *slog.Logger

	// CloudInitFlag is created after enrollment; default <Root>/etc/cloud/cloud-init.disabled.
	CloudInitFlag string
	// Debounce of file events before a drift check; default 2 s.
	Debounce time.Duration
	// PollWait is the long-poll wait in seconds; default wire.MaxPollWait.
	PollWait int
	// BackoffMin and BackoffMax bound the retry delay; default 1 s and 60 s.
	BackoffMin, BackoffMax time.Duration

	// Addrs reads the address list of the guest; default the netlink dumps
	// of readAddrs (D-213).
	Addrs func() (list []string, truncated bool, err error)
	// AddrsEvery is the period of the address report when nothing changed;
	// default 15 min. AddrsGap is the least time between two address
	// reports and two reads; default 35 s, a margin over the store limit
	// of 30 s of the controller (D-212), so network jitter gives no 429.
	AddrsEvery, AddrsGap time.Duration

	// Sync flushes the file systems before the power-off at the end of
	// seal; default unix.Sync. Tests record it.
	Sync func()

	// Self-update (T-091): ConfigPath is the config file the agent runs
	// with (for the self-test and the guard unit; default
	// DefaultConfigPath), BinaryPath the agent binary (default
	// DefaultBinaryPath), UnitDir where the guard units go (default
	// DefaultUnitDir), SelfExe the running binary that BinarySHA256 hashes
	// (default /proc/self/exe).
	ConfigPath, BinaryPath, UnitDir, SelfExe string
	// RepairTokenFile is the one-shot token file (D-390); default
	// wire.RepairTokenFile under Root.
	RepairTokenFile string

	// ConfirmWindow is the time of a safe apply for its round trip
	// (D-300); default wire.SafeApplySeconds. Tests shorten it.
	ConfirmWindow time.Duration
}

// Agent is one agent process.
type Agent struct {
	// unbindID is the id of an unbind command that ran (T-193, D-388).
	unbindID string

	opt    Options
	cfg    Config
	client *client
	state  stateDir
	env    resource.Env
	log    *slog.Logger

	credMu sync.Mutex
	cred   string

	// applyLock: an apply holds it for writing, a drift check for reading,
	// so a check never observes a half-applied resource (D-055).
	applyLock sync.RWMutex
	// safeMu: one safe apply at a time (D-300).
	safeMu sync.Mutex

	mu       sync.Mutex
	desired  []wire.Resource
	applying int                  // applies in progress
	open     map[string]wire.Item // drift that is on the guest now
	dirty    map[string]bool      // drift items not yet sent
	resolved map[string]wire.Item // drift that went away, not yet sent

	watcher     *watcher           // set by Run (tests read it)
	net         *netWatch          // live ip/nft watch (D-367); its subscriptions belong to the drift loop
	listChanged chan struct{}      // the desired list changed
	applyDone   chan []string      // an apply ended; ids of its resources
	driftBusy   chan time.Duration // a drift send got 429/503: its Retry-After (T-202)
	sendMu      sync.Mutex         // one drift report at a time

	// Address report state; only the Run goroutine uses it.
	addrLast  addrList  // the list the controller took last
	addrKnown bool      // addrLast is set
	addrAt    time.Time // when the controller took addrLast
	addrTry   time.Time // last address report, sent or not
	addrRead  time.Time // last read at a poll round
	addrErr   bool      // the last read failed (log once)

	// Self-update (T-091); only the Run goroutine uses these.
	upd        *updater
	binarySHA  string // SHA-256 of the running binary, read at start
	caps       wire.Capabilities
	confirmed  bool      // a pending update was checked after a hello
	restartCmd string    // id of an update that swapped the binary
	restartBy  time.Time // exit at the latest then

	sealedCmd string // id of a seal that ended without a failed step (under mu)
	// sealedContainer: that seal was of a container (D-253; under mu).
	sealedContainer bool

	oneShot *oneShot // the one-shot enrollment token (D-390); Run goroutine only

	repair repairState // the SSH repair line and report (D-383, D-384)
}

// New builds an agent. It reads the stored credential and desired list.
func New(opt Options) (*Agent, error) {
	cfg := opt.Config
	if err := cfg.check(); err != nil {
		return nil, err
	}
	if opt.Root == "" {
		opt.Root = "/"
	}
	if opt.Runner == nil {
		opt.Runner = resource.ExecRunner{}
	}
	if opt.Hostname == "" {
		opt.Hostname, _ = os.Hostname()
	}
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	if opt.CloudInitFlag == "" {
		opt.CloudInitFlag = filepath.Join(opt.Root, CloudInitFlag)
	}
	if opt.Debounce <= 0 {
		opt.Debounce = 2 * time.Second
	}
	if opt.PollWait <= 0 || opt.PollWait > wire.MaxPollWait {
		opt.PollWait = wire.MaxPollWait
	}
	if opt.BackoffMin <= 0 {
		opt.BackoffMin = time.Second
	}
	if opt.BackoffMax < opt.BackoffMin {
		opt.BackoffMax = max(60*time.Second, opt.BackoffMin)
	}
	if opt.Addrs == nil {
		opt.Addrs = readAddrs
	}
	if opt.AddrsEvery <= 0 {
		opt.AddrsEvery = 15 * time.Minute
	}
	if opt.AddrsGap <= 0 {
		opt.AddrsGap = 35 * time.Second
	}
	if opt.Sync == nil {
		opt.Sync = unix.Sync
	}
	if opt.ConfigPath == "" {
		opt.ConfigPath = DefaultConfigPath
	}
	if opt.BinaryPath == "" {
		opt.BinaryPath = DefaultBinaryPath
	}
	if opt.UnitDir == "" {
		opt.UnitDir = DefaultUnitDir
	}
	if opt.ConfirmWindow <= 0 {
		opt.ConfirmWindow = wire.SafeApplySeconds * time.Second
	}
	if opt.SelfExe == "" {
		opt.SelfExe = "/proc/self/exe"
	}
	c, err := newClient(cfg, time.Duration(opt.PollWait)*time.Second)
	if err != nil {
		return nil, err
	}
	a := &Agent{
		opt: opt, cfg: cfg, client: c,
		state:       stateDir{dir: cfg.StateDir},
		env:         agentEnv(opt, cfg),
		log:         opt.Log,
		open:        map[string]wire.Item{},
		dirty:       map[string]bool{},
		resolved:    map[string]wire.Item{},
		listChanged: make(chan struct{}, 1),
		applyDone:   make(chan []string, 64),
		driftBusy:   make(chan time.Duration, 1),
		caps:        Capabilities(),
	}
	a.net = newNetWatch(opt.Log, opt.Debounce, a.isApplying)
	a.upd = &updater{state: a.state, bin: binPaths{bin: opt.BinaryPath}, unitDir: opt.UnitDir,
		config: opt.ConfigPath, run: opt.Runner, log: opt.Log}
	if a.binarySHA, err = hashFile(opt.SelfExe); err != nil {
		a.log.Warn("cannot hash the running binary", "err", err)
	}
	// An unsafe state dir stops the start: it would hold the credential and
	// clear secret values (D-159, D-161). A cleanup error only warns.
	if err := a.state.ensure(); err != nil {
		return nil, err
	}
	if err := a.state.cleanStaleTemps(); err != nil {
		a.log.Warn("could not clean stale agent state temp files", "error", err)
	}
	if a.cred, err = a.state.credential(); err != nil {
		return nil, fmt.Errorf("read credential: %w", err)
	}
	if a.desired, err = a.state.desired(); err != nil {
		return nil, fmt.Errorf("read desired list: %w", err)
	}
	return a, nil
}

// Run enrolls when needed, polls for commands and watches for drift until
// ctx ends. It never gives up: every failure is retried with backoff.
func (a *Agent) Run(ctx context.Context) error {
	// A marker of a safe apply: the agent stopped inside the window. It
	// restores before anything else and sends the result of that apply
	// (D-300).
	restored := a.restoreAtStart(ctx)
	a.oneShot = a.takeOneShot()
	a.loadRepair()
	w, err := newWatcher()
	if err != nil {
		return fmt.Errorf("inotify: %w", err)
	}
	defer w.close()
	a.mu.Lock()
	a.watcher = w
	a.mu.Unlock()
	a.net.probe() // before the first hello, which carries live_watch (D-367)
	go a.driftLoop(ctx, w)

	b := backoff{min: a.opt.BackoffMin, max: a.opt.BackoffMax}
	connected := false
	// pending is a command result that the controller has not accepted
	// yet. It is sent again until a call succeeds; after a 401 the link has
	// forgotten the command, so the result is dropped.
	pending := restored
	for ctx.Err() == nil {
		// After an update: exit once the controller took the result, or
		// after restartWait without that (D-258).
		if a.restartCmd != "" && (pending == nil || time.Now().After(a.restartBy)) {
			a.log.Info("agent binary updated; exiting for the restart")
			return ErrRestart
		}
		if a.tryOneShot(ctx) {
			connected = false // a new credential says hello
		}
		if a.credential() == "" {
			if err := a.enroll(ctx); err != nil {
				a.log.Warn("enroll failed", "err", err)
				b.sleep(ctx, retryAfterOf(err))
				continue
			}
		}
		if !connected {
			// After start and after every reconnect: hello, then all open drift.
			hello := a.hello()
			if err := a.send(ctx, wire.Report{Type: wire.ReportHello, Hello: hello}); err != nil {
				connected = a.connError(ctx, err, &b, connected)
				continue
			}
			a.addrsTaken(hello.Addrs, hello.AddrsTruncated, time.Now())
			if !a.confirmed {
				// The first accepted hello confirms an update (D-271).
				if err := a.upd.confirm(ctx, a.binarySHA); err != nil {
					a.log.Warn("confirm the agent update", "err", err)
				} else {
					a.confirmed = true
				}
			}
			if err := a.flushDrift(ctx, true); err != nil {
				connected = a.connError(ctx, err, &b, connected)
				continue
			}
			// The backoff is reset only after a successful poll or result:
			// an error after a working hello must not loop at BackoffMin.
			connected = true
			a.markRepairDue() // a new hello: the controller gets the repair report again
		}
		if pending != nil {
			if err := a.send(ctx, *pending); err != nil {
				if permanent(err) {
					// The link does not know the command any more (for example
					// after a controller restart). Drop it and poll again.
					a.log.Warn("result refused for good; dropped", "command", pending.CommandID, "err", err)
					pending = nil
					continue
				}
				if errors.Is(err, errUnauthorized) {
					a.log.Warn("result dropped after 401", "command", pending.CommandID)
					pending = nil
				}
				connected = a.connError(ctx, err, &b, connected)
				continue
			}
			sent := pending.CommandID
			pending = nil
			b.reset()
			if a.unboundBy(sent) { // D-388: the controller took the unbind result
				a.finishUnbind(ctx)
				return nil
			}
			if a.sealedBy(sent) {
				// Review B1: the controller took the result of seal. The
				// agent sends nothing more: it removes itself, syncs and
				// powers off. It does not poll again, also when that fails.
				_ = a.finishSeal(ctx)
				<-ctx.Done()
				return ctx.Err()
			}
		}
		if err := a.sendAddrs(ctx, time.Now()); err != nil {
			connected = a.connError(ctx, err, &b, connected)
			continue
		}
		if err := a.sendRepair(ctx, time.Now()); err != nil {
			connected = a.connError(ctx, err, &b, connected)
			continue
		}
		cmd, err := a.client.poll(ctx, a.credential(), a.opt.PollWait)
		if err != nil {
			connected = a.connError(ctx, err, &b, connected)
			continue
		}
		b.reset()
		if cmd == nil {
			continue
		}
		rep := a.handle(ctx, *cmd)
		pending = &rep // sent at the top of the next round
	}
	return ctx.Err()
}

// connError handles a failed call and returns the new connected state.
// A 401 drops the credential, so the next round enrolls with a fresh token.
// A busy controller (429, 503) keeps the link: no new hello, no resend of
// all drift. Every error waits with backoff, and at least Retry-After.
func (a *Agent) connError(ctx context.Context, err error, b *backoff, connected bool) bool {
	if ctx.Err() != nil {
		return false
	}
	var busy *busyError
	switch {
	case errors.Is(err, errUnauthorized):
		a.log.Warn("credential refused; enrolling again")
		if derr := a.dropCredential(); derr != nil {
			a.log.Error("drop credential", "err", derr)
		}
		connected = false
	case errors.As(err, &busy):
		a.log.Info("controller busy", "status", busy.Status, "retry_after", busy.RetryAfter)
	default:
		a.log.Warn("controller call failed", "err", err)
		connected = false
	}
	b.sleep(ctx, retryAfterOf(err))
	return connected
}

func retryAfterOf(err error) time.Duration {
	var busy *busyError
	if errors.As(err, &busy) {
		return busy.RetryAfter
	}
	return 0
}

func (a *Agent) credential() string {
	a.credMu.Lock()
	defer a.credMu.Unlock()
	return a.cred
}

func (a *Agent) dropCredential() error {
	a.credMu.Lock()
	a.cred = ""
	a.credMu.Unlock()
	return a.state.dropCredential()
}

// enroll reads the token source again and exchanges the token for a
// credential. After success it disables cloud-init (D-025).
func (a *Agent) enroll(ctx context.Context) error {
	token, err := a.readToken()
	if err != nil {
		return err
	}
	resp, err := a.client.enroll(ctx, wire.EnrollRequest{Token: token, Hostname: a.opt.Hostname, Version: a.opt.Version})
	if err != nil {
		return err
	}
	id, err := a.state.enrolled(resp.Credential, resp.Build)
	if err != nil {
		return fmt.Errorf("store credential: %w", err)
	}
	if resp.Build && !id.Build {
		a.log.Warn("enrollment answer says build, but this agent is not new; it will not seal")
	}
	a.credMu.Lock()
	a.cred = resp.Credential
	a.credMu.Unlock()
	if a.cfg.TokenEnv == "" { // a container has no cloud-init (host D5)
		if err := disableCloudInit(a.opt.CloudInitFlag); err != nil {
			a.log.Error("disable cloud-init", "err", err)
		}
	}
	a.log.Info("enrolled", "agent_id", resp.AgentID, "build", id.Build)
	return nil
}

func disableCloudInit(flag string) error {
	if err := os.MkdirAll(filepath.Dir(flag), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(flag, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// send sends a report with the current credential.
func (a *Agent) send(ctx context.Context, r wire.Report) error {
	return a.client.report(ctx, a.credential(), r)
}

// hello describes the agent. It holds the address list; when the list
// cannot be read, Addrs is nil and the controller treats the list as
// unknown (D-212).
func (a *Agent) hello() *wire.Hello {
	caps := a.caps
	h := &wire.Hello{Version: a.opt.Version, Hostname: a.opt.Hostname, OS: osName(a.opt.Root),
		BinarySHA256: a.binarySHA, Capabilities: &caps, Reverted: a.upd.reverted(), LiveWatch: a.net.liveWatch()}
	if l, ok := a.readAddrs(); ok {
		h.Addrs, h.AddrsTruncated = l.List, l.Truncated
	}
	return h
}

// readAddrs reads the address list. A failure is logged once, until a
// read works again. The log never holds an address.
func (a *Agent) readAddrs() (addrList, bool) {
	list, truncated, err := a.opt.Addrs()
	if err != nil {
		if !a.addrErr {
			a.log.Warn("cannot read the address list", "err", err)
		}
		a.addrErr = true
		return addrList{}, false
	}
	a.addrErr = false
	if list == nil {
		list = []string{} // a new agent always sends the field (D-212)
	}
	return addrList{List: list, Truncated: truncated}, true
}

// addrsTaken records that the controller took a list (in a Hello or an
// addrs report). A nil list (not read) records nothing.
func (a *Agent) addrsTaken(list []string, truncated bool, now time.Time) {
	if list == nil {
		a.addrKnown = false
		return
	}
	a.addrLast, a.addrKnown, a.addrAt = addrList{List: list, Truncated: truncated}, true, now
}

// sendAddrs sends an addrs report at a poll round when the list differs
// from the one the controller took last, or when that is AddrsEvery old;
// never twice within AddrsGap, and it reads the list at most once per
// AddrsGap (D-213). When the list cannot be read any more after the
// controller took one, it says "unknown" once, so no stale list stays
// (review N1). A busy controller (429, 503) is no connection error: the
// list goes again after AddrsGap, and the poll does not wait (review S2).
// A refused report (400) is not sent again until the list changes or
// AddrsEvery passed. Other errors go to the caller, like every failed call.
func (a *Agent) sendAddrs(ctx context.Context, now time.Time) error {
	gap := a.opt.AddrsGap
	if !a.addrTry.IsZero() && now.Sub(a.addrTry) < gap ||
		a.addrKnown && now.Sub(a.addrAt) < gap || // the Hello carried a list a moment ago
		!a.addrRead.IsZero() && now.Sub(a.addrRead) < gap {
		return nil
	}
	a.addrRead = now
	cur, ok := a.readAddrs()
	var rep wire.Report
	switch {
	case !ok && !a.addrKnown:
		return nil
	case !ok:
		rep = wire.Report{Type: wire.ReportAddrs, AddrsUnknown: true}
	case a.addrKnown && cur.equal(a.addrLast) && now.Sub(a.addrAt) < a.opt.AddrsEvery:
		return nil
	default:
		rep = wire.Report{Type: wire.ReportAddrs, Addrs: cur.List, AddrsTruncated: cur.Truncated}
	}
	a.addrTry = now
	err := a.send(ctx, rep)
	var busy *busyError
	switch {
	case err == nil:
	case errors.As(err, &busy):
		a.log.Info("address report not taken: controller busy; sent again later", "status", busy.Status)
		return nil
	case permanent(err):
		a.log.Warn("address report refused; not sent again until the list changes", "err", err)
	default:
		return err
	}
	if ok {
		a.addrsTaken(cur.List, cur.Truncated, now)
	} else {
		a.addrKnown = false
	}
	return nil
}

// osName reads ID and VERSION_ID from os-release, for example "ubuntu 26.04".
func osName(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "etc/os-release"))
	if err != nil {
		return ""
	}
	vals := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			vals[k] = strings.Trim(v, `"`)
		}
	}
	return strings.TrimSpace(vals["ID"] + " " + vals["VERSION_ID"])
}

// backoff is an exponential delay with jitter.
type backoff struct {
	min, max, cur time.Duration
}

func (b *backoff) reset() { b.cur = 0 }

// sleep waits the next backoff step, and at least atLeast.
func (b *backoff) sleep(ctx context.Context, atLeast time.Duration) {
	if b.cur == 0 {
		b.cur = b.min
	} else {
		b.cur = min(b.cur*2, b.max)
	}
	d := max(b.cur/2+rand.N(b.cur/2+1), atLeast)
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// agentEnv is the resource Env of the agent: the guest root, and for
// network the state dir and the controller host (D-348, D-349).
func agentEnv(opt Options, cfg Config) resource.Env {
	env := resource.NewEnv(opt.Root, opt.Runner)
	env.StateDir = cfg.StateDir
	if u, err := url.Parse(cfg.URL); err == nil {
		env.Controller = u.Hostname()
	}
	return env
}
