package install

// T-132 (T-122 F7, F6): pct exec without a terminal, a wait for the boot
// after the first start, a checked mv after a SIGHUP, and a faithful dry
// run of a --restore rerun over a half-made LXC.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On a terminal, no pct exec gets one (lxc-attach would give the command a
// pty in the container); the key prompt keeps it (D-180).
func TestPctExecWithoutTerminal(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	code, out, errOut := h.runTTY(t, "y\n\n", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	if b, err := os.ReadFile(filepath.Join(h.state, "exec-tty")); err == nil {
		t.Errorf("pct exec with a terminal:\n%s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(h.state, "attach-tty")); !strings.Contains(string(b), "plumb key init") {
		t.Errorf("plumb key init without the terminal: %q", b)
	}
	// The output of pct exec still reaches the run (stdout and stderr).
	if !strings.Contains(out, "Plumb runs:") {
		t.Errorf("output lost:\n%s", out)
	}
}

const bootWait = "pct exec 105 -- timeout 120 systemctl is-system-running --wait"

// After the first start the run waits for the boot before any other step
// in the LXC; a boot that does not finish stops the run with a clear error.
func TestBootWaitAfterFirstStart(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	calls := h.calls()
	start, wait := index(calls, "pct start 105"), index(calls, bootWait)
	if start < 0 || wait < start {
		t.Fatalf("no boot wait after the start:\n%s", strings.Join(calls, "\n"))
	}
	for i, c := range calls[start+1 : wait] {
		if strings.HasPrefix(c, "pct exec 105") || strings.HasPrefix(c, "pct push 105") {
			t.Errorf("step %d before the boot wait: %s", i, c)
		}
	}

	h = newHost(t)
	code, _, errOut = h.run(append(asRoot, "FAKE_BOOT_STATE=starting"), "--yes", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "did not finish its boot within 120s (systemctl is-system-running: starting)") {
		t.Fatalf("unfinished boot: %d %s", code, errOut)
	}
	if index(h.calls(), "pct push 105") >= 0 {
		t.Error("a push after an unfinished boot")
	}
}

// T-122 F7: the mv ran, then pct exec reported SIGHUP. The run checks the
// file and goes on; a mv that did not run still stops it.
func TestMvHangupIsChecked(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	code, out, errOut := h.run(append(asRoot, "FAKE_MV_HUP_ONCE=/usr/local/bin/plumb"), "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "/usr/local/bin/plumb is in place (checksum checked); the run goes on") {
		t.Errorf("no note:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(h.state, "mv-hup")); err != nil {
		t.Fatal("the hangup did not happen")
	}
	if strings.Contains(errOut, "it was not changed") {
		t.Errorf("wrong message:\n%s", errOut)
	}

	h = newHost(t)
	code, _, errOut = h.run(append(asRoot, "FAKE_MV_FAIL=1"), "--yes", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "cannot put /usr/local/bin/plumb in place in LXC 105; it was not changed") {
		t.Errorf("failed mv: %d %s", code, errOut)
	}
}

// T-122 F6: the dry run of a --restore rerun over the half-made LXC of a
// failed restore shows what the real run does and changes nothing.
func TestRestoreRerunDryRun(t *testing.T) {
	t.Parallel()
	b, file := keptHost(t)
	net := []string{"--lxc-ip", "192.168.1.50/24", "--lxc-gw", "192.168.1.1"}
	if code, _, errOut := b.restore([]string{"FAKE_RESTORE_FAIL=1"}, file, net...); code == 0 {
		t.Fatalf("first restore did not fail: %s", errOut)
	}
	// Like a running LXC on a real host: its run dir holds more than the
	// known leftovers of a destroyed one.
	run := filepath.Join(filepath.Dir(b.state), "lxc", "105")
	os.MkdirAll(run, 0o755)
	os.WriteFile(filepath.Join(run, "config"), []byte("lxc.uts.name = plumb\n"), 0o644)
	b.resetCalls()
	code, out, errOut := b.restore(nil, file, append(net, "--dry-run")...)
	if code != 0 {
		t.Fatalf("dry run: %d\n%s\n%s", code, out, errOut)
	}
	lines := strings.Split(out, "\n")
	order := []string{"+ pct destroy 105 --purge 1", "+ pveum acl delete / --tokens plumb@pve\\!plumb --roles PlumbAudit",
		"+ pveum user token remove plumb@pve plumb", "+ pveum user token add plumb@pve plumb",
		"+ pveum acl modify / --tokens plumb@pve\\!plumb --roles PlumbAudit", "+ pct create 105",
		"+ pct push 105 " + file + " /var/lib/plumb-restore/.restore.pbk.plumb-new",
		"+ lxc-attach -n 105 --clear-env -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb key restore --data /var/lib/plumb",
		"+ lxc-attach -n 105 --clear-env -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb restore --data /var/lib/plumb --from /var/lib/plumb-restore/restore.pbk",
		"+ pct exec 105 -- rm -rf -- /var/lib/plumb-restore"}
	last := -1
	for _, c := range order {
		i := index(lines, c)
		if i <= last {
			t.Fatalf("%q at %d, after %d:\n%s", c, i, last, out)
		}
		last = i
	}
	var del, mod int
	for _, l := range lines {
		if strings.HasPrefix(l, "+ pveum acl delete") && strings.Contains(l, "--tokens") {
			del++
		}
		if strings.HasPrefix(l, "+ pveum acl modify") && strings.Contains(l, "--tokens") {
			mod++
		}
	}
	if del != 7 || mod != 7 {
		t.Errorf("token ACLs: %d deleted, %d set, want 7 and 7:\n%s", del, mod, out)
	}
	if strings.Contains(out+errOut, "master key already") || strings.Contains(out, "it is kept") {
		t.Errorf("the dry run reads the destroyed LXC or token:\n%s", out)
	}
	for _, c := range b.calls() {
		if mutating.MatchString(c) {
			t.Errorf("dry run changed the host: %s", c)
		}
	}
}
