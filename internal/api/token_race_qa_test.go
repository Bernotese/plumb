package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Force revoke between authentication and stream registration.
func TestQARevokeBeforeStreamRegistration(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.srv.cfg.KeepAlive = time.Hour
	ct := e.createToken(adm, map[string]any{"name": "watcher", "tier": "read"})
	authenticated, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h := e.srv.guard(access{tier: TierRead}, func(w http.ResponseWriter, r *http.Request, p *principal) {
		close(authenticated)
		<-resume
		e.srv.events(w, r, p)
	})
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/v1/events", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+ct.Bearer)
	go func() { h.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	<-authenticated
	code, _ := adm.do("DELETE", "/api/v1/tokens/"+ct.Token.ID, nil)
	close(resume)
	if code != 204 {
		cancel()
		<-done
		t.Fatal(code)
	}
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("revoked token starts a stream after revoke; it lasts until keep-alive")
	}
}
