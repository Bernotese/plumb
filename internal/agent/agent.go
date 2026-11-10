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

	mu       sync.Mutex
	desired  []wire.Resource
	applying int                  // applies in progress
	open     map[string]wire.Item // drift that is on the guest now
	dirty    map[string]bool      // drift items not yet sent
	resolved map[string]wire.Item // drift that went away, not yet sent

	listChanged chan struct{} // the desired list changed
	applyDone   chan []string // an apply ended; ids of its resources
	sendMu      sync.Mutex    // one drift report at a time
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
	go a.driftLoop(ctx, w)

	b := backoff{min: a.opt.BackoffMin, max: a.opt.BackoffMax}
	connected := false
	for ctx.Err() == nil {
		if a.credential() == "" {
			if err := a.enroll(ctx); err != nil {
				a.log.Warn("enroll failed", "err", err)
				b.sleep(ctx)
				continue
			}
		}
		if !connected {
			// After start and after every reconnect: hello, then all open drift.
			if err := a.send(ctx, wire.Report{Type: wire.ReportHello, Hello: a.hello()}); err != nil {
				a.connError(ctx, err, &b)
				continue
			}
			if err := a.flushDrift(ctx, true); err != nil {
				a.connError(ctx, err, &b)
				continue
			}
			connected = true
			b.reset()
		}
		cmd, err := a.client.poll(ctx, a.credential(), a.opt.PollWait)
		if err != nil {
			connected = false
			a.connError(ctx, err, &b)
			continue
		}
		b.reset()
		if cmd == nil {
			continue
		}
		rep := a.handle(ctx, *cmd)
		if err := a.send(ctx, rep); err != nil {
			connected = false
			a.connError(ctx, err, &b)
		}
	}
	return ctx.Err()
}

// connError handles a failed call: a 401 drops the credential, so the next
// round enrolls with a fresh token. Every error waits with backoff.
func (a *Agent) connError(ctx context.Context, err error, b *backoff) {
	if ctx.Err() != nil {
		return
	}
	if errors.Is(err, errUnauthorized) {
		a.log.Warn("credential refused; enrolling again")
		if derr := a.dropCredential(); derr != nil {
			a.log.Error("drop credential", "err", derr)
		}
	} else {
		a.log.Warn("controller call failed", "err", err)
	}
	b.sleep(ctx)
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

func (a *Agent) hello() *wire.Hello {
	return &wire.Hello{Version: a.opt.Version, Hostname: a.opt.Hostname, OS: osName(a.opt.Root)}
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

func (b *backoff) sleep(ctx context.Context) {
	if b.cur == 0 {
		b.cur = b.min
	} else {
		b.cur = min(b.cur*2, b.max)
	}
	d := b.cur/2 + rand.N(b.cur/2+1)
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
