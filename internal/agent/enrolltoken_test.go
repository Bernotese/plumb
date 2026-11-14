package agent

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// syncBuf is a log sink for several goroutines.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// oneShotEnv is a real link and an agent that can be started again on
// the same guest root and state dir.
type oneShotEnv struct {
	t      *testing.T
	st     *store.Store
	ctl    *link.Link
	srv    *httptest.Server
	offset atomic.Int64
	root   string
	state  string
	log    *syncBuf
}

func newOneShotEnv(t *testing.T) *oneShotEnv {
	t.Helper()
	ctx := context.Background()
	e := &oneShotEnv{t: t, root: t.TempDir(), state: filepath.Join(t.TempDir(), "state"), log: &syncBuf{}}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "link.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := func() time.Time { return time.Now().Add(time.Duration(e.offset.Load())) }
	st.Now = now
	e.st = st
	if e.ctl, err = link.New(link.Config{Dir: filepath.Join(t.TempDir(), "link"), Store: st, Now: now}); err != nil {
		t.Fatal(err)
	}
	e.srv = httptest.NewUnstartedServer(e.ctl.Handler())
	e.srv.TLS = e.ctl.TLSConfig()
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	tok, _, err := e.ctl.NewEnrollToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(e.root, "token"), tok+"\n")
	write(t, filepath.Join(e.root, "etc/os-release"), "ID=debian\nVERSION_ID=\"13\"\n")
	return e
}

// run starts the agent and returns a stop function.
func (e *oneShotEnv) run() func() {
	e.t.Helper()
	cfg := Config{URL: e.srv.URL, CA: string(e.ctl.CA().CertPEM), TokenFile: filepath.Join(e.root, "token"),
		StateDir: e.state, FullCheck: Duration(time.Hour)}
	a, err := New(Options{Config: cfg, Root: e.root, Runner: &runner{}, Version: "test", Hostname: "web1",
		Log:      slog.New(slog.NewTextHandler(e.log, nil)),
		Debounce: 100 * time.Millisecond, PollWait: 1, BackoffMin: 10 * time.Millisecond, BackoffMax: 50 * time.Millisecond})
	if err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Run(ctx) }()
	return func() { cancel(); <-done }
}

