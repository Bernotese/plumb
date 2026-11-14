// Package link is the controller side of the agent protocol: the agent
// listener, the Plumb CA, enrollment and the command queue
// (docs/ARCHITECTURE.md §5, §7). Agent input is untrusted: every body has
// a size limit and a strict decoder, and every change is redacted before
// it leaves this package.
package link

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// TokenTTL is how long an enrollment token is valid.
const TokenTTL = 15 * time.Minute

// Delivery is one validated, redacted report of an agent.
type Delivery struct {
	Target  string
	AgentID string
	Report  wire.Report
}

// Config configures a Link.
type Config struct {
	// Dir holds the CA and the listener certificate. It is created with
	// mode 0700. Key files have mode 0600.
	Dir string
	// Hosts are the host names and IPs of the agent listener certificate.
	Hosts []string
	// Restored reports restored mode (D-303): Send refuses every command
	// but observe while it is true. nil: never.
	Restored func() bool
	Store    *store.Store
	// Events gets the enrollment events. It may be nil in tests.
	Events *event.Log
	// AgentBinary is served at GET /agent/v1/binary. The plumb binary
	// embeds it; nil means 404.
	AgentBinary []byte
	// Sensitive returns the sensitive attributes of a resource in the head
	// revision. known is false when the head revision has no such
	// resource (or no revision exists). It must survive a restart, so it
	// reads the store, never this package's memory. nil knows nothing
	// (D-062).
	Sensitive func(ctx context.Context, target, resourceID string) (attrs []string, known bool, err error)
	// SecretValues returns the clear secret values of the head render of a
	// target. link masks them, and the values it sent, as substrings in
	// every Output, Error and change value of a report, so the masking
	// survives a restart (D-062). nil knows none.
	SecretValues func(ctx context.Context, target string) ([]string, error)
	// OnReport receives every report after validation and wire.Redact.
	// An error makes the agent retry the report later (503).
	OnReport func(ctx context.Context, d Delivery) error
	// DriftEvery and DriftBurst set the per-agent limit of drift reports
	// (D-366): one report per DriftEvery, burst DriftBurst. Zero: the
	// defaults DefaultDriftEvery and DefaultDriftBurst.
	DriftEvery time.Duration
	DriftBurst int
	// Now returns the time. Default time.Now.
	Now func() time.Time
	// Logger. Default slog.Default().
	Logger *slog.Logger
}

// Link is the agent side of the controller.
type Link struct {
	cfg    Config
	ca     *CA
	cert   tls.Certificate
	now    func() time.Time
	log    *slog.Logger
	binary *limiter
	enroll *limiter
	// authFail limits failed credential checks per client, so bad
	// bearers cannot make the controller do unbounded work.
	authFail *limiter
	drift    *driftLimiter // drift reports per agent (D-366)

	mu      sync.Mutex
	targets map[string]*targetState
}

// targetState is the in-memory state of one target: its command queue,
// the commands it has received and not answered, the sensitive attribute
// lists of the resources it was sent, and its last poll.
type targetState struct {
	// agentID is the agent that may take commands and send reports.
	// "" means unknown (after a controller restart): the store decides,
	// because a new token deletes the old agent row. revokedAgent means a
	// new token exists and no agent has enrolled with it yet.
	agentID     string
	queue       []wire.Command
	wake        chan struct{}               // closed and replaced when a command is queued
	outstanding map[string]wire.CommandType // command id → type, until its result
	// safe lists, per apply command, its resources with safe apply
	// (wire.SafeApplyTypes): a confirm report must name one (D-300).
	// Only applies that have one are in it; it ends with the result.
	safe      map[string][]string
	sensitive map[string][]string // resource id → sensitive attributes; a key means "known"
	// secrets holds the sensitive desired values that link sent, per
	// resource. They are masked in Output and Error (defence in depth).
	secrets  map[string][]string
	lastPoll time.Time
}

// New loads or creates the Plumb CA and the listener certificate.
func New(cfg Config) (*Link, error) {
	if cfg.Store == nil || cfg.Dir == "" {
		return nil, errors.New("link: Store and Dir are required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("link: %w", err)
	}
	if info, err := os.Stat(cfg.Dir); err != nil {
		return nil, fmt.Errorf("link: %w", err)
	} else if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("link: %s has mode %v; it must be 0700", cfg.Dir, info.Mode().Perm())
	}
	ca, err := loadOrCreateCA(cfg.Dir, cfg.Now())
	if err != nil {
		return nil, err
	}
	hosts := cfg.Hosts
	if len(hosts) == 0 {
		hosts = []string{"localhost", "127.0.0.1"}
	}
	cert, err := ca.serverCert(cfg.Dir, hosts, cfg.Now())
	if err != nil {
		return nil, err
	}
	return &Link{
		cfg: cfg, ca: ca, cert: cert, now: cfg.Now, log: cfg.Logger,
		// binary: 3 per client, then one per 20 s; enroll: 10, then one per 6 s.
		binary: newLimiter(cfg.Now, 20*time.Second, 3, 30),
		enroll: newLimiter(cfg.Now, 6*time.Second, 10, 100),
		// 20 failures per client, then one per 3 s.
		authFail: newLimiter(cfg.Now, 3*time.Second, 20, 200),
		drift:    newDriftLimiter(cfg.Now, cfg.DriftEvery, cfg.DriftBurst),
		targets:  map[string]*targetState{},
	}, nil
}

