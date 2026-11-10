package link

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// limiter is a token bucket per client IP plus one global bucket. It
// protects the calls that need no credential (binary, enroll).
type limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	rate    time.Duration // one token per rate
	burst   int
	global  bucket
	gburst  int
	clients map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(now func() time.Time, rate time.Duration, burst, globalBurst int) *limiter {
	return &limiter{now: now, rate: rate, burst: burst, gburst: globalBurst, clients: map[string]*bucket{},
		global: bucket{tokens: float64(globalBurst)}}
}

func (b *bucket) take(now time.Time, rate time.Duration, burst int) bool {
	if !b.last.IsZero() {
		b.tokens += float64(now.Sub(b.last)) / float64(rate)
		if b.tokens > float64(burst) {
			b.tokens = float64(burst)
		}
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// allow reports whether the request may run now.
func (l *limiter) allow(r *http.Request) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	c, ok := l.clients[ip]
	if !ok {
		if len(l.clients) > 10000 { // bound the memory; forget idle clients
			for k, v := range l.clients {
				if now.Sub(v.last) > time.Duration(l.burst)*l.rate {
					delete(l.clients, k)
				}
			}
		}
		c = &bucket{tokens: float64(l.burst)}
		l.clients[ip] = c
	}
	// The client bucket is checked first, so one client cannot drain the
	// global bucket after its own is empty.
	if !c.take(now, l.rate, l.burst) {
		return false
	}
	return l.global.take(now, l.rate/time.Duration(max(l.gburst/l.burst, 1)), l.gburst)
}
