package link

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientIPBuckets(t *testing.T) {
	for remote, want := range map[string]string{
		"[2001:db8:1:2::1]:1":    "2001:db8:1:2::/64",
		"[2001:db8:1:2::abcd]:2": "2001:db8:1:2::/64",
		"[fe80::1%eth0]:3":       "fe80::/64",
		"[::ffff:192.0.2.1]:4":   "192.0.2.1",
		"192.0.2.1:5":            "192.0.2.1",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if got := clientIP(r); got != want {
			t.Errorf("clientIP(%q) = %q, want %q", remote, got, want)
		}
	}
	c := &clock{t: time.Now()}
	l := newLimiter(c.now, time.Hour, 1, 100)
	for i, remote := range []string{"[2001:db8:1:2::1]:1", "[2001:db8:1:2::2]:2", "[2001:db8:1:3::1]:3", "192.0.2.1:4", "192.0.2.2:5"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if got, want := l.blocked(r), i == 1; got != want {
			t.Errorf("blocked(%q) = %v, want %v", remote, got, want)
		}
		if got, want := l.allow(r), i != 1; got != want {
			t.Errorf("allow(%q) = %v, want %v", remote, got, want)
		}
	}
}