// CA returns the Plumb CA.
func (l *Link) CA() *CA { return l.ca }

// TLSConfig is the server TLS config of the agent listener.
func (l *Link) TLSConfig() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{l.cert}, MinVersion: tls.VersionTLS13}
}

// ClientTLSConfig is what an agent uses: only the Plumb CA is trusted.
func (l *Link) ClientTLSConfig() *tls.Config {
	return &tls.Config{RootCAs: l.ca.Pool(), MinVersion: tls.VersionTLS13}
}

// Serve runs the agent listener on its own address (port 8444) until ctx
// ends.
func (l *Link) Serve(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return l.ServeListener(ctx, ln)
}

// ServeListener is Serve on an open listener. It adds TLS.
func (l *Link) ServeListener(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           l.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      time.Duration(wire.MaxPollWait+15) * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(l.log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	// Serve returns as soon as Shutdown starts; ServeListener returns only
	// after Shutdown waited for the open requests (they use the store).
	shutDone := make(chan struct{})
	go func() {
		defer close(shutDone)
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shut); err != nil {
			srv.Close() // the open requests did not end in 5 s
		}
	}()
	err := srv.Serve(tls.NewListener(ln, l.TLSConfig()))
	if errors.Is(err, http.ErrServerClosed) {
		<-shutDone
		return nil
	}
	return err
}

// Handler serves /agent/v1/*.
func (l *Link) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+wire.PathEnroll, l.handleEnroll)
	mux.HandleFunc("GET "+wire.PathPoll, l.handlePoll)
	mux.HandleFunc("POST "+wire.PathReport, l.handleReport)
	mux.HandleFunc("GET "+wire.PathBinary, l.handleBinary)
	mux.HandleFunc("GET "+wire.PathInstallScript, l.handleInstallScript)
	return mux
}

// --- tokens and credentials ---

// secret returns n random bytes as unpadded base64url.
func secret(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system has no randomness; nothing is safe
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// NewEnrollToken creates a one-time token for a target: 256 random bits,
// valid for 15 minutes, stored only as a hash. The old token and the old
// credential of the target stop working at once (re-enrollment). The
// token is a live secret: give it only to the driver as Bootstrap.
func (l *Link) NewEnrollToken(ctx context.Context, target string) (token string, expires time.Time, err error) {
	if target == "" {
		return "", time.Time{}, errors.New("link: token needs a target")
	}
	token = secret(32)
	expires = l.now().Add(TokenTTL)
	if err := l.cfg.Store.PutEnrollToken(ctx, target, hashSecret(token), expires); err != nil {
		return "", time.Time{}, err
	}
	l.mu.Lock()
	if ts := l.target(target); ts != nil {
		ts.lastPoll = time.Time{}
		ts.resetOutstanding()
		ts.agentID = revokedAgent
		close(ts.wake) // waiting polls of the old agent end now (with 401)
		ts.wake = make(chan struct{})
	}
	l.mu.Unlock()
	l.emit(ctx, "target.status", target, "system", map[string]any{"agent": "token_created", "expires_at": expires.UTC()})
	return token, expires, nil
}

// Revoke removes the agent and the enrollment token of a target (Target
// delete, D-211). The old credential gets 401 on every route from now on,
// also after a restart (the store has no agent row). Queued commands are
// dropped, so no desired state reaches the old agent, and an open poll
// ends with 401.
func (l *Link) Revoke(ctx context.Context, target string) error {
	if target == "" {
		return errors.New("link: revoke needs a target")
	}
	if err := l.cfg.Store.RevokeAgent(ctx, target); err != nil {
		return err
	}
	l.mu.Lock()
	ts := l.target(target)
	ts.agentID = revokedAgent
	ts.queue = nil
	ts.resetOutstanding()
	ts.sensitive = map[string][]string{}
	ts.secrets = map[string][]string{}
	ts.lastPoll = time.Time{}
	close(ts.wake) // a waiting poll checks current() again and gets 401
	ts.wake = make(chan struct{})
	l.mu.Unlock()
	l.emit(ctx, "target.status", target, "system", map[string]any{"agent": "revoked"})
	return nil
}

// The credential is "<agent id>.<secret>". The id finds the row; the
// secret is compared by hash in constant time.
func splitCredential(c string) (id, sec string, ok bool) {
	id, sec, ok = strings.Cut(c, ".")
	return id, sec, ok && id != "" && sec != ""
}

// dummyHash is compared when no agent matches, so a wrong id costs the
// same time as a wrong secret.
var dummyHash = hashSecret("no agent")

// authenticate returns the agent or writes the authentication failure response.
func (l *Link) authenticate(w http.ResponseWriter, r *http.Request) (store.Agent, bool) {
	if l.authFail.blocked(r) {
		w.Header().Set("Retry-After", strconv.Itoa(int((l.authFail.rate+time.Second-1)/time.Second)))
		writeError(w, http.StatusTooManyRequests, "too many requests")
		return store.Agent{}, false
	}
	agent, ok, err := l.checkCredential(r)
	if err != nil {
		storeUnavailable(w)
		return store.Agent{}, false
	}
	if !ok {
		l.authFail.allow(r) // count the failure
		writeError(w, http.StatusUnauthorized, "unauthorized")
	}
	return agent, ok
}

func (l *Link) checkCredential(r *http.Request) (store.Agent, bool, error) {
	auth := r.Header.Get("Authorization")
	cred, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok || len(cred) > 256 {
		return store.Agent{}, false, nil
	}
	id, sec, ok := splitCredential(cred)
	want := dummyHash
	var agent store.Agent
	if ok {
		a, err := l.cfg.Store.AgentByID(r.Context(), id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return store.Agent{}, false, err
		}
		if err == nil {
			agent, want = a, a.CredentialHash
		}
	}
	got := hashSecret(sec)
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 || agent.ID == "" {
		return store.Agent{}, false, nil
	}
	return agent, true, nil
}

func storeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeError(w, http.StatusServiceUnavailable, "try again")
}

