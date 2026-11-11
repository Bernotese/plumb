package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// Seal (D-205, D-207, plan §4 of T-039) makes an image build guest clean
// for a template. The step list is fixed here: the command body holds no
// path, command or user name. The steps run in the order of
// wire.SealSteps; the first failed step ends seal and the rest are
// skipped. After the controller took the result, the agent removes its own
// files, runs sync and powers the guest off (finishSeal); it sends no
// second result.

// Fixed names of the build guest (D-203, plan §2 of T-039).
const (
	buildUser    = "plumb-build"
	buildHome    = "/home/plumb-build"
	buildSudoers = "/etc/sudoers.d/90-cloud-init-users"
	dracutConf   = "/etc/dracut.conf.d/90-plumb-no-network.conf"
	// D-192: no network setup in the initramfs, so a static address of a
	// clone works at the first boot.
	dracutConfText = "# Plumb (D-192): no network setup in the initramfs.\nomit_dracutmodules+=\" network systemd-networkd \"\n"
	cloudInitLog   = "/var/log/cloud-init-output.log"
)

// agentFiles are the agent's own files, removed after the result of seal
// was taken (D-206: a VM template holds no agent). The state dir (the
// credential and the desired list) goes first.
var agentFiles = []string{
	DefaultStateDir,
	"/etc/plumb-agent", // agent.json, ca.pem
	"/etc/systemd/system/multi-user.target.wants/plumb-agent.service",
	"/etc/systemd/system/plumb-agent.service",
	"/usr/local/sbin/plumb-agent-install",
	"/usr/local/bin/plumb-agent",
}

// accountBackups are the backup files of the account files (T-066 F3).
var accountBackups = []string{"/etc/passwd-", "/etc/shadow-", "/etc/group-", "/etc/gshadow-", "/etc/subuid-", "/etc/subgid-"}

// histories are shell and tool histories of root that seal removes.
var histories = []string{".bash_history", ".lesshst", ".viminfo", ".python_history", ".wget-hsts"}

// sealStep is one step: it returns applied, skipped or failed, and a text.
type sealStep struct {
	name string
	run  func(ctx context.Context, body wire.SealBody) (wire.Status, string)
}

func (a *Agent) sealSteps() []sealStep {
	return []sealStep{
		{wire.SealStepInitramfs, a.sealInitramfs},
		{wire.SealStepUpgrade, a.sealUpgrade},
		{wire.SealStepLogStop, a.sealLogStop},
		{wire.SealStepCloudInit, a.sealCloudInit},
		{wire.SealStepHostKeys, a.sealHostKeys},
		{wire.SealStepBuildUser, a.sealBuildUser},
		{wire.SealStepSeed, a.sealSeed},
		{wire.SealStepLogs, a.sealLogs},
		{wire.SealStepTrim, a.sealTrim},
	}
}

// mayNotSeal returns why this agent must not seal, or "" (D-207). It reads
// the credential file again: the marks on disk decide.
func (a *Agent) mayNotSeal() string {
	id, _, err := a.state.identity()
	switch {
	case err != nil:
		return "cannot read the build mark: " + err.Error()
	case id.Watch:
		return "this agent got a watch: it is the agent of a Target, never of a build guest"
	case !id.Build:
		return "this agent has no build mark: only the agent of a new image build guest seals"
	}
	return ""
}

// seal answers a seal command: one Item per step (wire.SealSteps).
func (a *Agent) seal(ctx context.Context, cmdID string, body wire.SealBody) []wire.Item {
	// No drift check and no apply while the guest is sealed.
	a.applyLock.Lock()
	defer a.applyLock.Unlock()
	if why := a.mayNotSeal(); why != "" {
		a.log.Warn("seal refused", "reason", why)
		return []wire.Item{{Resource: wire.SealItemID(wire.SealStepGuard), Status: wire.StatusRefused, Output: why}}
	}
	items := []wire.Item{{Resource: wire.SealItemID(wire.SealStepGuard), Status: wire.StatusApplied,
		Output: "build mark set, no watch"}}
	failed := ""
	for _, s := range a.sealSteps() {
		id := wire.SealItemID(s.name)
		if failed != "" {
			items = append(items, wire.Item{Resource: id, Status: wire.StatusSkipped, Output: "not run: " + failed + " failed"})
			continue
		}
		st, out := s.run(ctx, body)
		item := wire.Item{Resource: id, Status: st, Output: resource.SafeOutput(wire.Resource{}, out)}
		if st == wire.StatusFailed {
			failed = id
			a.log.Error("seal step failed", "step", s.name)
		}
		items = append(items, item)
	}
	if failed == "" {
		a.mu.Lock()
		a.sealedCmd = cmdID
		a.mu.Unlock()
		a.log.Info("sealed; the agent removes itself and powers off when the controller took the result")
	}
	return items
}