// agentAfter waits until the store has an agent of web1 that said hello
// after t (TouchAgent on every call) and returns it.
func (e *oneShotEnv) agentAfter(t time.Time) store.Agent {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a, err := e.st.AgentByTarget(context.Background(), "web1")
		if err == nil && a.LastSeenAt.After(t) {
			return a
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatal("no agent call after the start")
	return store.Agent{}
}

func (e *oneShotEnv) now() time.Time { return time.Now().Add(time.Duration(e.offset.Load())) }

func (e *oneShotEnv) writeOneShot(content string, mode os.FileMode) string {
	e.t.Helper()
	p := filepath.Join(e.root, wire.RepairTokenFile)
	write(e.t, p, content+"\n")
	if err := os.Chmod(p, mode); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func gone(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); !os.IsNotExist(err) {
		t.Fatalf("%s is still there: %v", p, err)
	}
}

// TestOneShotTokenKeepsCredential (D-390, review R1 of #290): an expired,
// a refused and a badly owned or readable token file each leave the old
// credential working, and the file is gone. A good token swaps the
// credential and the old one is refused.
func TestOneShotTokenKeepsCredential(t *testing.T) {
	e := newOneShotEnv(t)
	ctx := context.Background()
	stop := e.run()
	first := e.agentAfter(time.Time{})
	stop()

	// Expired: the controller refuses the token; the old credential stays.
	rt, err := e.ctl.NewRepairToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	e.offset.Store(int64(link.TokenTTL + time.Minute))
	p := e.writeOneShot(rt.Token, 0o600)
	start := e.now()
	stop = e.run()
	if a := e.agentAfter(start); a.ID != first.ID {
		t.Fatalf("expired token: agent %s, want the old %s", a.ID, first.ID)
	}
	gone(t, p)
	stop()

	// Unknown token: the same.
	p = e.writeOneShot("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 0o600)
	start = e.now()
	stop = e.run()
	if a := e.agentAfter(start); a.ID != first.ID {
		t.Fatalf("unknown token: agent %s, want the old %s", a.ID, first.ID)
	}
	gone(t, p)
	stop()

	// Group-readable, a symlink, a dir: ignored and removed; the valid
	// token in it stays unused.
	rt, err = e.ctl.NewRepairToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	for name, mk := range map[string]func() string{
		"mode 0640": func() string { return e.writeOneShot(rt.Token, 0o640) },
		"mode 0604": func() string { return e.writeOneShot(rt.Token, 0o604) },
		"symlink": func() string {
			src := filepath.Join(t.TempDir(), "tok")
			if err := os.WriteFile(src, []byte(rt.Token), 0o600); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(e.root, wire.RepairTokenFile)
			if err := os.Symlink(src, p); err != nil {
				t.Fatal(err)
			}
			return p
		},
	} {
		p := mk()
		start = e.now()
		stop = e.run()
		if a := e.agentAfter(start); a.ID != first.ID {
			t.Fatalf("%s: the agent enrolled with an ignored file", name)
		}
		gone(t, p)
		stop()
	}

	// A good token: a new agent, the file is gone, the old credential is
	// refused.
	p = e.writeOneShot(rt.Token, 0o600)
	start = e.now()
	stop = e.run()
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	var a store.Agent
	for time.Now().Before(deadline) {
		a, err = e.st.AgentByTarget(ctx, "web1")
		if err == nil && a.ID != first.ID && a.LastSeenAt.After(start) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if a.ID == first.ID || a.LastSeenAt.Before(start) {
		t.Fatal("the agent did not enroll with the one-shot token")
	}
	gone(t, p)
	if _, err := e.st.AgentByID(ctx, first.ID); err == nil {
		t.Fatal("the old agent row stays after the enrollment")
	}
	if strings.Contains(e.log.String(), rt.Token) {
		t.Fatal("the log holds the token")
	}
	if !strings.Contains(e.log.String(), "one-shot enrollment token refused") ||
		!strings.Contains(e.log.String(), "one-shot enrollment token file ignored") {
		t.Fatalf("log:\n%s", e.log.String())
	}
}

// An unreachable controller keeps the one-shot token for oneShotKeep only
// and never drops the stored credential.
func TestOneShotTokenUnreachableKeepsCredential(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{opt: Options{RepairTokenFile: filepath.Join(dir, "enroll.token")}, log: slog.New(slog.NewTextHandler(&syncBuf{}, nil))}
	if err := os.WriteFile(a.oneShotPath(), []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.oneShot = a.takeOneShot()
	if a.oneShot == nil || a.oneShot.token != "tok" {
		t.Fatalf("one-shot %+v", a.oneShot)
	}
	gone(t, a.oneShotPath())
	c, err := newClient(Config{URL: "https://127.0.0.1:1", CA: testCAPEM(t)}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a.client, a.cred = c, "old.cred"
	if a.tryOneShot(context.Background()) || a.oneShot == nil || a.credential() != "old.cred" {
		t.Fatal("a failed call dropped the token or changed the credential")
	}
	a.oneShot.at = time.Now().Add(-oneShotKeep - time.Second)
	if a.tryOneShot(context.Background()) || a.oneShot != nil || a.credential() != "old.cred" {
		t.Fatal("after oneShotKeep: the token stays or the credential changed")
	}
}

func testCAPEM(t *testing.T) string {
	t.Helper()
	l, err := link.New(link.Config{Dir: filepath.Join(t.TempDir(), "ca"), Store: func() *store.Store {
		st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "x.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	}()})
	if err != nil {
		t.Fatal(err)
	}
	return string(l.CA().CertPEM)
}
