package link

import (
	"math"
	"sync"
	"time"
)

// Per-agent limit of drift reports (D-366, T-180). Back-pressure, never a
// drop: over the limit the link answers 429 with Retry-After, keeps the
// credential and stores nothing of the report. The agent keeps every
// unsent drift per resource (the latest state wins) and sends it again
// (#272 F1, F2; internal/agent/drift_busy_test.go). Only drift reports
// count: results, progress, confirms, hellos and address reports never do.

// Defaults of the drift limit: 120 reports per minute, burst 240. The
// numbers may only grow (D-366).
const (
	DefaultDriftEvery = time.Minute / 120
	DefaultDriftBurst = 240
	// driftEventGap: at most one agent.rate_limited event per agent.
	driftEventGap = 10 * time.Minute
	// maxDriftAgents bounds the memory; idle full buckets go first.
	maxDriftAgents = 10000
)

type driftLimiter struct {
	mu     sync.Mutex
	now    func() time.Time
	every  time.Duration // one token per every
	burst  int
	agents map[string]*driftBucket
}

type driftBucket struct {
	bucket
	lastEvent time.Time
}

func newDriftLimiter(now func() time.Time, every time.Duration, burst int) *driftLimiter {
	if every <= 0 {
		every = DefaultDriftEvery
	}
	if burst <= 0 {
		burst = DefaultDriftBurst
	}
	return &driftLimiter{now: now, every: every, burst: burst, agents: map[string]*driftBucket{}}
}

// take takes a token for one drift report of agent. When none is left it
// returns the wait until the next token (at least one second) and whether
// an agent.rate_limited event is due.
func (d *driftLimiter) take(agent string) (ok bool, retry time.Duration, event bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	b, found := d.agents[agent]
	if !found {
		if len(d.agents) >= maxDriftAgents {
			full := time.Duration(d.burst) * d.every
			for k, v := range d.agents {
				if now.Sub(v.last) > full {
					delete(d.agents, k)
				}
			}
		}
		// Only an authenticated agent gets here, so the map stays as
		// small as the agent table even when no bucket was idle.
		b = &driftBucket{bucket: bucket{tokens: float64(d.burst)}}
		d.agents[agent] = b
	}
	if b.take(now, d.every, d.burst) {
		return true, 0, false
	}
	wait := time.Duration(math.Ceil((1-b.tokens)*float64(d.every)/float64(time.Second))) * time.Second
	if wait < time.Second {
		wait = time.Second
	}
	if b.lastEvent.IsZero() || now.Sub(b.lastEvent) >= driftEventGap {
		b.lastEvent = now
		event = true
	}
	return false, wait, event
}
