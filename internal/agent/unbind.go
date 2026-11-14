package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	if err := removeRepairLines(filepath.Join(a.opt.Root, "root/.ssh/authorized_keys")); err != nil {
		errs = append(errs, err)
	}
	args := append([]string{"disable"}, unbindUnits[1:]...) // the agent's own unit is stopped last
	if _, err := a.opt.Runner.Run(ctx, "systemctl", args...); err != nil {
		errs = append(errs, fmt.Errorf("systemctl disable: %w", err))
	}
	if _, err := a.opt.Runner.Run(ctx, "systemctl", "disable", unbindUnits[0]); err != nil {
		errs = append(errs, fmt.Errorf("systemctl disable: %w", err))
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

// unboundBy reports whether the command id is an unbind that ended
// (handle sets it).
func (a *Agent) unboundBy(cmdID string) bool { return cmdID != "" && a.unbindID == cmdID }

func removeIfExists(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// removeRepairLines drops every authorized_keys line whose comment starts
// with plumb-repair- and keeps every other line byte for byte. A missing
// file is fine. The file is replaced atomically with its mode.
func removeRepairLines(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	changed := false
	for sc.Scan() {
		line := sc.Text()
		if isRepairLine(line) {
			changed = true
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !changed {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp := path + ".plumb-unbind"
	if err := os.WriteFile(tmp, out.Bytes(), fi.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// isRepairLine: the last field is the comment plumb-repair-<id>. An
// option string may hold spaces only inside quotes, so the comment is the
// last space-separated field of a key line.
func isRepairLine(line string) bool {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return false
	}
	f := strings.Fields(line)
	if len(f) < 3 || !reRepairComment.MatchString(f[len(f)-1]) {
		return false
	}
	// The key type is the field before the key, which is before the comment.
	return strings.HasPrefix(f[len(f)-3], "ssh-")
}

// reRepairComment is the comment of a repair key line (D-383).
var reRepairComment = regexp.MustCompile(`^plumb-repair-[0-9a-z]+$`)
