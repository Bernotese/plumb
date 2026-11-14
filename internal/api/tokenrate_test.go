package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// contractRates returns "METHOD /api/v1<path>" → x-plumb-rate ("" without
// a mark) for every operation of the contract.
func contractRates(t *testing.T, k *contract) map[string]string {
	t.Helper()
	out := map[string]string{}
	for path, item := range k.doc["paths"].(map[string]any) {
		for _, m := range []string{"get", "post", "put", "patch", "delete"} {
			op, ok := item.(map[string]any)[m].(map[string]any)
			if !ok {
				continue
			}
			mark, _ := op["x-plumb-rate"].(string)
			out[strings.ToUpper(m)+" /api/v1"+path] = mark
		}
	}
	return out
}

// D-365: the middleware's kinds are exactly the contract's marks, both
// ways; a mark without a limit, or a limit without a mark, fails.
func TestTokenRateKindsMatchContract(t *testing.T) {
	marks := contractRates(t, loadContract(t))
	for op, mark := range marks {
		if got := tokenRateKinds[op]; got != mark {
			t.Errorf("%s: middleware kind %q, contract x-plumb-rate %q", op, got, mark)
		}
		if mark != "" {
			if _, ok := tokenRateLimits[mark]; !ok {
				t.Errorf("%s: unknown kind %q", op, mark)
			}
		}
	}
	for op := range tokenRateKinds {
		if _, ok := marks[op]; !ok {
			t.Errorf("%s: a kind for an operation the contract does not have", op)
		}
	}
	for op := range tokenRateUnmarkedGET {
		if marks[op] != "" {
			t.Errorf("%s: exempt, but the contract marks it", op)
		}
	}
}

// The rules of the marks (ARCHITECTURE §18): every GET that a token can
// call has read, except the event stream; every other operation that a
// token can call and that answers with a Plan has proposal.
func TestTokenRateMarkRules(t *testing.T) {
	k := loadContract(t)
	marks := contractRates(t, k)
	paths := k.doc["paths"].(map[string]any)
	for _, ep := range endpoints {
		if ep.human {
			continue // no token can call it
		}
		path, _, _ := strings.Cut(ep.path, "?")
		op, mark := "", ""
		for o, m := range marks {
			if pathMatches(o, ep.method+" "+path) {
				op, mark = o, m
			}
		}
		if op == "" {
			t.Errorf("%s %s: not in the contract", ep.method, ep.path)
			continue
		}
		switch {
		case op == "GET /api/v1/events":
			if mark != "" {
				t.Errorf("%s: the event stream is not limited (D-365)", op)
			}
		case ep.method == "GET":
			if mark != rateRead {
				t.Errorf("%s: a token GET needs x-plumb-rate read, has %q", op, mark)
			}
		default:
			method, p, _ := strings.Cut(op, " ")
			o := paths[strings.TrimPrefix(p, "/api/v1")].(map[string]any)[strings.ToLower(method)]
			raw, _ := json.Marshal(o.(map[string]any)["responses"])
			if bytes.Contains(raw, []byte(`"#/components/schemas/Plan"`)) && mark != rateProposal {
				t.Errorf("%s: a token operation that makes a Plan needs x-plumb-rate proposal, has %q", op, mark)
			}
		}
	}
}

// pathMatches compares a pattern with {params} and a concrete path.
func pathMatches(pattern, concrete string) bool {
	want, got := strings.Split(pattern, "/"), strings.Split(concrete, "/")
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] && !strings.HasPrefix(want[i], "{") {
			return false
		}
	}
	return true
}

// A GET route without a mark that is not exempt stops Handler.
func TestTokenRateUnmarkedGETPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("an unmarked GET route was accepted")
		}
	}()
	newTokenRates(time.Now).wrap(nil, "GET /api/v1/new-thing", nil)
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) add(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }
func (c *fakeClock) takeN(r *tokenRates, tok, kind string, n int) (ok int) {
	for range n {
		if o, _, _ := r.take(tok, kind); o {
			ok++
		}
	}
	return ok
}

// The numbers of D-365 per kind, and the refill.
func TestTokenRateBuckets(t *testing.T) {
	c := &fakeClock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	r := newTokenRates(c.Now)
	for _, k := range []struct {
		kind  string
		burst int
		retry time.Duration
	}{{rateProposal, 30, 2 * time.Second}, {rateComment, 30, 2 * time.Second}, {rateRead, 120, time.Second}} {
		if got := c.takeN(r, "tok_a", k.kind, k.burst+5); got != k.burst {
			t.Fatalf("%s: %d taken, want burst %d", k.kind, got, k.burst)
		}
		ok, retry, _ := r.take("tok_a", k.kind)
		if ok || retry != k.retry {
			t.Fatalf("%s: over the limit: ok %v retry %v, want %v", k.kind, ok, retry, k.retry)
		}
		// Another token has its own bucket.
		if ok, _, _ := r.take("tok_b", k.kind); !ok {
			t.Fatalf("%s: another token is limited", k.kind)
		}
	}
	// One minute refills 30 proposals and 600 reads (capped at the burst).
	c.add(time.Minute)
	if got := c.takeN(r, "tok_a", rateProposal, 40); got != 30 {
		t.Fatalf("proposals after a minute: %d, want 30", got)
	}
	if got := c.takeN(r, "tok_a", rateRead, 700); got != 120 {
		t.Fatalf("reads after a minute: %d, want the burst 120", got)
	}
	// Sustained: 600 reads per minute.
	n := 0
	for range 600 {
		c.add(100 * time.Millisecond)
		n += c.takeN(r, "tok_a", rateRead, 1)
	}
	if n != 600 {
		t.Fatalf("sustained reads: %d of 600", n)
	}
}

