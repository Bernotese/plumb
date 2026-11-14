package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Unbind (D-388, ARCHITECTURE §19): the delete of an adopted Target keeps
// the guest. The agent removes Plumb's way in and itself: the repair lines,
// every agent unit (also the firewall and network boot units: the delete
// Plan that drops a firewall is human-only firewall-off, D-368), its
// binaries and /etc/plumb-agent. What it applied elsewhere stays (files,
// packages, users, the netplan file). The credential goes last, after the
// controller took the result (finishUnbind), like seal.

// unbindUnits are the units the agent disables and removes.
var unbindUnits = []string{
	"plumb-agent.service", "plumb-agent-install.service", "plumb-agent-guard.service", "plumb-agent-guard.timer",
	"plumb-agent-firewall.service", "plumb-agent-firewall-rollback.service", "plumb-agent-firewall-rollback.timer",
	"plumb-agent-network.service", "plumb-agent-network-rollback.service", "plumb-agent-network-rollback.timer",
}

// unbindFiles are removed (relative to the guest root).
var unbindFiles = []string{
	"/usr/local/bin/plumb-agent", "/usr/local/bin/plumb-agent.prev", "/usr/local/sbin/plumb-agent-install",
}

// unbind does the removal up to the credential. It returns every error it
// met after it tried every step; the result goes to the controller.
func (a *Agent) unbind(ctx context.Context) error {
	var errs []error
	if err := removeRepairLines(a.opt.Root); err != nil { // repairline.go (T-191)
		errs = append(errs, err)
	}
	// systemctl disable fails on a unit file that does not exist, and a
	// guest has only some of these units (T-205): only present ones.
	var present []string
	for _, u := range unbindUnits[1:] { // the agent's own unit is stopped last
		if _, err := os.Lstat(filepath.Join(a.opt.Root, "etc/systemd/system", u)); err == nil {
			present = append(present, u)
		}
	}
	if len(present) > 0 {
		if _, err := a.opt.Runner.Run(ctx, "systemctl", append([]string{"disable"}, present...)...); err != nil {
			errs = append(errs, fmt.Errorf("systemctl disable: %w", err))
		}
	}
	if _, err := os.Lstat(filepath.Join(a.opt.Root, "etc/systemd/system", unbindUnits[0])); err == nil {
		if _, err := a.opt.Runner.Run(ctx, "systemctl", "disable", unbindUnits[0]); err != nil {
			errs = append(errs, fmt.Errorf("systemctl disable: %w", err))
		}
	}
	for _, u := range unbindUnits {
		errs = append(errs, removeIfExists(filepath.Join(a.opt.Root, "etc/systemd/system", u)))
	}
	for _, f := range unbindFiles {
		errs = append(errs, removeIfExists(filepath.Join(a.opt.Root, f)))
	}
	if err := os.RemoveAll(filepath.Join(a.opt.Root, "etc/plumb-agent")); err != nil {
		errs = append(errs, err)
	}
	if _, err := a.opt.Runner.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		errs = append(errs, fmt.Errorf("systemctl daemon-reload: %w", err))
	}
	return errors.Join(errs...)
}

// finishUnbind runs after the controller took the result: it removes the
// credential and the state, then stops the agent's own unit. The agent
// then ends; systemd does not start it again (the unit is gone).
func (a *Agent) finishUnbind(ctx context.Context) {
	if err := os.RemoveAll(a.cfg.StateDir); err != nil {
		a.log.Warn("unbind: remove the state", "err", err)
	}
	if _, err := a.opt.Runner.Run(ctx, "systemctl", "--no-block", "stop", unbindUnits[0]); err != nil {
		a.log.Warn("unbind: stop the agent unit", "err", err)
	}
}

// unbindRefused handles a 401 on the result of an unbind (review L1 of
// #299): the Run revoked the credential, also when the result got lost,
// so the agent finishes the unbind instead of lingering with its state.
func (a *Agent) unbindRefused(ctx context.Context, cmdID string) bool {
	if !a.unboundBy(cmdID) {
		return false
	}
	a.finishUnbind(ctx)
	return true
}

// unboundBy reports whether the command id is an unbind that ended
// (handle sets it).
func (a *Agent) unboundBy(cmdID string) bool { return cmdID != "" && a.unbindID == cmdID }

func removeIfExists(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
