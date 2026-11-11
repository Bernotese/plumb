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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	// reports; default 30 s, the store limit of the controller (D-212).
	AddrsEvery, AddrsGap time.Duration
}

// Agent is one agent process.
type Agent struct {
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

	mu       sync.Mutex
	desired  []wire.Resource
	applying int                  // applies in progress
	open     map[string]wire.Item // drift that is on the guest now
	dirty    map[string]bool      // drift items not yet sent
	resolved map[string]wire.Item // drift that went away, not yet sent

	watcher     *watcher      // set by Run (tests read it)
	listChanged chan struct{} // the desired list changed
	applyDone   chan []string // an apply ended; ids of its resources
	sendMu      sync.Mutex    // one drift report at a time

	// Address report state; only the Run goroutine uses it.
	addrLast  addrList  // the list the controller took last
	addrKnown bool      // addrLast is set
	addrAt    time.Time // when the controller took addrLast
	addrTry   time.Time // last address report, sent or not
	addrErr   bool      // the last read failed (log once)
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
		opt.AddrsGap = 30 * time.Second
	}
	c, err := newClient(cfg, time.Duration(opt.PollWait)*time.Second)
	if err != nil {
		return nil, err
	}
	a := &Agent{
		opt: opt, cfg: cfg, client: c,
		state:       stateDir{dir: cfg.StateDir},
		env:         resource.NewEnv(opt.Root, opt.Runner),
		log:         opt.Log,
		open:        map[string]wire.Item{},
		dirty:       map[string]bool{},
		resolved:    map[string]wire.Item{},
		listChanged: make(chan struct{}, 1),
		applyDone:   make(chan []string, 64),
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
	w, err := newWatcher()
	if err != nil {
		return fmt.Errorf("inotify: %w", err)
	}
	defer w.close()
	a.mu.Lock()
	a.watcher = w
	a.mu.Unlock()
	go a.driftLoop(ctx, w)

	b := backoff{min: a.opt.BackoffMin, max: a.opt.BackoffMax}
	connected := false
	// pending is a command result that the controller has not accepted
	// yet. It is sent again until a call succeeds; after a 401 the link has
	// forgotten the command, so the result is dropped.
	var pending *wire.Report
	for ctx.Err() == nil {
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
			if err := a.flushDrift(ctx, true); err != nil {
				connected = a.connError(ctx, err, &b, connected)
				continue
			}
			// The backoff is reset only after a successful poll or result:
			// an error after a working hello must not loop at BackoffMin.
			connected = true
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
			pending = nil
			b.reset()
		}
		if err := a.sendAddrs(ctx, time.Now()); err != nil {
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
	b, err := os.ReadFile(a.cfg.TokenFile)
	if err != nil {
		return fmt.Errorf("read token: %w", err)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return errors.New("token source is empty")
	}
	resp, err := a.client.enroll(ctx, wire.EnrollRequest{Token: token, Hostname: a.opt.Hostname, Version: a.opt.Version})
	if err != nil {
		return err
	}
	if err := a.state.saveCredential(resp.Credential); err != nil {
		return fmt.Errorf("store credential: %w", err)
	}
	a.credMu.Lock()
	a.cred = resp.Credential
	a.credMu.Unlock()
	if err := disableCloudInit(a.opt.CloudInitFlag); err != nil {
		a.log.Error("disable cloud-init", "err", err)
	}
	a.log.Info("enrolled", "agent_id", resp.AgentID)
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
	h := &wire.Hello{Version: a.opt.Version, Hostname: a.opt.Hostname, OS: osName(a.opt.Root)}
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
// never twice within AddrsGap (D-213). The controller may drop a list over
// its own limit; the periodic send heals that. A refused report (400) is
// not sent again until the list changes or AddrsEvery passed. Other errors
// go to the caller, like every failed call.
func (a *Agent) sendAddrs(ctx context.Context, now time.Time) error {
	if !a.addrTry.IsZero() && now.Sub(a.addrTry) < a.opt.AddrsGap {
		return nil
	}
	if a.addrKnown && now.Sub(a.addrAt) < a.opt.AddrsGap {
		return nil // the Hello carried a list a moment ago
	}
	cur, ok := a.readAddrs()
	if !ok {
		return nil
	}
	if a.addrKnown && cur.equal(a.addrLast) && now.Sub(a.addrAt) < a.opt.AddrsEvery {
		return nil
	}
	a.addrTry = now
	err := a.send(ctx, wire.Report{Type: wire.ReportAddrs, Addrs: cur.List, AddrsTruncated: cur.Truncated})
	if err != nil && permanent(err) {
		a.log.Warn("address report refused; not sent again until the list changes", "err", err)
		err = nil
	}
	if err == nil {
		a.addrsTaken(cur.List, cur.Truncated, now)
	}
	return err
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
