package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// GuardOptions configure RunGuard. Zero values mean the production defaults.
type GuardOptions struct {
	ConfigPath string // default DefaultConfigPath
	BinaryPath string // default DefaultBinaryPath
	UnitDir    string // default DefaultUnitDir
	Runner     resource.Runner
	Log        *slog.Logger
	// BootID reads the boot id; default /proc/sys/kernel/random/boot_id.
	BootID func() (string, error)
	Now    func() time.Time
}

// RunGuard is one tick of "plumb-agent.prev guard" (D-271). It runs as the
// old binary, from the guard timer. With no pending update it removes the
// guard units. A pending update that had no confirmed hello within
// guardWindow, or that crash-loops, gets .prev back and a restart.
func RunGuard(ctx context.Context, opt GuardOptions) error {
	if opt.ConfigPath == "" {
		opt.ConfigPath = DefaultConfigPath
	}
	if opt.BinaryPath == "" {
		opt.BinaryPath = DefaultBinaryPath
	}
	if opt.UnitDir == "" {
		opt.UnitDir = DefaultUnitDir
	}
	if opt.Runner == nil {
		opt.Runner = resource.ExecRunner{}
	}
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	if opt.BootID == nil {
		opt.BootID = readBootID
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	cfg, err := LoadConfig(opt.ConfigPath)
	if err != nil {
		return err
	}
	u := &updater{state: stateDir{dir: cfg.StateDir}, bin: binPaths{bin: opt.BinaryPath},
		unitDir: opt.UnitDir, config: opt.ConfigPath, run: opt.Runner, log: opt.Log}
	unlock, err := u.lock()
	if err != nil {
		return err
	}
	defer unlock()
	m, ok, err := u.marker()
	if err != nil {
		return err
	}
	if !ok || m.State != updatePending {
		return u.removeGuard(ctx)
	}

	boot, err := opt.BootID()
	if err != nil {
		return fmt.Errorf("boot id: %w", err)
	}
	restarts, rerr := u.restarts(ctx)
	if rerr != nil {
		opt.Log.Warn("cannot read the restarts of the agent unit; only the window counts", "err", rerr)
	} else if m.BootID != boot || m.RestartsBase == nil {
		m.BootID, m.RestartsBase = boot, &restarts // first tick of this boot
	}
	m.Ticks++
	switch {
	case rerr == nil && restarts-*m.RestartsBase >= crashLoopRestarts:
		m.Reason = wire.RevertCrashLoop
	case time.Duration(m.Ticks)*guardTick >= guardWindow:
		m.Reason = wire.RevertNoHello
	default:
		return u.saveMarker(m)
	}

	opt.Log.Warn("agent update failed; restoring the previous binary", "reason", m.Reason, "from", m.FromVersion, "to", m.ToVersion)
	// Idempotent: a failed step keeps the marker pending, and the next
	// tick tries again.
	if err := linkOver(u.bin.prev(), u.bin.temp(), u.bin.bin); err != nil {
		_ = u.saveMarker(m)
		return fmt.Errorf("restore %s: %w", u.bin.prev(), err)
	}
	// The marker says reverted before the restart: the restored agent
	// reads it at its start and reports the revert in its first hello
	// (CI of #198: a marker written after the restart was too late).
	pending := m
	m.State, m.At = updateReverted, opt.Now().UTC().Format(time.RFC3339)
	if err := u.saveMarker(m); err != nil {
		return err
	}
	if _, err := u.run.Run(ctx, "systemctl", "restart", agentService); err != nil {
		_ = u.saveMarker(pending)
		return fmt.Errorf("restart the agent: %w", err)
	}
	return u.removeGuard(ctx)
}

// restarts reads NRestarts of the agent unit.
func (u *updater) restarts(ctx context.Context) (int, error) {
	out, err := u.run.Run(ctx, "systemctl", "show", "--property=NRestarts", "--value", agentService)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func readBootID() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(b)), err
}
