// Package api serves the public HTTP API and the web UI: handlers,
// sessions, API tokens and the event stream (docs/ARCHITECTURE.md §4, §7,
// api/openapi.yaml). The words and ids are those of the database and the
// wire (D-045); there is no mapping layer.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Config configures the API server.
type Config struct {
	Store  *store.Store
	Events *event.Log
	Plans  *plan.Engine
	Issues *issue.Service
	// Backups writes and lists the backups (T-112). nil: the backup
	// routes answer 503.
	Backups *backup.Service
	// Version is the Plumb version, for the export header.
	Version string
	// Secrets is the encrypted secret store (required).
	Secrets *secret.Store
	// UI is the built web UI (index.html, assets/…). nil: no UI.
	UI fs.FS
	// Dev: plain HTTP. The session cookie has no Secure flag and the
	// expected Origin scheme is http (D-039). Never on a real host.
	Dev bool
	// DevLoginToken enables GET /dev/login (D-080). It counts only with
	// Dev; on every other server the route does not exist.
	DevLoginToken  string
	PasswordParams PasswordParams
	SessionTTL     time.Duration // default 12 h
	KeepAlive      time.Duration // SSE comment interval, default 15 s
	Now            func() time.Time
	Logger         *slog.Logger
}

// Server is the API.
type Server struct {
	cfg Config
	st  *store.Store
	log *slog.Logger
	now func() time.Time
	// Failed logins (review T-011 M1): 5/min per user and client, 20/min
	// per client, and 100/min per known user. Nobody can lock out the real
	// user from another client by guessing at 5/min.
	loginPair *failLimiter
	loginIP   *failLimiter
	loginUser *failLimiter
	// bearers counts bad bearers per client and token id (20/min) while the
	// key is kept; eviction can drop it, so the guaranteed bound is bearerIP.
	bearers  *failLimiter
	bearerIP *failLimiter // bad bearers per client, hard cap 300/min (D-157)
	dummy    string       // a password hash for unknown users (same work)
	// hashSlots bounds concurrent argon2id work (64 MiB each, review M2).
	hashSlots chan struct{}

	streamMu  sync.Mutex
	streams   map[string]map[uint64]context.CancelFunc // open event streams per principal (review m2)
	streamSeq uint64
}

// Limits of expensive or long-lived work.
const (
	hashSlotCount       = 4
	maxStreamsPerCaller = 8
	maxStreams          = 64
)

// New returns the server.
func New(cfg Config) *Server {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	if cfg.KeepAlive == 0 {
		cfg.KeepAlive = 15 * time.Second
	}
	if cfg.PasswordParams == (PasswordParams{}) {
		cfg.PasswordParams = DefaultPasswordParams
	}
	return &Server{
		cfg: cfg, st: cfg.Store, log: cfg.Logger, now: cfg.Now,
		loginPair: newFailLimiter(cfg.Now, 5, time.Minute),
		loginIP:   newFailLimiter(cfg.Now, 20, time.Minute),
		loginUser: newFailLimiter(cfg.Now, 100, time.Minute),
		bearers:   newFailLimiter(cfg.Now, 20, time.Minute),
		bearerIP:  newFailLimiter(cfg.Now, 300, time.Minute),
		dummy:     HashPassword(randomText(16), cfg.PasswordParams),
		hashSlots: make(chan struct{}, hashSlotCount),
		streams:   map[string]map[uint64]context.CancelFunc{},
	}
}

// --- principals and access rules ---

// principal is who calls.
type principal struct {
	name       string // user:<name> | token:<id>
	tier       string
	maxRisk    wire.Risk // apply tokens only
	user       *store.User
	session    *store.Session
	token      *store.Token
	mustChange bool
}

func (p *principal) human() bool { return p.session != nil }

// access is the rule of one operation.
type access struct {
	tier        string // minimum tier; "" = no authentication (login)
	sessionOnly bool   // humans only
	duringReset bool   // allowed while the password must change (the /session operations)
}

// The session cookie. Outside --dev it has the __Host- prefix, so the
// browser itself enforces Secure, Path=/ and no Domain (review T-011 n1).
const (
	sessionCookie    = "plumb_session"
	sessionCookieTLS = "__Host-plumb_session"
)

func (s *Server) cookieName() string {
	if s.cfg.Dev {
		return sessionCookie
	}
	return sessionCookieTLS
}

// withHashSlot runs fn in one of the few argon2id slots. ok is false when
// all slots are busy; the caller answers 429 at once.
func (s *Server) withHashSlot(fn func()) (ok bool) {
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
		fn()
		return true
	default:
		return false
	}
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, p *principal)