// --- handlers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// decodeStrict reads exactly one JSON value with no unknown fields.
func decodeStrict(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("data after the JSON value")
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("data after the JSON value")
	}
	return nil
}

const maxEnrollBytes = 4 << 10

func (l *Link) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if !l.enroll.allow(r) {
		writeError(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	var req wire.EnrollRequest
	if err := decodeStrict(http.MaxBytesReader(w, r.Body, maxEnrollBytes), &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad enroll request")
		return
	}
	if len(req.Hostname) > 253 || len(req.Version) > 64 || !printable(req.Hostname) || !printable(req.Version) {
		writeError(w, http.StatusBadRequest, "bad hostname or version")
		return
	}
	cred := secret(32)
	agent, err := l.cfg.Store.Enroll(r.Context(), hashSecret(req.Token), l.now(), store.Agent{
		ID: secret(12), CredentialHash: hashSecret(cred), Hostname: req.Hostname, Version: req.Version,
	})
	if errors.Is(err, store.ErrInvalidToken) {
		// Unknown, used, expired and foreign tokens get the same answer.
		writeError(w, http.StatusUnauthorized, "invalid enrollment token")
		return
	}
	if err != nil {
		l.log.Error("enroll", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	l.mu.Lock()
	ts := l.target(agent.Target)
	ts.resetOutstanding()
	ts.agentID = agent.ID
	l.mu.Unlock()
	l.emit(r.Context(), "target.status", agent.Target, "agent:"+agent.Target,
		map[string]any{"agent": "enrolled", "agent_id": agent.ID, "hostname": agent.Hostname, "version": agent.Version})
	// The build mark (D-207): only the token of a build link "build:<image>"
	// gets it, and only the build lane makes such a token (a Target name
	// cannot hold ':').
	writeJSON(w, http.StatusOK, wire.EnrollResponse{AgentID: agent.ID, Credential: agent.ID + "." + cred,
		Build: strings.HasPrefix(agent.Target, wire.BuildLinkPrefix)})
}

func printable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// revokedAgent is an agentID that no agent has.
const revokedAgent = "-"

// current reports whether the agent is the current agent of its target,
// and binds an unknown target to it. The caller holds l.mu. The check
// runs under the same lock as every dequeue, so a poll that started
// before a re-enrollment cannot take a command after it.
func (l *Link) current(ts *targetState, agentID string) bool {
	if ts.agentID == "" {
		ts.agentID = agentID // first call after a restart; authenticate checked the store
	}
	return ts.agentID == agentID
}

// resetOutstanding forgets the commands that the agent took. The safe
// lists of queued commands stay: a new agent polls them.
func (ts *targetState) resetOutstanding() {
	ts.outstanding = map[string]wire.CommandType{}
	safe := map[string][]string{}
	for _, c := range ts.queue {
		if ids, ok := ts.safe[c.ID]; ok {
			safe[c.ID] = ids
		}
	}
	ts.safe = safe
}

// target returns the state of a target. The caller holds l.mu.
func (l *Link) target(name string) *targetState {
	ts, ok := l.targets[name]
	if !ok {
		ts = &targetState{wake: make(chan struct{}), outstanding: map[string]wire.CommandType{}, safe: map[string][]string{},
			sensitive: map[string][]string{}, secrets: map[string][]string{}}
		l.targets[name] = ts
	}
	return ts
}

func (l *Link) handlePoll(w http.ResponseWriter, r *http.Request) {
	agent, ok := l.authenticate(w, r)
	if !ok {
		return
	}
	wait := 0
	if v := r.URL.Query().Get("wait"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad wait")
			return
		}
		wait = min(n, wire.MaxPollWait)
	}
	now := l.now()
	if err := l.cfg.Store.TouchAgent(r.Context(), agent.ID, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "unauthorized") // re-enrolled meanwhile
		} else {
			storeUnavailable(w)
		}
		return
	}
	timer := time.NewTimer(time.Duration(wait) * time.Second)
	defer timer.Stop()
	for {
		l.mu.Lock()
		ts := l.target(agent.Target)
		if !l.current(ts, agent.ID) {
			l.mu.Unlock()
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ts.lastPoll = now
		if len(ts.queue) > 0 {
			cmd := ts.queue[0]
			ts.queue = ts.queue[1:]
			ts.outstanding[cmd.ID] = cmd.Type
			l.mu.Unlock()
			writeJSON(w, http.StatusOK, cmd)
			return
		}
		wake := ts.wake
		l.mu.Unlock()
		select {
		case <-wake:
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

// Send queues a command for a target and returns its id. body is
// wire.ObserveBody, wire.ApplyBody, wire.WatchBody or wire.SealBody. The
// agent gets it at its next poll. seal goes only to the link of an image
// build guest (prefix wire.BuildLinkPrefix, D-207): one of the two
// independent guards that keep seal away from a real Target.
func (l *Link) Send(target string, typ wire.CommandType, body any) (string, error) {
	// Restored mode: the only command is observe (D-303).
	if typ != wire.CommandObserve && l.cfg.Restored != nil && l.cfg.Restored() {
		return "", fmt.Errorf("%w: no %s command to %s", ErrRestoredMode, typ, target)
	}
	build := strings.HasPrefix(target, wire.BuildLinkPrefix)
	switch typ {
	case wire.CommandObserve, wire.CommandApply:
	case wire.CommandWatch:
		// A build guest is no Target: it never watches (D-203), so its
		// agent may seal (D-207). (T-062 part B)
		if build {
			return "", fmt.Errorf("link: no watch for the build guest %q", target)
		}
	case wire.CommandSeal:
		if !build || len(target) == len(wire.BuildLinkPrefix) {
			return "", fmt.Errorf("link: seal only goes to an image build guest (link %q has no prefix %q)", target, wire.BuildLinkPrefix)
		}
		switch body.(type) {
		case wire.SealBody, *wire.SealBody:
		default:
			return "", fmt.Errorf("link: seal needs a wire.SealBody, got %T", body)
		}
	case wire.CommandGrow:
		// The root file system of a Target VM after a disk grow (D-299).
		if build {
			return "", fmt.Errorf("link: no grow for the build guest %q", target)
		}
		switch body.(type) {
		case wire.GrowBody, *wire.GrowBody:
		default:
			return "", fmt.Errorf("link: grow needs a wire.GrowBody, got %T", body)
		}
	case wire.CommandUpdate:
		// A build guest runs the agent of this controller and never updates
		// (T-092): its seal does not remove plumb-agent.prev.
		if build {
			return "", fmt.Errorf("link: no update for the build guest %q", target)
		}
		switch body.(type) {
		case wire.UpdateBody, *wire.UpdateBody:
		default:
			return "", fmt.Errorf("link: update needs a wire.UpdateBody, got %T", body)
		}
	default:
		return "", fmt.Errorf("link: unknown command type %q", typ)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	var withResources struct {
		Resources []wire.Resource `json:"resources"`
	}
	if err := json.Unmarshal(raw, &withResources); err != nil {
		return "", err
	}
	id := secret(12)
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := l.target(target)
	if typ == wire.CommandWatch {
		ts.sensitive = map[string][]string{} // the watch list is the full desired list
		ts.secrets = map[string][]string{}
	}
	withValues := wire.BodyResources(body) // json "-" fields are only here
	for i, res := range withResources.Resources {
		ts.sensitive[res.ID] = append([]string{}, res.Sensitive...)
		var values []string
		for _, attr := range res.Sensitive {
			if v, ok := res.Attrs[attr].(string); ok && len(v) >= minMaskLen {
				values = append(values, v)
			}
		}
		if i < len(withValues) && withValues[i].ID == res.ID {
			values = append(values, withValues[i].SecretValues...)
		}
		ts.secrets[res.ID] = values
		if typ == wire.CommandApply && slices.Contains(wire.SafeApplyTypes, res.Type) {
			ts.safe[id] = append(ts.safe[id], res.ID)
		}
	}
	ts.queue = append(ts.queue, wire.Command{ID: id, Type: typ, Body: raw})
	close(ts.wake)
	ts.wake = make(chan struct{})
	return id, nil
}

// Taken reports whether the agent of target took command id at a poll and
// sent no result for it yet. A command that waits in the queue, or whose
// result came in, is not taken (T-102).
func (l *Link) Taken(target, id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	ts, ok := l.targets[target]
	if !ok {
		return false
	}
	_, taken := ts.outstanding[id]
	return taken
}

// Online reports whether the target polled in the last 90 seconds.
func (l *Link) Online(target string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	ts, ok := l.targets[target]
	return ok && !ts.lastPoll.IsZero() && l.now().Sub(ts.lastPoll) < wire.OnlineSeconds*time.Second
}

// Limits of report contents, besides wire.MaxReportBytes.
const (
	maxItems      = 10000
	maxOutput     = 64 << 10
	maxAttrLen    = 256
	maxErrorLen   = 64 << 10
	maxHelloField = 253
)

// agentStatuses are the item states an agent may report.
var agentStatuses = map[wire.Status]bool{
	wire.StatusUnchanged: true, wire.StatusDiffers: true, wire.StatusRunning: true, wire.StatusApplied: true,
	wire.StatusFailed: true, wire.StatusRefused: true, wire.StatusSkipped: true,
}

func (l *Link) handleReport(w http.ResponseWriter, r *http.Request) {
	agent, ok := l.authenticate(w, r)
	if !ok {
		return
	}
	var rep wire.Report
	if err := decodeStrict(http.MaxBytesReader(w, r.Body, wire.MaxReportBytes), &rep); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "report too large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad report")
		return
	}
	// D-366: drift reports per agent, before any check, store read or
	// store write. The type needs the body: a peek at "type" scans the
	// whole body like the decode does, and must never read another type
	// than the decode, so the limit comes right after the decode. Only
	// drift counts; results and confirms of a Run are never limited.
	if rep.Type == wire.ReportDrift {
		if ok, retry, event := l.drift.take(agent.ID); !ok {
			if event {
				l.log.Warn("drift reports over the limit; agent told to wait", "target", agent.Target)
				l.emit(r.Context(), "agent.rate_limited", agent.Target, "agent:"+agent.Target, map[string]any{})
			}
			w.Header().Set("Retry-After", strconv.Itoa(int(retry/time.Second)))
			writeError(w, http.StatusTooManyRequests, "too many drift reports")
			return
		}
	}
	if err := validateReport(rep, strings.HasPrefix(agent.Target, wire.BuildLinkPrefix)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rep.Type == wire.ReportConfirm {
		l.confirm(w, agent, rep)
		return
	}
	// A bad address list in a Hello makes the list unknown; the Hello
	// itself is taken, so display data never cuts the link (D-212). The
	// log holds the reason, never the list.
	if h := rep.Hello; h != nil && h.Addrs != nil {
		if err := wire.CheckAddrs(h.Addrs, h.AddrsTruncated); err != nil {
			l.log.Warn("hello with a bad address list; list set to unknown", "target", agent.Target, "reason", err)
			h.Addrs, h.AddrsTruncated = nil, false
		}
	}

	// Source 1 of D-062: the head revision. It reads the store, so it runs
	// before l.mu is taken.
	type headList struct {
		attrs []string
		known bool
	}
	head := make([]headList, len(rep.Items))
	if l.cfg.Sensitive != nil {
		cache := map[string]headList{}
		for i, it := range rep.Items {
			if len(it.Changes) == 0 && it.Output == "" {
				continue
			}
			h, ok := cache[it.Resource]
			if !ok {
				attrs, known, err := l.cfg.Sensitive(r.Context(), agent.Target, it.Resource)
				if err != nil {
					l.log.Error("sensitive lookup", "target", agent.Target, "err", err)
					writeError(w, http.StatusServiceUnavailable, "try again")
					return
				}
				h = headList{attrs, known}
				cache[it.Resource] = h
			}
			head[i] = h
		}
	}

	// The secret values of the head render (D-062: no memory needed).
	var headValues []string
	if l.cfg.SecretValues != nil && reportHasText(rep) {
		v, err := l.cfg.SecretValues(r.Context(), agent.Target)
		if err != nil {
			l.log.Error("secret lookup", "target", agent.Target, "err", err)
			writeError(w, http.StatusServiceUnavailable, "try again")
			return
		}
		headValues = v
	}

	// A result or progress must answer a command that this target got.
	l.mu.Lock()
	ts := l.target(agent.Target)
	if !l.current(ts, agent.ID) {
		l.mu.Unlock()
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var cmdType wire.CommandType
	if rep.Type == wire.ReportResult || rep.Type == wire.ReportProgress {
		var known bool
		cmdType, known = ts.outstanding[rep.CommandID]
		if !known {
			l.mu.Unlock()
			writeError(w, http.StatusConflict, "unknown command id")
			return
		}
		if err := checkItemKinds(rep, cmdType); err != nil {
			l.mu.Unlock()
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if rep.Type == wire.ReportResult {
			delete(ts.outstanding, rep.CommandID)
			delete(ts.safe, rep.CommandID)
		}
	}
	// D-062: a change loses its values when the head revision, the sent
	// list or the agent marks it. A resource that neither the head nor the
	// sent list knows loses the values of every change.
	var secrets []string
	for i := range rep.Items {
		it := &rep.Items[i]
		sent, sentKnown := ts.sensitive[it.Resource]
		attrs := append(append([]string{}, head[i].attrs...), sent...)
		if !head[i].known && !sentKnown {
			attrs = attrs[:0]
			for _, c := range it.Changes {
				attrs = append(attrs, c.Attr)
			}
		}
		it.Changes = wire.Redact(attrs, it.Changes)
	}
	// Every value link sent to this target, for every resource: an agent
	// can print a secret in the output of another resource (T-029 QA).
	secrets = append(secrets, headValues...)
	for _, v := range ts.secrets {
		secrets = append(secrets, v...)
	}
	l.mu.Unlock()
	// Defence in depth (review T-009, T-029 QA): a secret value never
	// passes in Output, Error or a change value, also as a part of a longer
	// text, even if the agent has a bug.
	secrets = maskList(secrets)
	for i := range rep.Items {
		rep.Items[i].Output = mask(rep.Items[i].Output, secrets)
		for j := range rep.Items[i].Changes {
			c := &rep.Items[i].Changes[j]
			c.From, c.To = maskValue(c.From, secrets), maskValue(c.To, secrets)
		}
	}
	rep.Error = mask(rep.Error, secrets)

	if rep.Type == wire.ReportHello {
		if err := l.cfg.Store.UpdateAgentInfo(r.Context(), agent.ID, rep.Hello.Version, rep.Hello.Hostname); err != nil {
			l.log.Error("hello", "err", err)
		}
	}
	if l.cfg.OnReport != nil {
		if err := l.cfg.OnReport(r.Context(), Delivery{Target: agent.Target, AgentID: agent.ID, Report: rep}); err != nil {
			if errors.Is(err, ErrTooOften) {
				w.Header().Set("Retry-After", "30")
				writeError(w, http.StatusTooManyRequests, "address list changed too often")
				return
			}
			l.log.Error("report handler", "target", agent.Target, "err", err)
			if rep.Type == wire.ReportResult {
				l.mu.Lock()
				l.target(agent.Target).outstanding[rep.CommandID] = cmdType // the retry must be accepted
				l.mu.Unlock()
			}
			writeError(w, http.StatusServiceUnavailable, "try again")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// confirm answers the round trip of a safe apply (D-300): 204 when the
// command is an apply of this agent that has no result yet and the
// resource is one of its safe-apply resources, else 409. It stores and
// forwards nothing; the Run step stays running.
func (l *Link) confirm(w http.ResponseWriter, agent store.Agent, rep wire.Report) {
	l.mu.Lock()
	ts := l.target(agent.Target)
	if !l.current(ts, agent.ID) {
		l.mu.Unlock()
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	open := ts.outstanding[rep.CommandID] == wire.CommandApply &&
		slices.Contains(ts.safe[rep.CommandID], rep.Items[0].Resource)
	l.mu.Unlock()
	if !open {
		writeError(w, http.StatusConflict, "no open apply with this resource")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// minMaskLen is the shortest sensitive attribute value that link keeps
// for masking. A secret value itself is masked at every length: a short
// password blanks out ordinary text, which is safer than a leak.
const minMaskLen = 4

// maskList removes empty and duplicate values and sorts the longest
// first, so a value that contains another is masked whole.
func maskList(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// mask replaces every secret value in s.
func mask(s string, secrets []string) string {
	for _, v := range secrets {
		if s == "" {
			break
		}
		s = strings.ReplaceAll(s, v, "[redacted]")
	}
	return s
}

// maskValue masks every string inside a change value.
func maskValue(v any, secrets []string) any {
	switch v := v.(type) {
	case string:
		return mask(v, secrets)
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = maskValue(e, secrets)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = maskValue(e, secrets)
		}
		return out
	}
	return v
}

// reportHasText reports whether a report has an Output, an Error or a
// change value that a secret could be in.
func reportHasText(rep wire.Report) bool {
	if rep.Error != "" {
		return true
	}
	for _, it := range rep.Items {
		if it.Output != "" || len(it.Changes) > 0 {
			return true
		}
	}
	return false
}

// ErrRestoredMode: link sends no command but observe in restored mode
// (D-303).
var ErrRestoredMode = errors.New("link: restored mode")

// ErrTooOften is returned by OnReport for a report that changes stored
// data more often than its limit allows (an address list, D-212). link
// answers 429; the agent sends the data again later.
var ErrTooOften = errors.New("link: report changes data too often")

// validateReport checks a report against the wire types and limits.
// validateReport checks the form of a report. build: the agent is the
// agent of a build guest; only its results may hold seal Items (D-205).
// checkItemKinds then ties them to an outstanding seal command.
func validateReport(rep wire.Report, build bool) error {
	if rep.Type != wire.ReportAddrs && (rep.Addrs != nil || rep.AddrsTruncated || rep.AddrsUnknown) {
		return errors.New("addrs only in an addrs report")
	}
	if rep.Type != wire.ReportRepair && rep.Repair != nil {
		return errors.New("repair only in a repair report")
	}
	switch rep.Type {
	case wire.ReportResult, wire.ReportProgress:
		if rep.CommandID == "" {
			return fmt.Errorf("%s needs command_id", rep.Type)
		}
		if rep.Hello != nil {
			return errors.New("hello only in a hello report")
		}
	case wire.ReportDrift:
		if rep.CommandID != "" || rep.Hello != nil || len(rep.Items) == 0 {
			return errors.New("drift has items and no command_id")
		}
	case wire.ReportHello:
		h := rep.Hello
		if h == nil || rep.CommandID != "" || len(rep.Items) > 0 {
			return errors.New("hello has only the hello field")
		}
		for _, f := range []string{h.Version, h.Hostname, h.OS} {
			if len(f) > maxHelloField || !printable(f) {
				return errors.New("bad hello field")
			}
		}
	case wire.ReportConfirm:
		if rep.CommandID == "" || rep.Hello != nil || rep.Error != "" || len(rep.Items) != 1 {
			return errors.New("confirm has command_id and one item")
		}
		it := rep.Items[0]
		typ, _, _ := wire.SplitID(it.Resource)
		if !slices.Contains(wire.SafeApplyTypes, typ) || it.Status != wire.StatusRunning ||
			len(it.Changes) > 0 || it.Output != "" || it.RolledBack {
			return errors.New("confirm item: a safe-apply resource, running, nothing else")
		}
	case wire.ReportRepair:
		if rep.CommandID != "" || rep.Hello != nil || len(rep.Items) > 0 || rep.Error != "" {
			return errors.New("repair has only the repair field")
		}
		if err := wire.CheckRepairReport(rep.Repair); err != nil {
			return err
		}
	case wire.ReportAddrs:
		if rep.CommandID != "" || rep.Hello != nil || len(rep.Items) > 0 || rep.Error != "" {
			return errors.New("addrs has only the addrs fields")
		}
		if rep.AddrsUnknown && (rep.Addrs != nil || rep.AddrsTruncated) {
			return errors.New("addrs_unknown has no list")
		}
		if err := wire.CheckAddrs(rep.Addrs, rep.AddrsTruncated); err != nil {
			return err // the reason, never the list
		}
	default:
		return fmt.Errorf("unknown report type %q", rep.Type)
	}
	if len(rep.Error) > maxErrorLen {
		return errors.New("error text too long")
	}
	if len(rep.Items) > maxItems {
		return errors.New("too many items")
	}
	for i, it := range rep.Items {
		typ, _, ok := wire.SplitID(it.Resource)
		if !ok || len(it.Resource) > 4096 {
			return fmt.Errorf("item %d: bad resource id", i)
		}
		// Only the types that an agent manages. guest:config and
		// guest:address belong to the controller: an item of them from an
		// agent would open or close their Issues (T-070 review B1). A seal
		// step ("seal:<step>") only in a result; checkItemKinds then needs
		// a seal command for it.
		// The Item agent:binary only in a result; checkItemKinds then needs
		// an update command for it (T-092).
		// The Item grow:root only in a result of a grow command (D-299).
		if !wire.AgentType(typ) && !(typ == wire.SealItemType && build && rep.Type == wire.ReportResult) &&
			!(typ == wire.AgentItemType && !build && rep.Type == wire.ReportResult) &&
			!(typ == wire.GrowItemType && !build && rep.Type == wire.ReportResult) {
			return fmt.Errorf("item %d: resource type %q is not an agent resource", i, typ)
		}
		if !agentStatuses[it.Status] {
			return fmt.Errorf("item %d: bad status %q", i, it.Status)
		}
		// rolled_back only on a failed safe apply (D-300).
		if it.RolledBack && (it.Status != wire.StatusFailed || !slices.Contains(wire.SafeApplyTypes, typ)) {
			return fmt.Errorf("item %d: rolled_back only on a failed safe-apply resource", i)
		}
		if len(it.Output) > maxOutput {
			return fmt.Errorf("item %d: output too long", i)
		}
		for j, c := range it.Changes {
			if c.Attr == "" || len(c.Attr) > maxAttrLen {
				return fmt.Errorf("item %d change %d: bad attr", i, j)
			}
			if !c.Risk.Valid() {
				return fmt.Errorf("item %d change %d: bad risk %q", i, j, c.Risk)
			}
		}
	}
	return nil
}

// checkUpdateItems: the result of an update command has at most one Item,
// agent:binary, applied, unchanged or failed, without changes; an update
// has no progress (T-092, D-267). A result with an error and no Item is
// allowed (a bad body).
func checkUpdateItems(rep wire.Report) error {
	if rep.Type != wire.ReportResult {
		return errors.New("update has no progress reports")
	}
	if len(rep.Items) > 1 {
		return errors.New("update has one item")
	}
	for _, it := range rep.Items {
		if it.Resource != wire.AgentBinaryID || len(it.Changes) > 0 ||
			(it.Status != wire.StatusApplied && it.Status != wire.StatusUnchanged && it.Status != wire.StatusFailed) {
			return fmt.Errorf("bad update item %q %q", it.Resource, it.Status)
		}
	}
	return nil
}

// checkGrowItems: the result of a grow command has at most one Item,
// grow:root, applied, unchanged or failed, without changes, and no
// progress (D-299). A result with an error and no Item is allowed.
func checkGrowItems(rep wire.Report) error {
	if rep.Type != wire.ReportResult {
		return errors.New("grow has no progress reports")
	}
	if len(rep.Items) > 1 {
		return errors.New("grow has one item")
	}
	for _, it := range rep.Items {
		if it.Resource != wire.GrowItemID || len(it.Changes) > 0 ||
			(it.Status != wire.StatusApplied && it.Status != wire.StatusUnchanged && it.Status != wire.StatusFailed) {
			return fmt.Errorf("bad grow item %q %q", it.Resource, it.Status)
		}
	}
	return nil
}

// sealItemStatuses are the states of a seal step (wire.SealSteps).
var sealItemStatuses = map[wire.Status]bool{
	wire.StatusApplied: true, wire.StatusSkipped: true, wire.StatusFailed: true, wire.StatusRefused: true,
}

// checkItemKinds checks the items of a result or progress against the type
// of its command: a seal command has a result of seal steps only, each
// step at most once and without changes, and no progress; every other
// command has no seal step (review of #119).
func checkItemKinds(rep wire.Report, cmd wire.CommandType) error {
	switch cmd {
	case wire.CommandUpdate:
		return checkUpdateItems(rep)
	case wire.CommandGrow:
		return checkGrowItems(rep)
	}
	if cmd != wire.CommandSeal {
		for i, it := range rep.Items {
			switch typ, _, _ := wire.SplitID(it.Resource); typ {
			case wire.SealItemType:
				return fmt.Errorf("item %d: a seal step answers only a seal command", i)
			case wire.AgentItemType:
				return fmt.Errorf("item %d: %s answers only an update command", i, wire.AgentBinaryID)
			case wire.GrowItemType:
				return fmt.Errorf("item %d: %s answers only a grow command", i, wire.GrowItemID)
			}
		}
		return nil
	}
	if rep.Type != wire.ReportResult {
		return errors.New("seal has no progress reports")
	}
	steps := map[string]bool{}
	for _, s := range wire.SealSteps {
		steps[wire.SealItemID(s)] = true
	}
	seen := map[string]bool{}
	for i, it := range rep.Items {
		if !steps[it.Resource] || seen[it.Resource] {
			return fmt.Errorf("item %d: not a seal step, or a step twice", i)
		}
		seen[it.Resource] = true
		if !sealItemStatuses[it.Status] || len(it.Changes) > 0 {
			return fmt.Errorf("item %d: bad seal step state", i)
		}
	}
	return nil
}

func (l *Link) handleBinary(w http.ResponseWriter, r *http.Request) {
	if !l.binary.allow(r) {
		writeError(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	if l.cfg.AgentBinary == nil {
		writeError(w, http.StatusNotFound, "no agent binary in this build")
		return
	}
	sum := sha256.Sum256(l.cfg.AgentBinary)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(l.cfg.AgentBinary)))
	w.Header().Set("X-Plumb-Sha256", hex.EncodeToString(sum[:]))
	w.Write(l.cfg.AgentBinary)
}

func (l *Link) emit(ctx context.Context, typ, target, actor string, data any) {
	if l.cfg.Events == nil {
		return
	}
	if _, err := l.cfg.Events.Emit(ctx, typ, "target:"+target, actor, data); err != nil {
		l.log.Error("event", "type", typ, "err", err)
	}
}