// sealedBy reports whether the command id is a seal that ended without a
// failed step.
func (a *Agent) sealedBy(cmdID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return cmdID != "" && a.sealedCmd == cmdID
}

// finishSeal is the end of the handshake (review B1 of T-039): remove the
// agent's files, sync, power off. When a file cannot be removed the guest
// stays on: the controller waits for "stopped", times out and deletes the
// guest, so no template holds the agent.
func (a *Agent) finishSeal(ctx context.Context) error {
	a.applyLock.Lock()
	defer a.applyLock.Unlock()
	var errs error
	// A state dir other than the default: only the agent's own files in it.
	for _, n := range []string{credentialFile, desiredFile} {
		if err := os.Remove(a.state.path(n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = errors.Join(errs, err)
		}
	}
	if err := a.state.cleanStaleTemps(); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = errors.Join(errs, err)
	}
	for _, p := range agentFiles {
		errs = errors.Join(errs, removeGuest(a.opt.Root, p))
	}
	if errs != nil {
		a.log.Error("seal: cannot remove the agent; the guest stays on and the build fails", "err", errs)
		return errs
	}
	a.opt.Sync()
	if _, err := a.opt.Runner.Run(ctx, "systemctl", "poweroff"); err != nil {
		a.log.Error("seal: power off failed; the build fails", "err", err)
		return err
	}
	a.log.Info("seal: agent removed; the guest powers off")
	return nil
}

func applied(text string) (wire.Status, string) { return wire.StatusApplied, text }
func skippedStep(text string) (wire.Status, string) {
	return wire.StatusSkipped, text
}
func failedStep(format string, args ...any) (wire.Status, string) {
	return wire.StatusFailed, fmt.Sprintf(format, args...)
}

// commandIn reports whether the guest has the command in /usr/bin or
// /usr/sbin (Debian 13 and Ubuntu 26.04 have a merged /usr).
func (a *Agent) commandIn(name string) bool {
	return guestRegular(a.opt.Root, "/usr/bin/"+name) || guestRegular(a.opt.Root, "/usr/sbin/"+name)
}

// unitIn reports whether a unit file of the distribution exists.
func (a *Agent) unitIn(name string) bool {
	return guestRegular(a.opt.Root, "/usr/lib/systemd/system/"+name)
}

// (2) D-192, only with dracut (Ubuntu 26.04; Debian 13 has none).
func (a *Agent) sealInitramfs(ctx context.Context, _ wire.SealBody) (wire.Status, string) {
	if !a.commandIn("dracut") {
		return skippedStep("no dracut in this guest")
	}
	// T-066 re-check: dracut copies /etc/hostname, /etc/hosts and
	// /etc/machine-id into the initrd, so they are reset before it runs.
	// The cloud-init step (5) resets them again (no change then).
	if err := a.resetIdentityFiles(); err != nil {
		return failedStep("%v", err)
	}
	if err := writeGuestFile(a.opt.Root, dracutConf, []byte(dracutConfText)); err != nil {
		return failedStep("%v", err)
	}
	if _, err := a.opt.Runner.Run(ctx, "dracut", "-f", "--regenerate-all"); err != nil {
		return failedStep("%v", err)
	}
	return applied("initramfs without network modules")
}

var (
	aptOldSummary = regexp.MustCompile(`(?m)^([0-9]+) upgraded, ([0-9]+) newly installed`)
	aptNewSummary = regexp.MustCompile(`(?m)^\s*Upgrading: ([0-9]+), Installing: ([0-9]+)`)
	aptInst       = regexp.MustCompile(`(?m)^Inst `)
)

// aptPending reads the output of "apt-get -s dist-upgrade": the number of
// packages to upgrade or install. Packages that Ubuntu holds back by
// phasing are "not upgraded" and do not count. ok is false when the
// output has no summary.
func aptPending(out string) (n int, ok bool) {
	m := aptOldSummary.FindStringSubmatch(out)
	if m == nil {
		m = aptNewSummary.FindStringSubmatch(out)
	}
	if m == nil {
		return 0, false
	}
	up, _ := strconv.Atoi(m[1])
	in, _ := strconv.Atoi(m[2])
	return max(up+in, len(aptInst.FindAllString(out, -1))), true
}

// (3) The upgrade of the build is complete.
func (a *Agent) sealUpgrade(ctx context.Context, body wire.SealBody) (wire.Status, string) {
	if !body.Upgraded {
		return skippedStep("the build ran without the upgrade")
	}
	out, err := a.opt.Runner.Run(ctx, "apt-get", "-s", "dist-upgrade")
	if err != nil {
		return failedStep("%v", err)
	}
	n, ok := aptPending(string(out))
	if !ok {
		return failedStep("cannot read the summary of apt-get -s dist-upgrade")
	}
	if n > 0 {
		return failedStep("the upgrade is not complete: %d packages left. End of %s:\n%s",
			n, cloudInitLog, readGuestTail(a.opt.Root, cloudInitLog, 2048))
	}
	return applied("nothing left to upgrade")
}

