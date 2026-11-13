package resource

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Bernotese/plumb/internal/wire"
)

// Safe apply, the part that every type of wire.SafeApplyTypes shares
// (D-293, D-300, D-333, D-349). The agent runs the window, the marker and
// the confirm (internal/agent/safeapply.go); a type gives Prepare, Commit
// and Restore, its rollback command and its restore at agent start.

// SafeApplier is a resource of wire.SafeApplyTypes. The agent calls
// Prepare, then Apply (live only), then after the controller round trip
// Commit, or Restore when the round trip did not come.
type SafeApplier interface {
	Resource
	// Prepare makes sure that Restore, the boot and the rollback timer
	// bring back the confirmed state. It changes nothing live.
	Prepare(ctx context.Context) error
	// Commit makes the live state the confirmed one.
	Commit(ctx context.Context) error
	// Restore puts the confirmed state back live.
	Restore(ctx context.Context) error
}

// rollbackAfter is the delay of a rollback timer in seconds: after the
// window, so a living agent always restores or commits first. A var only
// for the guest tests (firewall_guest.go).
var rollbackAfter = wire.SafeApplySeconds + 30

// RollbackUnit is the name of the transient rollback timer of a type
// (review F1 of T-111): it brings the confirmed state back when the agent
// dies in the window and does not come back. Units: <name>.timer,
// <name>.service.
func RollbackUnit(typ string) string { return "plumb-agent-" + typ + "-rollback" }

// safeRestorers put the confirmed state of a type back without a desired
// resource. Add a type here and in wire.SafeApplyTypes.
var safeRestorers = map[string]func(ctx context.Context, env Env) error{
	"firewall": restoreFirewall,
}

// Restore puts the confirmed state of a safe-apply type back, without a
// desired resource: the agent does it at start when it finds the marker.
func Restore(ctx context.Context, env Env, typ string) error {
	if f, ok := safeRestorers[typ]; ok {
		return f(ctx, env)
	}
	return fmt.Errorf("no safe apply for type %q", typ)
}

// armRollback starts the rollback timer of typ, which runs cmd after
// rollbackAfter seconds. A stale timer of an earlier agent is stopped
// first.
func armRollback(ctx context.Context, env Env, typ, description string, cmd ...string) error {
	disarmRollback(ctx, env, typ)
	args := append([]string{"--unit=" + RollbackUnit(typ), "--description=" + description,
		"--on-active=" + strconv.Itoa(rollbackAfter), "--timer-property=AccuracySec=1s"}, cmd...)
	if _, err := env.Run.Run(ctx, "systemd-run", args...); err != nil {
		return fmt.Errorf("arm the rollback timer: %w", err)
	}
	return nil
}

// disarmRollback stops the rollback timer of typ. A timer that is not
// loaded is no error.
func disarmRollback(ctx context.Context, env Env, typ string) {
	u := RollbackUnit(typ)
	_, _ = env.Run.Run(ctx, "systemctl", "stop", u+".timer")
	_, _ = env.Run.Run(ctx, "systemctl", "reset-failed", u+".timer", u+".service")
}
