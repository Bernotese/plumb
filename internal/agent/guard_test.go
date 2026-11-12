package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// guardEnv is an update env after a swap to v2, with a config for the guard.
type guardEnv struct {
	*updEnv
	nb   []byte
	boot string
	opt  GuardOptions
}

func newGuardEnv(t *testing.T) *guardEnv {
	t.Helper()
	e := newUpdEnv(t)
	b, _ := json.Marshal(Config{URL: "https://127.0.0.1:8444", CA: newCA(t).pem, StateDir: e.u.state.dir})
	if err := os.WriteFile(e.config, b, 0o644); err != nil {
		t.Fatal(err)
	}
	g := &guardEnv{updEnv: e, nb: fakeBin("v2", ""), boot: "boot-1"}
	if _, err := e.u.update(context.Background(), serveBytes(g.nb), shaOf(g.nb), "v1"); err != nil {
		t.Fatal(err)
	}
	g.opt = GuardOptions{ConfigPath: e.config, BinaryPath: e.bin, UnitDir: e.units, Runner: e.r,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		BootID: func() (string, error) { return g.boot, nil },
		Now:    func() time.Time { return time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC) }}
	return g
}

func (g *guardEnv) tick(t *testing.T) error {
	t.Helper()
	return RunGuard(context.Background(), g.opt)
}

func (g *guardEnv) marker(t *testing.T) updateMarker {
	t.Helper()
	m, ok, err := g.u.marker()
	if err != nil || !ok {
		t.Fatalf("marker: %v %v", ok, err)
	}
	return m
}

// reverted checks the end state of a revert.
func (g *guardEnv) reverted(t *testing.T, reason string) {
	t.Helper()
	if read(t, g.bin) != string(g.old) || read(t, g.u.bin.prev()) != string(g.old) {
		t.Error("old binary not back")
	}
	m := g.marker(t)
	if m.State != updateReverted || m.Reason != reason || m.At != "2026-11-12T10:00:00Z" {
		t.Errorf("marker %+v", m)
	}
	if !g.r.called("restart "+agentService) || present(filepath.Join(g.units, guardTimer)) {
		t.Errorf("no restart or guard left: %v", g.r.calls)
	}
}

func ticksOf(d time.Duration) int { return int((d + guardTick - 1) / guardTick) }

func TestGuardNoHelloWindow(t *testing.T) {
	g := newGuardEnv(t)
	for i := 1; i < ticksOf(guardWindow); i++ {
		if err := g.tick(t); err != nil {
			t.Fatal(err)
		}
	}
	if m := g.marker(t); m.State != updatePending || m.Ticks != ticksOf(guardWindow)-1 || read(t, g.bin) != string(g.nb) {
		t.Fatalf("reverted early: %+v", m)
	}
	if err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	g.reverted(t, wire.RevertNoHello)
	// The next tick sees no pending update and only cleans up.
	if err := g.tick(t); err != nil || g.marker(t).State != updateReverted {
		t.Fatal(err)
	}
}

func TestGuardCrashLoop(t *testing.T) {
	g := newGuardEnv(t)
	g.r.restarts = 7
	if err := g.tick(t); err != nil { // base 7
		t.Fatal(err)
	}
	g.r.restarts = 9
	if err := g.tick(t); err != nil || g.marker(t).State != updatePending {
		t.Fatalf("reverted at +2: %v", err)
	}
	g.r.restarts = 10
	if err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	g.reverted(t, wire.RevertCrashLoop)
}

func TestGuardRebootNewBase(t *testing.T) {
	g := newGuardEnv(t)
	g.r.restarts = 5
	if err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	// After a reboot NRestarts starts again at 0: a new base, no revert.
	g.boot, g.r.restarts = "boot-2", 0
	if err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	m := g.marker(t)
	if m.State != updatePending || m.BootID != "boot-2" || *m.RestartsBase != 0 || m.Ticks != 2 {
		t.Fatalf("marker %+v", m)
	}
}

func TestGuardRestartFailureRetries(t *testing.T) {
	g := newGuardEnv(t)
	g.r.restarts = 0
	if err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	g.r.restarts = 3
	g.r.fail["restart "+agentService] = errors.New("busy")
	if err := g.tick(t); err == nil {
		t.Fatal("no error")
	}
	if m := g.marker(t); m.State != updatePending || read(t, g.bin) != string(g.old) {
		t.Fatalf("after a failed restart: %+v", m)
	}
	delete(g.r.fail, "restart "+agentService)
	if err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	g.reverted(t, wire.RevertCrashLoop)
}

func TestGuardNoRestartsOnlyWindow(t *testing.T) {
	g := newGuardEnv(t)
	g.r.fail["show --property=NRestarts --value "+agentService] = errors.New("no systemd")
	for i := 0; i < ticksOf(guardWindow); i++ {
		if err := g.tick(t); err != nil {
			t.Fatal(err)
		}
	}
	g.reverted(t, wire.RevertNoHello)
}

func TestGuardAfterConfirmRemovesItself(t *testing.T) {
	g := newGuardEnv(t)
	// The new agent dropped the marker but could not remove the units.
	if err := g.u.dropMarker(); err != nil {
		t.Fatal(err)
	}
	if err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	if present(filepath.Join(g.units, guardTimer)) || present(filepath.Join(g.units, guardService)) || g.r.called("restart "+agentService) {
		t.Error("guard did not clean up, or restarted the agent")
	}
	if read(t, g.bin) != string(g.nb) {
		t.Error("a confirmed binary was reverted")
	}
}

// The lock orders confirm and revert: a guard tick waits for a confirm
// that holds the lock, then sees no marker.
func TestGuardWaitsForConfirmLock(t *testing.T) {
	g := newGuardEnv(t)
	unlock, err := g.u.lock()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- g.tick(t) }()
	select {
	case <-done:
		t.Fatal("guard ran without the lock")
	case <-time.After(100 * time.Millisecond):
	}
	if err := g.u.dropMarker(); err != nil { // the confirm, under the lock
		t.Fatal(err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if read(t, g.bin) != string(g.nb) {
		t.Error("reverted after confirm")
	}
}
