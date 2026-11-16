package agent

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func TestAgentWithRealLink(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "link.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var offset atomic.Int64
	now := func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	st.Now = now
	deliveries := make(chan link.Delivery, 32)
	ctl, err := link.New(link.Config{Dir: filepath.Join(t.TempDir(), "link"), Store: st, Now: now,
		OnReport: func(_ context.Context, d link.Delivery) error { deliveries <- d; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	type attempt struct {
		at          time.Time
		code        int
		retry, cred string
	}
	attempts := make(chan attempt, 64)
	handler := ctl.Handler()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at := time.Now()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r) // Every response comes from the real link.
		for key, values := range rec.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		attempts <- attempt{at, rec.Code, rec.Header().Get("Retry-After"), r.Header.Get("Authorization")}
	}))
	srv.TLS = ctl.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	token := func() string {
		t.Helper()
		tok, _, err := ctl.NewEnrollToken(ctx, "web1")
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	// Reuse only the temp guest/agent fixture; its controller is the real link.
	ta := startAgent(t, &controller{srv: srv}, testCA{pem: string(ctl.CA().CertPEM)}, token())
	receive := func(typ wire.ReportType, id string) wire.Report {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case d := <-deliveries:
				if d.Target != "web1" {
					t.Fatalf("wrong delivery target: %s", d.Target)
				}
				if d.Report.Type == typ && d.Report.CommandID == id {
					return d.Report
				}
			case <-deadline:
				t.Fatalf("no %s report for %q", typ, id)
			}
		}
	}
	receive(wire.ReportHello, "")
	r := fileRes("integration", "/srv/linked", map[string]any{"content": "hello"})
	id, err := ctl.Send("web1", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{r}, Planned: unverified(r), MaxRisk: wire.RiskReversible})
	if err != nil {
		t.Fatal(err)
	}
	if rep := receive(wire.ReportResult, id); rep.Error != "" || len(rep.Items) != 1 || rep.Items[0].Status != wire.StatusApplied {
		t.Fatalf("apply result: %+v", rep)
	}
	// Fail the real TouchAgent write, then recover without re-enrollment.
	// The agent keeps polling, so the link may hold the write lock right
	// now: the second connection waits for it (T-251).
	db := openRawDB(t, dbPath)
	if _, err := db.Exec(`CREATE TRIGGER fail_touch BEFORE UPDATE OF last_seen_at ON agent BEGIN SELECT RAISE(FAIL, 'private store failure'); END`); err != nil {
		t.Fatal(err)
	}
	cred := ta.a.credential()
	var busy attempt
	deadline := time.After(5 * time.Second)
waitBusy:
	for {
		select {
		case busy = <-attempts:
			if busy.code == http.StatusServiceUnavailable {
				break waitBusy
			}
		case <-deadline:
			t.Fatal("no 503 from failed TouchAgent")
		}
	}
	if busy.retry != "1" {
		t.Fatalf("503 Retry-After: %q", busy.retry)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_touch`); err != nil {
		t.Fatal(err)
	}
	id, err = ctl.Send("web1", wire.CommandObserve, wire.ObserveBody{Resources: []wire.Resource{r}})
	if err != nil {
		t.Fatal(err)
	}
	receive(wire.ReportResult, id)
	if ta.a.credential() != cred {
		t.Fatal("store failure dropped credential")
	}
	old := ta.a.credential()
	write(t, ta.cfg.TokenFile, token()) // Revokes the old credential and wakes its poll with 401.
	receive(wire.ReportHello, "")
	current := ta.a.credential()
	if current == "" || current == old {
		t.Fatal("401 did not replace the credential")
	}
	// Exhaust the real failed-auth bucket on the agent's source IP.
	for range 25 {
		req := httptest.NewRequest("GET", wire.PathPoll, nil)
		req.RemoteAddr = "127.0.0.1:1"
		req.Header.Set("Authorization", "Bearer bad.credential")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	next := func() attempt {
		t.Helper()
		select {
		case got := <-attempts:
			return got
		case <-time.After(5 * time.Second):
			t.Fatal("agent did not retry")
		}
		return attempt{}
	}
	limited := next()
	for limited.code != http.StatusTooManyRequests {
		limited = next()
	}
	retry := next()
	seconds, err := strconv.Atoi(limited.retry)
	if err != nil || seconds <= 0 {
		t.Fatalf("link returned invalid Retry-After %q", limited.retry)
	}
	if delay := retry.at.Sub(limited.at); delay < time.Duration(seconds)*time.Second {
		t.Errorf("429 retry after %v ignores Retry-After: %s", delay, limited.retry)
	}
	if retry.cred != "Bearer "+current || ta.a.credential() != current {
		t.Fatal("429 dropped the valid credential")
	}
	offset.Store(int64(3 * time.Second)) // Refill the real limiter without waiting on the wall clock.
	id, err = ctl.Send("web1", wire.CommandObserve, wire.ObserveBody{Resources: []wire.Resource{r}})
	if err != nil {
		t.Fatal(err)
	}
	if rep := receive(wire.ReportResult, id); rep.Error != "" || len(rep.Items) != 1 || rep.Items[0].Status != wire.StatusUnchanged {
		t.Fatalf("recovered observe: %+v", rep)
	}
}

// openRawDB opens a second connection to the store's file for a test, with
// the busy timeout of store.Open: a write of the running link (TouchAgent
// at each poll) holds the write lock for a moment, and a connection
// without a busy timeout then fails at once with SQLITE_BUSY (T-251; CI
// of #376 at 14887f2).
func openRawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// T-251: the second connection of TestAgentWithRealLink waits for a writer
// instead of failing. A writer holds the lock on purpose (BEGIN IMMEDIATE,
// as the store's transactions); the DDL through openRawDB must wait and
// succeed. Without the busy timeout it fails with SQLITE_BUSY every time.
func TestRawDBWaitsForAWriter(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "busy.db")
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	holder, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { holder.Close() })
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond) // the writer keeps the lock for a while
		conn.ExecContext(ctx, "COMMIT")
		conn.Close()
		close(released)
	}()
	db := openRawDB(t, path)
	if _, err := db.Exec(`CREATE TRIGGER t251 BEFORE UPDATE OF last_seen_at ON agent BEGIN SELECT 1; END`); err != nil {
		t.Fatalf("the second connection did not wait for the writer: %v", err)
	}
	<-released
}
