package link

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

func TestEnrollmentExpiryBoundary(t *testing.T) {
	for _, elapsed := range []time.Duration{TokenTTL - time.Nanosecond, TokenTTL} {
		t.Run(elapsed.String(), func(t *testing.T) {
			f := newFixture(t)
			tok := f.token("web1")
			f.clock.add(elapsed)
			code, _, body := f.enroll(tok)
			if elapsed < TokenTTL {
				if code != 200 {
					t.Fatalf("token expired early: %d %s", code, body)
				}
			} else if code != 401 || string(body) != "{\"error\":\"invalid enrollment token\"}\n" {
				t.Fatalf("expiry must use the uniform invalid-token response: %d %s", code, body)
			}
		})
	}
}

func TestConcurrentEnrollmentUsesTokenOnce(t *testing.T) {
	f := newFixture(t)
	tok := f.token("web1")
	start := make(chan struct{})
	codes := make(chan int, 8)
	for range cap(codes) {
		go func() {
			<-start
			code, _, _ := f.enroll(tok)
			codes <- code
		}()
	}
	close(start)
	accepted := 0
	for range cap(codes) {
		switch code := <-codes; code {
		case 200:
			accepted++
		case 401:
		default:
			t.Errorf("unexpected concurrent enrollment status: %d", code)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d enrollments with one token, want 1", accepted)
	}
}

func TestReenrollmentRevokesWaitingPoll(t *testing.T) {
	f := newFixture(t)
	cred := f.agent("web1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := httptest.NewRequest("GET", wire.PathPoll+"?wait=30", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+cred)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); f.srv.ServeHTTP(rec, req) }()
	defer func() { cancel(); <-done }()
	for !f.link.Online("web1") {
		select {
		case <-ctx.Done():
			t.Fatal("long poll did not begin")
		case <-time.After(time.Millisecond):
		}
	}
	f.token("web1") // Immediately revokes the old credential.
	if _, err := f.link.Send("web1", wire.CommandObserve, wire.ObserveBody{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if rec.Code != 401 {
			t.Fatalf("revoked credential received a later command: %d %s", rec.Code, rec.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("revoked poll did not return")
	}
}
