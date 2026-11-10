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
	Store *store.Store
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
	wake        chan struct{} // closed and replaced when a command is queued
	outstanding map[string]bool
	sensitive   map[string][]string // resource id → sensitive attributes; a key means "known"
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
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	err := srv.Serve(tls.NewListener(ln, l.TLSConfig()))
	if errors.Is(err, http.ErrServerClosed) {
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
		ts.outstanding = map[string]bool{}
		ts.agentID = revokedAgent
		close(ts.wake) // waiting polls of the old agent end now (with 401)
		ts.wake = make(chan struct{})
	}
	l.mu.Unlock()
	l.emit(ctx, "target.status", target, "system", map[string]any{"agent": "token_created", "expires_at": expires.UTC()})
	return token, expires, nil
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
	agent, ok := l.checkCredential(r)
	if !ok {
		l.authFail.allow(r) // count the failure
		writeError(w, http.StatusUnauthorized, "unauthorized")
	}
	return agent, ok
}

func (l *Link) checkCredential(r *http.Request) (store.Agent, bool) {
	auth := r.Header.Get("Authorization")
	cred, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok || len(cred) > 256 {
		return store.Agent{}, false
	}
	id, sec, ok := splitCredential(cred)
	want := dummyHash
	var agent store.Agent
	if ok {
		a, err := l.cfg.Store.AgentByID(r.Context(), id)
		if err == nil {
			agent, want = a, a.CredentialHash
		}
	}
	got := hashSecret(sec)
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 || agent.ID == "" {
		return store.Agent{}, false
	}
	return agent, true
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
	ts.outstanding = map[string]bool{}
	ts.agentID = agent.ID
	l.mu.Unlock()
	l.emit(r.Context(), "target.status", agent.Target, "agent:"+agent.Target,
		map[string]any{"agent": "enrolled", "agent_id": agent.ID, "hostname": agent.Hostname, "version": agent.Version})
	writeJSON(w, http.StatusOK, wire.EnrollResponse{AgentID: agent.ID, Credential: agent.ID + "." + cred})
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

// target returns the state of a target. The caller holds l.mu.
func (l *Link) target(name string) *targetState {
	ts, ok := l.targets[name]
	if !ok {
		ts = &targetState{wake: make(chan struct{}), outstanding: map[string]bool{},
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
		writeError(w, http.StatusUnauthorized, "unauthorized") // re-enrolled meanwhile
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
			ts.outstanding[cmd.ID] = true
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
// wire.ObserveBody, wire.ApplyBody or wire.WatchBody. The agent gets it at
// its next poll.
func (l *Link) Send(target string, typ wire.CommandType, body any) (string, error) {
	switch typ {
	case wire.CommandObserve, wire.CommandApply, wire.CommandWatch:
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
	}
	ts.queue = append(ts.queue, wire.Command{ID: id, Type: typ, Body: raw})
	close(ts.wake)
	ts.wake = make(chan struct{})
	return id, nil
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
	if err := validateReport(rep); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
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
	if rep.Type == wire.ReportResult || rep.Type == wire.ReportProgress {
		if !ts.outstanding[rep.CommandID] {
			l.mu.Unlock()
			writeError(w, http.StatusConflict, "unknown command id")
			return
		}
		if rep.Type == wire.ReportResult {
			delete(ts.outstanding, rep.CommandID)
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
			l.log.Error("report handler", "target", agent.Target, "err", err)
			if rep.Type == wire.ReportResult {
				l.mu.Lock()
				l.target(agent.Target).outstanding[rep.CommandID] = true // the retry must be accepted
				l.mu.Unlock()
			}
			writeError(w, http.StatusServiceUnavailable, "try again")
			return
		}
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

// validateReport checks a report against the wire types and limits.
func validateReport(rep wire.Report) error {
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
		if _, _, ok := wire.SplitID(it.Resource); !ok || len(it.Resource) > 4096 {
			return fmt.Errorf("item %d: bad resource id", i)
		}
		if !agentStatuses[it.Status] {
			return fmt.Errorf("item %d: bad status %q", i, it.Status)
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
