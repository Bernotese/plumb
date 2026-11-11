package install

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T-056 host run: a repair run pushed the new binary onto the running
// /usr/local/bin/plumb; pct push printed "Text file busy", exited 0, and
// the old binary kept running. The fake pct does the same.

func fileSum(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// installed returns a host with a finished install whose service runs.
func installed(t *testing.T) *host {
	t.Helper()
	h := newHost(t)
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("install: %d %s %s", code, out, errOut)
	}
	return h
}

func (h *host) running() string { return fileSum(h.t, filepath.Join(h.state, "running-exe")) }

func (h *host) newBinary() string {
	os.WriteFile(h.plumb, []byte("#!/bin/sh\necho plumb new build\n"), 0o755)
	return fileSum(h.t, h.plumb)
}

func TestRepairReplacesRunningBinary(t *testing.T) {
	h := installed(t)
	want := h.newBinary()
	code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("repair: %d %s %s", code, out, errOut)
	}
	if h.running() != want {
		t.Error("the service does not run the new binary after the repair run")
	}
	if fileSum(t, filepath.Join(h.state, "push", "_usr_local_bin_plumb")) != want {
		t.Error("/usr/local/bin/plumb is not the new binary")
	}
	if strings.Contains(errOut, "Text file busy") {
		t.Errorf("pushed onto the running binary: %s", errOut)
	}
}

// A push that fails silently (exit 0) stops the run before the rename and
// before the restart; the file in place and the running service stay.
func TestSilentPushFailureStops(t *testing.T) {
	for _, dest := range []string{"/usr/local/bin/plumb", "/etc/plumb/pve-root-ca.pem", "/etc/systemd/system/plumb.service"} {
		t.Run(dest, func(t *testing.T) {
			h := installed(t)
			old, placed := h.running(), filepath.Join(h.state, "push", strings.ReplaceAll(dest, "/", "_"))
			before := fileSum(t, placed)
			h.newBinary()
			os.Remove(filepath.Join(h.state, "calls"))
			code, _, errOut := h.run(append(asRoot, "FAKE_PUSH_FAIL="+dest), "--yes", "--plumb-binary", h.plumb)
			if code == 0 || !strings.Contains(errOut, "copy of "+dest+" in LXC 105 is missing or wrong") {
				t.Fatalf("silent push failure: %d %q", code, errOut)
			}
			if fileSum(t, placed) != before {
				t.Errorf("%s changed after a failed push", dest)
			}
			if h.running() != old || index(h.calls(), "pct exec 105 -- systemctl restart plumb.service") >= 0 {
				t.Error("the service was restarted or changed after a failed push")
			}
			tmp := filepath.Join(h.state, "push", strings.ReplaceAll(filepath.Dir(dest)+"/."+filepath.Base(dest)+".plumb-new", "/", "_"))
			if _, err := os.Stat(tmp); !os.IsNotExist(err) {
				t.Errorf("temp file left: %v", err)
			}
		})
	}
	// plumb.env is pushed when the token is new: a fresh install.
	h := newHost(t)
	code, _, errOut := h.run(append(asRoot, "FAKE_PUSH_FAIL=/etc/plumb/plumb.env"), "--yes", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "cannot write the settings into the LXC") {
		t.Errorf("silent push failure of plumb.env: %d %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(h.state, "push", "_etc_plumb_plumb.env")); !os.IsNotExist(err) {
		t.Errorf("plumb.env in place after a failed push: %v", err)
	}
}

// After the restart the service must run the new binary.
func TestRestartMustRunNewBinary(t *testing.T) {
	h := installed(t)
	h.newBinary()
	code, _, errOut := h.run(append(asRoot, "FAKE_RESTART_OLD=1", "PLUMB_INSTALL_RUNNING_TRIES=2"), "--yes", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "does not run the new binary after the restart") {
		t.Errorf("old process after the restart: %d %q", code, errOut)
	}
}

// A failed pct exec names itself and stops the run.
func TestFailedPctExecStops(t *testing.T) {
	h := installed(t)
	os.Remove(filepath.Join(h.state, "calls"))
	code, _, errOut := h.run(append(asRoot, "FAKE_EXEC_FAIL=systemctl daemon-reload"), "--yes", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, `"pct exec" failed (exit 3)`) {
		t.Errorf("failed pct exec: %d %q", code, errOut)
	}
	if index(h.calls(), "pct exec 105 -- systemctl restart plumb.service") >= 0 {
		t.Error("the run went on after the failed pct exec")
	}
}

// Review nit 1: a new binary in a crash loop fails the run; the journal
// lines are shown with the password masked.
func TestCrashLoopFails(t *testing.T) {
	h := installed(t)
	h.newBinary()
	code, _, errOut := h.run(append(asRoot, "FAKE_CRASH_LOOP=1", "PLUMB_INSTALL_RUNNING_TRIES=2", "PLUMB_INSTALL_STAY_WAIT=0"), "--yes", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "does not stay up with the new binary") {
		t.Errorf("crash loop: %d %q", code, errOut)
	}
	if strings.Contains(errOut, "pw-from-journal") || !strings.Contains(errOut, "Password: <hidden>") {
		t.Errorf("journal lines leak the password or are missing: %q", errOut)
	}
}

// Review nit 2: a failed rename removes the temp file.
func TestFailedRenameRemovesTemp(t *testing.T) {
	h := installed(t)
	h.newBinary()
	code, _, errOut := h.run(append(asRoot, "FAKE_MV_FAIL=1"), "--yes", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "cannot put /usr/local/bin/plumb in place") {
		t.Errorf("failed rename: %d %q", code, errOut)
	}
	left, _ := filepath.Glob(filepath.Join(h.state, "push", "*.plumb-new*"))
	if len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}