// (4) Stop the log writers for the rest of this boot.
func (a *Agent) sealLogStop(ctx context.Context, _ wire.SealBody) (wire.Status, string) {
	var done []string
	for _, u := range []string{"rsyslog.service", "syslog.socket"} {
		if u == "syslog.socket" && len(done) == 0 {
			break // only together with rsyslog
		}
		if !a.unitIn(u) {
			continue
		}
		if _, err := a.opt.Runner.Run(ctx, "systemctl", "stop", u); err != nil {
			return failedStep("%v", err)
		}
		done = append(done, u)
	}
	if _, err := a.opt.Runner.Run(ctx, "journalctl", "--relinquish-var"); err != nil {
		return failedStep("%v", err)
	}
	return applied(strings.Join(append(done, "journal to /run"), ", ") + " stopped")
}

// (5) cloud-init runs again in every clone, with a new machine-id.
func (a *Agent) sealCloudInit(ctx context.Context, _ wire.SealBody) (wire.Status, string) {
	if err := removeGuest(a.opt.Root, CloudInitFlag); err != nil {
		return failedStep("%v", err)
	}
	if _, err := a.opt.Runner.Run(ctx, "cloud-init", "clean", "--logs", "--seed", "--machine-id", "--configs", "all"); err != nil {
		return failedStep("%v", err)
	}
	if err := a.resetIdentityFiles(); err != nil {
		return failedStep("%v", err)
	}
	return applied("cloud-init state, logs, seed, configs and machine-id reset; hostname " + neutralHostname)
}

// resetIdentityFiles removes the build guest's identity from the files in
// /etc that a clone must make again (T-066 F1): /etc/hostname gets the
// neutral name (cloud-init's set_hostname sets the clone's own name at its
// first boot), the build name leaves /etc/hosts, and /etc/machine-id gets
// "uninitialized" (as "cloud-init clean --machine-id" writes it; systemd
// makes a new id at the first boot). In place, never through a link.
func (a *Agent) resetIdentityFiles() error {
	old, err := readGuestFile(a.opt.Root, "/etc/hostname", 4096)
	if err != nil {
		return err
	}
	old = strings.TrimSpace(old)
	if err := rewriteGuestFile(a.opt.Root, "/etc/hostname", []byte(neutralHostname+"\n")); err != nil {
		return err
	}
	if old != "" && old != neutralHostname {
		hosts, err := readGuestFile(a.opt.Root, "/etc/hosts", 1<<20)
		if err != nil {
			return err
		}
		if clean := cleanHosts(hosts, old); clean != hosts {
			if err := rewriteGuestFile(a.opt.Root, "/etc/hosts", []byte(clean)); err != nil {
				return err
			}
		}
	}
	// A link fails here (readGuestFile); a missing file stays missing.
	id, err := readGuestFile(a.opt.Root, "/etc/machine-id", 4096)
	if err != nil {
		return err
	}
	if id != "" || guestRegular(a.opt.Root, "/etc/machine-id") {
		return rewriteGuestFile(a.opt.Root, "/etc/machine-id", []byte("uninitialized\n"))
	}
	return nil
}

// neutralHostname is the hostname of a template (T-066 F1).
const neutralHostname = "localhost"

// cleanHosts drops the name host (and host.<domain>) from every line of
// an /etc/hosts text; a line without a name left is dropped. Comments and
// other lines stay as they are.
func cleanHosts(text, host string) string {
	lines := strings.SplitAfter(text, "\n")
	var b strings.Builder
	for _, line := range lines {
		body, comment, _ := strings.Cut(strings.TrimRight(line, "\n"), "#")
		f := strings.Fields(body)
		if len(f) < 2 {
			b.WriteString(line)
			continue
		}
		keep := []string{f[0]}
		for _, n := range f[1:] {
			if n != host && !strings.HasPrefix(n, host+".") {
				keep = append(keep, n)
			}
		}
		switch {
		case len(keep) == len(f):
			b.WriteString(line)
		case len(keep) == 1:
			// The line named only the build host: dropped.
		default:
			out := strings.Join(keep, "\t")
			if comment != "" {
				out += " #" + comment
			}
			b.WriteString(out + "\n")
		}
	}
	return b.String()
}