// Handler returns the API and the UI.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	route := func(pattern string, a access, h handlerFunc) {
		mux.Handle(pattern, s.guard(a, h))
	}
	read, planTier, apply := access{tier: TierRead}, access{tier: TierPlan}, access{tier: TierApply}
	self := access{tier: TierRead, duringReset: true}
	human := access{tier: TierAdmin, sessionOnly: true, duringReset: true}
	admin := access{tier: TierAdmin, sessionOnly: true}

	route("GET /api/v1/session", self, s.getSession)
	route("POST /api/v1/session", access{}, s.login)
	route("DELETE /api/v1/session", human, s.logout)
	route("PUT /api/v1/session/password", human, s.changePassword)

	route("GET /api/v1/images", read, s.listDocs("Image"))
	route("GET /api/v1/images/{name}", read, s.getDoc("Image"))
	route("POST /api/v1/images/{name}/build", planTier, s.buildImage)
	route("GET /api/v1/blocks", read, s.listDocs("Block"))
	route("GET /api/v1/blocks/{name}", read, s.getDoc("Block"))
	route("GET /api/v1/rules", read, s.listDocs("Rule"))
	route("GET /api/v1/rules/{name}", read, s.getDoc("Rule"))
	route("GET /api/v1/targets", read, s.listDocs("Target"))
	route("GET /api/v1/targets/{name}", read, s.getDoc("Target"))
	route("POST /api/v1/targets/{name}/reenroll", planTier, s.reenroll)
	route("POST /api/v1/targets/{name}/agent-update", planTier, s.agentUpdate)

	route("GET /api/v1/plans", read, s.listPlans)
	route("POST /api/v1/plans", planTier, s.createPlan)
	route("GET /api/v1/plans/{id}", read, s.getPlan)
	route("DELETE /api/v1/plans/{id}", planTier, s.discardPlan)
	route("POST /api/v1/plans/{id}/approve", apply, s.approvePlan)

	route("GET /api/v1/runs", read, s.listRuns)
	route("GET /api/v1/runs/{id}", read, s.getRun)

	route("GET /api/v1/issues", read, s.listIssues)
	route("GET /api/v1/issues/{id}", read, s.getIssue)
	route("POST /api/v1/issues/{id}/resolve", planTier, s.resolveIssue)
	route("POST /api/v1/issues/{id}/comments", planTier, s.commentIssue)

	route("GET /api/v1/secrets", read, s.listSecrets)
	route("PUT /api/v1/secrets/{name}", admin, s.putSecret)
	route("DELETE /api/v1/secrets/{name}", admin, s.deleteSecret)

	route("GET /api/v1/revisions", read, s.listRevisions)
	route("GET /api/v1/revisions/{n}/diff", read, s.diffRevision)
	route("POST /api/v1/revisions/{n}/rollback", planTier, s.rollbackRevision)

	route("GET /api/v1/export", read, s.exportState)
	route("POST /api/v1/export", admin, s.exportStateWithSecrets)
	route("POST /api/v1/import", admin, s.importState)

	route("GET /api/v1/events", read, s.events)

	route("GET /api/v1/restore", read, s.getRestore)
	route("POST /api/v1/restore/confirm", human, s.confirmRestore)
	route("POST /api/v1/tokens/{id}/enable", human, s.enableToken)

	route("GET /api/v1/backups", read, s.listBackups)
	route("POST /api/v1/backups", apply, s.createBackup)

	route("GET /api/v1/tokens", admin, s.listTokens)
	route("POST /api/v1/tokens", admin, s.createToken)
	route("DELETE /api/v1/tokens/{id}", admin, s.revokeToken)

	if s.cfg.Dev && s.cfg.DevLoginToken != "" {
		mux.HandleFunc("GET /dev/login", s.devLogin)
	}
	// Reserve dev routes so disabled endpoints cannot fall back to the SPA.
	mux.HandleFunc("/dev/", http.NotFound)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such API operation.")
	})
	mux.HandleFunc("/", s.ui)
	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.ServeHTTP(w, r)
	})
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "ip:" + r.RemoteAddr
	}
	return "ip:" + host
}

// guard authenticates the caller and enforces the access rule.
func (s *Server) guard(a access, h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.tier == "" {
			h(w, r, nil)
			return
		}
		p, err := s.authenticate(r)
		if errors.Is(err, errRateLimited) {
			w.Header().Set("Retry-After", "60")
			problem(w, http.StatusTooManyRequests, "rate-limited", "Too many attempts", "Too many failed authentications. Try again later.")
			return
		}
		if p == nil {
			problem(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", "")
			return
		}
		// A session sends a cookie by itself, so every change it makes
		// must come from our own origin (CSRF). A bearer token does not.
		if p.human() && !safeMethod(r.Method) && !s.sameOrigin(r) {
			problem(w, http.StatusForbidden, "origin-mismatch", "Origin mismatch", "The Origin header must match the server.")
			return
		}
		if a.sessionOnly && !p.human() {
			problem(w, http.StatusForbidden, "human-required", "Human session required", "This operation is for humans only; no token can call it.")
			return
		}
		if p.mustChange && !a.duringReset {
			problem(w, http.StatusForbidden, "password-change-required", "Password change required", "Change the password first (PUT /api/v1/session/password).")
			return
		}
		if tierLevel[p.tier] < tierLevel[a.tier] {
			problem(w, http.StatusForbidden, "forbidden", "Forbidden", "This operation needs tier "+a.tier+"; the caller has "+p.tier+".")
			return
		}
		h(w, r, p)
	})
}

