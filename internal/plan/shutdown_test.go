package plan

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a log sink for one writer goroutine and the test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Review S1 of #144: a clean-up that outlives the cancel of its Run (a
// failed build, retention) is cut off ShutdownGrace after Close, and the
// log names the Run and the build. Close then returns.
func TestCloseCutsOffCleanupAfterGrace(t *testing.T) {
	var logs syncBuffer
	grace, stopGrace := context.WithCancel(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{cfg: Config{ShutdownGrace: 200 * time.Millisecond}, log: slog.New(slog.NewTextHandler(&logs, nil)),
		ctx: ctx, cancel: cancel, grace: grace, stopGrace: stopGrace, active: map[int64]bool{42: true}}

	cut := make(chan error, 1)
	e.runs.Add(1)
	go func() { // a Run whose clean-up waits for a slow Proxmox task
		defer e.runs.Done()
		<-e.ctx.Done() // the Run is cancelled by Close
		c, stop := e.cleanupContext(e.ctx, "clean-up of a failed build", "run", 42, "image", "web", "version", 3)
		defer stop()
		if c.Err() != nil {
			cut <- c.Err() // the cancel of the Run must not reach the clean-up
			return
		}
		<-c.Done()
		cut <- c.Err()
	}()
	start := time.Now()
	e.Close()
	took := time.Since(start)
	if took < 150*time.Millisecond || took > 5*time.Second {
		t.Fatalf("Close took %v, want about the grace of 200ms", took)
	}
	if err := <-cut; err != context.Canceled {
		t.Fatalf("clean-up context: %v", err)
	}
	out := logs.String()
	for _, want := range []string{"clean-up of a failed build cut off", "run=42", "image=web", "version=3", "runs=[42]"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

// A clean-up that ends in time is not cut off, and Close does not wait
// for the grace.
func TestCloseWithoutCleanupReturnsAtOnce(t *testing.T) {
	var logs syncBuffer
	grace, stopGrace := context.WithCancel(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{cfg: Config{ShutdownGrace: time.Minute}, log: slog.New(slog.NewTextHandler(&logs, nil)),
		ctx: ctx, cancel: cancel, grace: grace, stopGrace: stopGrace, active: map[int64]bool{}}
	e.runs.Add(1)
	go func() {
		defer e.runs.Done()
		<-e.ctx.Done()
		c, stop := e.cleanupContext(e.ctx, "retention", "run", 7)
		stop()
		_ = c
	}()
	start := time.Now()
	e.Close()
	if time.Since(start) > 5*time.Second || strings.Contains(logs.String(), "cut off") {
		t.Fatalf("Close: %v, log %q", time.Since(start), logs.String())
	}
}

// Re-review S1 of #144: serve calls Stop, then waits for its goroutines,
// then Close. One of them (the dev seed) waits for a Run (Engine.Wait),
// and a cancelled build's clean-up ends only at the grace. So the grace
// clock must start in Stop, not in Close.
func TestStopStartsTheGraceClock(t *testing.T) {
	var logs syncBuffer
	grace, stopGrace := context.WithCancel(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{cfg: Config{ShutdownGrace: 200 * time.Millisecond}, log: slog.New(slog.NewTextHandler(&logs, nil)),
		ctx: ctx, cancel: cancel, grace: grace, stopGrace: stopGrace, active: map[int64]bool{9: true}}
	e.runs.Add(1)
	go func() { // a build whose clean-up waits for a slow Proxmox task
		defer e.runs.Done()
		<-e.ctx.Done()
		c, stop := e.cleanupContext(e.ctx, "clean-up of a failed build", "run", 9, "image", "web")
		defer stop()
		<-c.Done()
	}()
	seed := make(chan struct{})
	go func() { // the dev seed
		e.Wait()
		close(seed)
	}()

	start := time.Now()
	e.Stop()
	select { // serve waits for its goroutines before Close
	case <-seed:
	case <-time.After(5 * time.Second):
		t.Fatal("the seed still waits for the Run: the grace did not start at Stop")
	}
	e.Close()
	if took := time.Since(start); took < 150*time.Millisecond || took > 5*time.Second {
		t.Fatalf("shutdown took %v, want about the grace of 200ms", took)
	}
	if out := logs.String(); !strings.Contains(out, "runs=[9]") || !strings.Contains(out, "clean-up of a failed build cut off") {
		t.Fatalf("log:\n%s", out)
	}
}
