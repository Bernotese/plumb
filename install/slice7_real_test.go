package install

// T-218: the real plumb binary reads the slice 7 lines of the plumb.env
// that install.sh wrote (the fake-PVE tests check only the text). serve
// reads them before the Proxmox client, so a run that gets to the CA file
// (missing here) has accepted them.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/secret"
)

func TestRealPlumbReadsSlice7Env(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.addStorage("plumb-backup", "dir", "backup", "")
	root := []string{"PLUMB_INSTALL_ASSUME_ROOT=1", "TMPDIR=" + h.tmpDir()}
	if code, out, errOut := h.run(root, append([]string{"--yes", "--plumb-binary", h.plumb}, slice7Flags...)...); code != 0 {
		t.Fatalf("install: %d\n%s\n%s", code, out, errOut)
	}
	raw, err := os.ReadFile(filepath.Join(h.state, "push", "_etc_plumb_plumb.env"))
	if err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, line := range strings.Split(string(raw), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			env = append(env, line)
		}
	}
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "key"), 0o700)
	if err := secret.WriteKeyFile(filepath.Join(data, "key", "master.key"), secret.NewKey()); err != nil {
		t.Fatal(err)
	}
	serve := func(env []string) (int, string) {
		t.Helper()
		cmd := exec.Command(realPlumb(t), "serve", "--data", data, "--hosts", "192.0.2.50,plumb")
		cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + data}, env...)
		out, err := cmd.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), string(out)
		} else if err != nil {
			t.Fatal(err)
		}
		return 0, string(out)
	}
	set := func(k, v string) []string {
		out := []string{}
		for _, l := range env {
			if !strings.HasPrefix(l, k+"=") {
				out = append(out, l)
			}
		}
		return append(out, k+"="+v)
	}
	for _, e := range [][]string{env, set("PLUMB_PVE_EXTRA_NETS", ""), set("PLUMB_PVE_GUEST_BACKUP_CLEAR", "0")} {
		code, out := serve(e)
		if code != 78 || !strings.Contains(out, "cannot read the Proxmox CA file /etc/plumb/pve-root-ca.pem") || strings.Contains(out, "PLUMB_PVE_EXTRA") || strings.Contains(out, "GUEST_BACKUP") {
			t.Errorf("serve with the installer's lines: exit %d\n%s", code, out)
		}
	}
	// A bad line stops serve before Proxmox, with exit 78 and the name.
	for k, v := range map[string]string{"PLUMB_PVE_EXTRA_NETS": "vmbr9/0", "PLUMB_PVE_GUEST_BACKUP_CLEAR": "yes", "PLUMB_PVE_GUEST_BACKUP_STORAGE": "a b"} {
		if code, out := serve(set(k, v)); code != 78 || !strings.Contains(out, k) || strings.Contains(out, "CA file") {
			t.Errorf("%s=%s: exit %d\n%s", k, v, code, out)
		}
	}
}
