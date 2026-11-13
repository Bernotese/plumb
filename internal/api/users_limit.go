package api

import (
	"sync"
	"time"
)

// backoffLimiter limits failed logins per (name, client) with a growing
// delay and a fixed end (D-330, risks.md §1 worst case 5): the first free
// failures cost nothing; then each failure blocks the key for base,
// 2·base, 4·base … up to max. A key with no failure for forget is
// dropped, so a lock never lasts: the longest wait is max, and a client
// that stops guessing is free after forget. A good login resets the key.
type backoffLimiter struct {
	mu     sync.Mutex
	now    func() time.Time
	free   int
	base   time.Duration
	max    time.Duration
	forget time.Duration
	keys   map[string]*backoff
}

type backoff struct {
	fails int
	last  time.Time
	until time.Time
}

func newBackoffLimiter(now func() time.Time, free int, base, max, forget time.Duration) *backoffLimiter {
	return &backoffLimiter{now: now, free: free, base: base, max: max, forget: forget, keys: map[string]*backoff{}}
}

// blocked returns the time to wait, or 0.
func (l *backoffLimiter) blocked(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.keys[key]
	if !ok {
		return 0
	}
	now := l.now()
	if now.Sub(b.last) >= l.forget && !now.Before(b.until) {
		delete(l.keys, key)
		return 0
	}
	if wait := b.until.Sub(now); wait > 0 {
		return wait
	}
	return 0
}

func (l *backoffLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.keys[key]
	if ok && now.Sub(b.last) >= l.forget && !now.Before(b.until) {
		ok = false
	}
	if !ok {
		l.bound(now)
		b = &backoff{}
		l.keys[key] = b
	}
	b.fails++
	b.last = now
	if b.fails >= l.free {
		d := l.max
		if shift := b.fails - l.free; shift < 30 {
			d = min(l.base<<shift, l.max)
		}
		b.until = now.Add(d)
	}
}

func (l *backoffLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.keys, key)
}

// bound keeps at most maxLimiterKeys keys: first the ones that ended,
// then the oldest.
func (l *backoffLimiter) bound(now time.Time) {
	if len(l.keys) < maxLimiterKeys {
		return
	}
	for k, b := range l.keys {
		if now.Sub(b.last) >= l.forget && !now.Before(b.until) {
			delete(l.keys, k)
		}
	}
	for len(l.keys) >= maxLimiterKeys {
		var oldest string
		var at time.Time
		for k, b := range l.keys {
			if oldest == "" || b.last.Before(at) {
				oldest, at = k, b.last
			}
		}
		delete(l.keys, oldest)
	}
}