var errRateLimited = errors.New("rate limited")

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin compares the Origin header with the server's own origin.
func (s *Server) sameOrigin(r *http.Request) bool {
	scheme := "https"
	if s.cfg.Dev {
		scheme = "http"
	}
	origin := r.Header.Get("Origin")
	return origin != "" && origin == scheme+"://"+r.Host
}

// authenticate returns the principal of a bearer token or a session
// cookie, or nil.
func (s *Server) authenticate(r *http.Request) (*principal, error) {
	ctx := r.Context()
	if auth := r.Header.Get("Authorization"); auth != "" {
		bearer, ok := strings.CutPrefix(auth, "Bearer ")
		id, sec, ok2 := strings.Cut(bearer, ".")
		wellFormed := ok && ok2 && len(bearer) < 256
		// The pair key uses the presented id, not the stored token, so a
		// blocked id gives no oracle for which ids exist (D-157).
		client := clientKey(r)
		pair := client + " id:"
		if wellFormed {
			pair += id
		}
		if s.bearerIP.blocked(client) > 0 || s.bearers.blocked(pair) > 0 {
			return nil, errRateLimited
		}
		want := sha256Hex("no token")
		var tok store.Token
		if wellFormed {
			if t, err := s.st.TokenByID(ctx, id); err == nil {
				tok, want = t, t.Hash
			}
		}
		// A token that a restore disabled gets 401 until a person enables
		// it (D-303); it counts like a bad bearer.
		if subtle.ConstantTimeCompare([]byte(sha256Hex(sec)), []byte(want)) != 1 || tok.ID == "" || !tok.DisabledAt.IsZero() {
			s.bearers.fail(pair)
			s.bearerIP.fail(client)
			return nil, nil
		}
		s.st.TouchToken(ctx, tok.ID)
		return &principal{name: "token:" + tok.ID, tier: tok.Tier, maxRisk: wire.Risk(tok.MaxRisk), token: &tok}, nil
	}
	c, err := r.Cookie(s.cookieName())
	if err != nil || c.Value == "" || len(c.Value) > 256 {
		return nil, nil
	}
	sess, err := s.st.SessionByHash(ctx, sha256Hex(c.Value), s.now())
	if err != nil {
		return nil, nil
	}
	u, err := s.st.UserByID(ctx, sess.UserID)
	if err != nil || !u.LockedAt.IsZero() {
		return nil, nil
	}
	return &principal{name: "user:" + u.Name, tier: TierAdmin, user: &u, session: &sess, mustChange: u.MustChangePassword}, nil
}

// --- responses ---

// problem writes an RFC 9457 problem.
func problem(w http.ResponseWriter, status int, code, title, detail string) {
	writeProblem(w, problemBody{Type: "urn:plumb:problem:" + code, Title: title, Status: status, Detail: detail})
}

type fieldError struct {
	Pointer string `json:"pointer"`
	Message string `json:"message"`
}

type problemBody struct {
	Type   string       `json:"type"`
	Title  string       `json:"title"`
	Status int          `json:"status"`
	Detail string       `json:"detail,omitempty"`
	Errors []fieldError `json:"errors,omitempty"`
	// secret-in-use (D-087)
	Targets []string `json:"targets,omitempty"`
	Plans   []int64  `json:"plans,omitempty"`
	Secrets []string `json:"secrets,omitempty"` // missing secrets of a 422
	Missing []string `json:"missing,omitempty"` // agent-outdated (D-268)
}

func writeProblem(w http.ResponseWriter, p problemBody) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(p.Status)
	json.NewEncoder(w).Encode(p)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

// maxBody is the largest request body.
const maxBody = 4 << 20

// decode reads one strict JSON body. An empty body is allowed when
// optional is true.
func decode(w http.ResponseWriter, r *http.Request, v any, optional bool) bool {
	body := http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	dec.UseNumber()
	err := dec.Decode(v)
	if errors.Is(err, io.EOF) && optional {
		return true
	}
	if err == nil && dec.More() {
		err = errors.New("data after the JSON value")
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			problem(w, http.StatusRequestEntityTooLarge, "too-large", "Request too large", "The body is larger than 4 MiB.")
			return false
		}
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "Request body is not valid: "+err.Error())
		return false
	}
	return true
}

// ui serves the embedded web UI. Unknown paths get index.html, so the
// single-page app can route them.
func (s *Server) ui(w http.ResponseWriter, r *http.Request) {
	if s.cfg.UI == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" && name != "index.html" {
		if info, err := fs.Stat(s.cfg.UI, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.cfg.UI, name)
			return
		}
	}
	index, err := fs.ReadFile(s.cfg.UI, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(index)
}