// At most one token.rate_limited event per token per 10 minutes, across
// kinds.
func TestTokenRateEventGap(t *testing.T) {
	c := &fakeClock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	r := newTokenRates(c.Now)
	c.takeN(r, "tok_a", rateProposal, 30)
	c.takeN(r, "tok_a", rateComment, 30)
	events := 0
	for range 5 {
		for _, kind := range []string{rateProposal, rateComment} {
			if _, _, ev := r.take("tok_a", kind); ev {
				events++
			}
		}
	}
	if events != 1 {
		t.Fatalf("%d events, want 1", events)
	}
	c.add(9 * time.Minute)
	c.takeN(r, "tok_a", rateProposal, 30)
	if _, _, ev := r.take("tok_a", rateProposal); ev {
		t.Fatal("an event within 10 minutes")
	}
	c.add(time.Minute)
	c.takeN(r, "tok_a", rateProposal, 30)
	if _, _, ev := r.take("tok_a", rateProposal); !ev {
		t.Fatal("no event after 10 minutes")
	}
}

// The memory bound: a full map drops the buckets that are full again.
func TestTokenRatePrune(t *testing.T) {
	c := &fakeClock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	r := newTokenRates(c.Now)
	for i := range maxTokenRateKeys {
		r.take("tok_"+strconv.Itoa(i), rateRead)
	}
	c.add(time.Minute)
	r.take("tok_new", rateRead)
	if len(r.buckets) != 1 {
		t.Fatalf("%d buckets after the prune, want 1", len(r.buckets))
	}
}

// Through the API: a plan token gets 429 rate-limited with Retry-After
// after its proposals, nothing is done, the answer echoes nothing, and
// one event names the token and the kind; a human session sends more and
// is never limited; another kind of the same token still works.
func TestTokenRateHTTP(t *testing.T) {
	e := newEnv(t, true)
	tok := e.token(TierPlan, "")
	const mark = "ECHO-MARK-7f3a"
	body := map[string]any{"reason": mark, "put": []any{"not a document"}}
	limited := 0
	var rec429 []byte
	var retry string
	for i := range 40 {
		rec := tok.raw("POST", "/api/v1/plans", body)
		if rec.Code == 429 {
			limited, rec429, retry = i, rec.Body.Bytes(), rec.Header().Get("Retry-After")
			break
		}
		if rec.Code != 400 && rec.Code != 422 {
			t.Fatalf("proposal %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if limited < 30 {
		t.Fatalf("no 429 after 40 proposals, or before 30 (%d)", limited)
	}
	if problemType(rec429) != "rate-limited" || bytes.Contains(rec429, []byte(mark)) {
		t.Fatalf("429 body %s", rec429)
	}
	if s, err := strconv.Atoi(retry); err != nil || s < 1 || s > 2 {
		t.Fatalf("Retry-After %q", retry)
	}
	// Reads and comments are other buckets.
	if code, body := tok.do("GET", "/api/v1/plans", nil); code != 200 {
		t.Fatalf("a read after the proposal limit: %d %s", code, body)
	}
	if code, _ := tok.do("POST", "/api/v1/issues/999999/comments", map[string]string{"body": "x"}); code == 429 {
		t.Fatal("a comment after the proposal limit")
	}

	// One event, with the token and the kind.
	sub := e.events.Subscribe(e.ctx, 0)
	var got []string
	timeout := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case ev := <-sub.C:
			if ev.Type == "token.rate_limited" {
				got = append(got, fmt.Sprintf("%s %s %s", ev.Subject, ev.Actor, ev.Data))
				if bytes.Contains(ev.Data, []byte(mark)) {
					t.Errorf("the event holds the request: %s", ev.Data)
				}
			}
		case <-timeout:
			break loop
		}
	}
	tokID, _, _ := strings.Cut(tok.bearer, ".")
	if len(got) != 1 || !strings.HasPrefix(got[0], "token:"+tokID+" token:"+tokID) || !strings.Contains(got[0], `"kind":"proposal"`) {
		t.Fatalf("events %v", got)
	}

	// A human session is not limited by this bucket.
	adm := e.admin()
	for i := range 45 {
		if code, b := adm.do("POST", "/api/v1/plans", body); code == 429 {
			t.Fatalf("session proposal %d: 429 %s", i, b)
		}
	}
	for i := range 200 {
		if code, b := adm.do("GET", "/api/v1/session", nil); code == 429 {
			t.Fatalf("session read %d: 429 %s", i, b)
		}
	}

	// Reads of a read token: limited after the burst of 120.
	rd := e.token(TierRead, wire.Risk(""))
	n := 0
	for ; n < 400; n++ {
		if code, _ := rd.do("GET", "/api/v1/session", nil); code == 429 {
			break
		}
	}
	if n < 120 || n == 400 {
		t.Fatalf("read limit after %d reads, want after the burst of 120", n)
	}
	// The event stream is never limited.
	if code, _ := rd.do("GET", "/api/v1/events?since=-1", nil); code == 429 {
		t.Fatal("the event stream is limited")
	}
}
