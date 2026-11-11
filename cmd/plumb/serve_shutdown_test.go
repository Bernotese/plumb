package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/store"
)

// serveGoroutines are the functions of the goroutines that runServer
// starts. None of them may still run when runServer has returned: they use
// the store, and runServer closes it (CI race job: TempDir cleanup found a
// new file in the data dir after the test).
var serveGoroutines = []string{
	"main.runServer.func",
	"main.seedDev",
	"main.(*devAgents).run",
	"plan.(*Engine).PollGuests",
	"plan.(*Engine).RunSchedules",
	"link.(*Link).ServeListener",
}

func liveServeGoroutines() []string {
	var b bytes.Buffer
	pprof.Lookup("goroutine").WriteTo(&b, 2)
	var out []string
	for _, g := range strings.Split(b.String(), "\n\n") {
		for _, fn := range serveGoroutines {
			if strings.Contains(g, fn) {
				out = append(out, fn)
				break
			}
		}
	}
	return out
}

// Shutdown: runServer returns only after every goroutine it started has
// ended. Several starts, because a goroutine that is still running at the
// return shows up only now and then.
func TestServeWaitsForItsGoroutines(t *testing.T) {
	if left := liveServeGoroutines(); len(left) > 0 {
		t.Fatalf("serve goroutines run before the test: %v", left)
	}
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		f, err := parseServe([]string{"--dev", "--dev-demo", "--data", t.TempDir(), "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0"}, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		out := outputProbe(func(b []byte) (int, error) {
			if strings.HasPrefix(string(b), "dev login:") {
				cancel() // stop while the seed and the first poll rounds run
			}
			return len(b), nil
		})
		done := make(chan error, 1)
		go func() { done <- runServer(ctx, f, out, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("runServer did not return")
		}
		if left := liveServeGoroutines(); len(left) > 0 {
			t.Fatalf("start %d: still running after runServer returned: %v", i, left)
		}
		cancel()
	}
}

// CI race job of #144: serve hung at a stop while the dev seed waited for
// its first Run, and that Run waited for an agent that was already gone.
// The Runs must be cancelled before serve waits for the seed. The fake
// driver is slow here, so the Run is still running at the stop.
func TestServeStopsDuringASeedRun(t *testing.T) {
	testDevDriver = func(d *fake.Driver) { d.SetLatency(150 * time.Millisecond) }
	defer func() { testDevDriver = nil }()
	data := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f, err := parseServe([]string{"--dev", "--dev-demo", "--data", data, "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	listening := make(chan struct{}, 1)
	out := outputProbe(func(b []byte) (int, error) {
		if strings.HasPrefix(string(b), "Plumb listens on") {
			listening <- struct{}{} // the migrations are done
		}
		return len(b), nil
	})
	done := make(chan error, 1)
	go func() { done <- runServer(ctx, f, out, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	select {
	case <-listening:
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not start")
	}
	st, err := store.Open(ctx, filepath.Join(data, "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	deadline := time.Now().Add(20 * time.Second)
	for {
		runs, _ := st.RunningRuns(ctx)
		if len(runs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the dev seed started no Run")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel() // stop while the seed Run runs
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not return after a stop during the seed Run")
	}
}
