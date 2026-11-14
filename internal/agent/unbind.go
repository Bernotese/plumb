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
// the guest. The agent then removes Plumb's way in and itself: the repair
// lines, its credential and state, its config, its units and its binaries.
// What it applied stays as it is (files, packages, users), also the
// firewall and network boot units and their files in /etc/plumb-agent: a
// person removes them if wanted; they keep the guest as it was configured.

// unbindUnits are the units the agent disables and removes. The firewall
// and network units stay (see above).
var unbindUnits = []string{"plumb-agent.service", "plumb-agent-guard.timer", "plumb-agent-guard.service", "plumb-agent-install.service"}

// unbindFiles are removed (relative to the guest root).
var unbindFiles = []string{
	"/etc/plumb-agent/agent.json", "/etc/plumb-agent/ca.pem", "/etc/plumb-agent/enroll.token",
	"/usr/local/bin/plumb-agent", "/usr/local/bin/plumb-agent.prev", "/usr/local/sbin/plumb-agent-install",
}

// unbind does the removal. It returns the first error, after it tried
// every step; the caller exits the agent afterwards (its unit is gone, so
// systemd does not start it again).
func (a *Agent) unbind(ctx context.Context) error {
	var errs []error
	if err := removeRepairLines(filepath.Join(a.opt.Root, "root/.ssh/authorized_keys")); err != nil {
		errs = append(errs, err)
	}
	args := append([]string{"disable"}, unbindUnits...)
	if _, err := a.opt.Runner.Run(ctx, "systemctl", args...); err != nil {
		errs = append(errs, fmt.Errorf("systemctl disable: %w", err))
	}
	for _, u := range unbindUnits {
		errs = append(errs, removeIfExists(filepath.Join(a.opt.Root, "etc/systemd/system", u)))
	}
	for _, f := range unbindFiles {
		errs = append(errs, removeIfExists(filepath.Join(a.opt.Root, f)))
	}
	if _, err := a.opt.Runner.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		errs = append(errs, fmt.Errorf("systemctl daemon-reload: %w", err))
	}
	// The credential last: until here a failed step can still be reported.
	if err := os.RemoveAll(a.cfg.StateDir); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

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