// (6) D-185: every clone makes its own SSH host keys.
func (a *Agent) sealHostKeys(_ context.Context, _ wire.SealBody) (wire.Status, string) {
	n := 0
	for _, name := range guestEntries(a.opt.Root, "/etc/ssh") {
		if !strings.HasPrefix(name, "ssh_host_") {
			continue
		}
		if err := removeGuest(a.opt.Root, "/etc/ssh/"+name); err != nil {
			return failedStep("%v", err)
		}
		n++
	}
	return applied(fmt.Sprintf("%d host key files removed", n))
}

// (7) The user of the build guest. userdel -r removes the home directory,
// so it runs only when the home is the fixed one and a real directory: a
// Block may have changed the user.
func (a *Agent) sealBuildUser(ctx context.Context, _ wire.SealBody) (wire.Status, string) {
	home, found, err := passwdHome(a.opt.Root, buildUser)
	if err != nil {
		return failedStep("%v", err)
	}
	text := "no user " + buildUser
	if found {
		if home != buildHome {
			return failedStep("user %s has the home %q, not %s; not removed", buildUser, home, buildHome)
		}
		if fi, err := os.Lstat(filepath.Join(a.opt.Root, buildHome)); err == nil && !fi.IsDir() {
			return failedStep("%s is not a directory; not removed", buildHome)
		}
		// pkill exits 1 when no process matched.
		if _, err := a.opt.Runner.Run(ctx, "pkill", "-KILL", "-u", buildUser); err != nil && exitCodeOf(err) != 1 {
			return failedStep("%v", err)
		}
		if _, err := a.opt.Runner.Run(ctx, "userdel", "-r", "-f", buildUser); err != nil {
			return failedStep("%v", err)
		}
		text = "user " + buildUser + " removed"
	}
	if err := removeGuest(a.opt.Root, buildSudoers); err != nil {
		return failedStep("%v", err)
	}
	// T-066 F3: the backups that shadow-utils keeps still name the user.
	// They are only backups: removed (a link is unlinked, never followed).
	for _, b := range accountBackups {
		if err := removeGuest(a.opt.Root, b); err != nil {
			return failedStep("%v", err)
		}
	}
	return applied(text)
}

// (8) The random seed and the credential secret of systemd are per guest.
// T-066 F2: systemd-random-seed.service saves a new seed when it stops,
// also at the power-off. Stopping it here makes that save now; after the
// removal nothing writes the file again before the power-off.
func (a *Agent) sealSeed(ctx context.Context, _ wire.SealBody) (wire.Status, string) {
	if a.unitIn("systemd-random-seed.service") {
		if _, err := a.opt.Runner.Run(ctx, "systemctl", "stop", "systemd-random-seed.service"); err != nil {
			return failedStep("%v", err)
		}
	}
	for _, p := range []string{"/var/lib/systemd/random-seed", "/var/lib/systemd/credential.secret"} {
		if err := removeGuest(a.opt.Root, p); err != nil {
			return failedStep("%v", err)
		}
	}
	return applied("random seed and credential secret removed")
}

// (9) Logs, caches, temp files and histories (host C6: the logs held the
// hostname, the address and the user of the build guest).
func (a *Agent) sealLogs(ctx context.Context, _ wire.SealBody) (wire.Status, string) {
	if err := emptyGuestDir(a.opt.Root, "/var/log/journal"); err != nil {
		return failedStep("%v", err)
	}
	if err := emptyLogs(a.opt.Root, "/var/log"); err != nil {
		return failedStep("%v", err)
	}
	if _, err := a.opt.Runner.Run(ctx, "apt-get", "clean"); err != nil {
		return failedStep("%v", err)
	}
	for _, d := range []string{"/tmp", "/var/tmp"} {
		if err := emptyGuestDir(a.opt.Root, d); err != nil {
			return failedStep("%v", err)
		}
	}
	for _, h := range histories {
		if err := removeGuest(a.opt.Root, "/root/"+h); err != nil {
			return failedStep("%v", err)
		}
	}
	return applied("journal removed, /var/log emptied, apt cache, /tmp, /var/tmp and histories cleared")
}

// (10) Give the free blocks back to the thin pool. A disk without discard
// support only makes a larger template: not a failure.
func (a *Agent) sealTrim(ctx context.Context, _ wire.SealBody) (wire.Status, string) {
	if _, err := a.opt.Runner.Run(ctx, "fstrim", "--all"); err != nil {
		return skippedStep("fstrim did not run: " + err.Error())
	}
	return applied("free blocks trimmed")
}

// passwdHome reads the home of a user from the guest's /etc/passwd.
func passwdHome(root, user string) (home string, found bool, err error) {
	b, err := os.ReadFile(filepath.Join(root, "etc/passwd"))
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 7 && f[0] == user {
			return f[5], true, nil
		}
	}
	return "", false, nil
}

// exitCodeOf returns the exit code of a failed command, or -1.
func exitCodeOf(err error) int {
	var ee *resource.ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}
