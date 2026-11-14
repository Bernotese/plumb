package api

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Per-token rate limit (D-365, T-179). One in-memory token bucket per API
// token and kind; the kind of an operation is its `x-plumb-rate` mark in
// api/openapi.yaml (tokenRateKinds; a test keeps both equal). Over the
// limit: 429 rate-limited with Retry-After, the handler does not run.
// Human sessions are never limited here (the login limit stays), and an
// operation without a mark (the event stream, approve, discard, the
// admin operations) is not limited.

// The kinds of x-plumb-rate.
const (
	rateProposal = "proposal"
	rateComment  = "comment"
	rateRead     = "read"
)

// tokenRateLimit is one kind's bucket: one token per every, burst at most.
type tokenRateLimit struct {
	every time.Duration
	burst int
}

// tokenRateLimits are the numbers of D-365. They may only grow.
var tokenRateLimits = map[string]tokenRateLimit{
	rateProposal: {time.Minute / 30, 30},
	rateComment:  {time.Minute / 30, 30},
	rateRead:     {time.Minute / 600, 120},
}

// tokenRateKinds maps each route pattern to its x-plumb-rate mark. It
// holds the planned operations too, so a card that adds the route is
// limited at once. TestTokenRateKindsMatchContract compares it with the
// contract both ways.
var tokenRateKinds = map[string]string{
	"GET /api/v1/session":                      rateRead,
	"GET /api/v1/catalog/blocks":               rateRead,
	"GET /api/v1/images":                       rateRead,
	"GET /api/v1/images/{name}":                rateRead,
	"POST /api/v1/images/{name}/refresh":       rateProposal,
	"POST /api/v1/images/{name}/build":         rateProposal,
	"GET /api/v1/blocks":                       rateRead,
	"GET /api/v1/blocks/{name}":                rateRead,
	"GET /api/v1/targets":                      rateRead,
	"GET /api/v1/targets/{name}":               rateRead,
	"POST /api/v1/targets/{name}/reenroll":     rateProposal,
	"POST /api/v1/targets/{name}/repair":       rateProposal,
	"POST /api/v1/targets/{name}/adopt":        rateProposal,
	"GET /api/v1/guests/unmanaged":             rateRead,
	"POST /api/v1/targets/{name}/agent-update": rateProposal,
	"GET /api/v1/plans":                        rateRead,
	"POST /api/v1/plans":                       rateProposal,
	"GET /api/v1/plans/{id}":                   rateRead,
	"GET /api/v1/runs":                         rateRead,
	"GET /api/v1/runs/{id}":                    rateRead,
	"GET /api/v1/issues":                       rateRead,
	"GET /api/v1/issues/{id}":                  rateRead,
	"POST /api/v1/issues/{id}/resolve":         rateProposal,
	"POST /api/v1/issues/{id}/comments":        rateComment,
	"GET /api/v1/rules":                        rateRead,
	"GET /api/v1/rules/{name}":                 rateRead,
	"GET /api/v1/revisions":                    rateRead,
	"GET /api/v1/revisions/{n}/diff":           rateRead,
	"POST /api/v1/revisions/{n}/rollback":      rateProposal,
	"GET /api/v1/secrets":                      rateRead,
	"GET /api/v1/export":                       rateRead,
	"GET /api/v1/backups":                      rateRead,
	"GET /api/v1/restore":                      rateRead,
	"GET /api/v1/settings":                     rateRead,
}

// tokenRateUnmarkedGET are the GET routes without a mark (D-365: every
// token GET except the event stream). Any other GET route without a mark
// stops Handler, so a new read cannot skip the limit.
var tokenRateUnmarkedGET = map[string]bool{
	"GET /api/v1/events": true,
	"GET /api/v1/users":  true, // admin session only
	"GET /api/v1/tokens": true, // admin session only
}

const (
	// tokenRateEventGap: at most one token.rate_limited event per token.
	tokenRateEventGap = 10 * time.Minute
	// maxTokenRateKeys bounds the memory; idle full buckets go first.
	// Only an authenticated token gets a bucket, so the map stays near
	// the size of the token table even when none is idle.
	maxTokenRateKeys = 30000
)

type tokenRates struct {
	mu        sync.Mutex
	now       func() time.Time
	buckets   map[string]*rateBucket // token id + "\x00" + kind
	lastEvent map[string]time.Time   // token id
}

type rateBucket struct {
	tokens float64
	last   time.Time
}

func newTokenRates(now func() time.Time) *tokenRates {
	return &tokenRates{now: now, buckets: map[string]*rateBucket{}, lastEvent: map[string]time.Time{}}
}

// take takes one request of kind for token. When the bucket is empty it
// returns the wait until the next request (whole seconds, at least one)
// and whether a token.rate_limited event is due.
func (t *tokenRates) take(token, kind string) (ok bool, retry time.Duration, event bool) {
	lim := tokenRateLimits[kind]
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	key := token + "\x00" + kind
	b, found := t.buckets[key]
	if !found {
		if len(t.buckets) >= maxTokenRateKeys {
			t.prune(now)
		}
		b = &rateBucket{tokens: float64(lim.burst), last: now}
		t.buckets[key] = b
	}
	b.tokens = math.Min(float64(lim.burst), b.tokens+float64(now.Sub(b.last))/float64(lim.every))
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0, false
	}
	retry = time.Duration(math.Ceil((1-b.tokens)*float64(lim.every)/float64(time.Second))) * time.Second
	retry = max(retry, time.Second)
	if last, seen := t.lastEvent[token]; !seen || now.Sub(last) >= tokenRateEventGap {
		t.lastEvent[token] = now
		event = true
	}
	return false, retry, event
}

// prune drops the buckets that are full again, and old event marks.
func (t *tokenRates) prune(now time.Time) {
	for k, b := range t.buckets {
		_, kind, _ := strings.Cut(k, "\x00")
		lim := tokenRateLimits[kind]
		if now.Sub(b.last) >= time.Duration(lim.burst)*lim.every {
			delete(t.buckets, k)
		}
	}
	for k, at := range t.lastEvent {
		if now.Sub(at) >= tokenRateEventGap {
			delete(t.lastEvent, k)
		}
	}
}

// wrap limits the route pattern by its mark. It panics for a GET route
// without a mark that is not on the exempt list (a programming error that
// every test run finds).
func (t *tokenRates) wrap(s *Server, pattern string, h handlerFunc) handlerFunc {
	kind, marked := tokenRateKinds[pattern]
	if !marked {
		if strings.HasPrefix(pattern, "GET /api/") && !tokenRateUnmarkedGET[pattern] {
			panic(fmt.Sprintf("api: %s has no x-plumb-rate kind (D-365)", pattern))
		}
		return h
	}
	return func(w http.ResponseWriter, r *http.Request, p *principal) {
		if p == nil || p.human() || p.token == nil {
			h(w, r, p)
			return
		}
		ok, retry, event := t.take(p.token.ID, kind)
		if ok {
			h(w, r, p)
			return
		}
		if event && s.cfg.Events != nil {
			if _, err := s.cfg.Events.Emit(r.Context(), "token.rate_limited", "token:"+p.token.ID, p.name,
				map[string]any{"kind": kind}); err != nil {
				s.log.Error("token.rate_limited event", "err", err)
			}
		}
		w.Header().Set("Retry-After", strconv.Itoa(int(retry/time.Second)))
		problem(w, http.StatusTooManyRequests, "rate-limited", "Too many requests",
			"This API token sent too many "+kind+" requests. Try again in "+strconv.Itoa(int(retry/time.Second))+" s.")
	}
}
