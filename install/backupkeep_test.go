package install

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/backup"
)

// T-198: --backup-keep has exactly the range of the controller
// (backup.MaxKeep, D-361); a larger value is refused before any change,
// instead of stopping plumb serve after the install. --backup-copy-keep is
// read only by the copy script on the host and keeps 1 … 999.
func TestBackupKeepRange(t *testing.T) {
	t.Parallel()
	b := newBackupHost(t)
	max := strconv.Itoa(backup.MaxKeep)
	over := strconv.Itoa(backup.MaxKeep + 1)
	for _, c := range []struct {
		args []string
		ok   bool
		msg  string
	}{
		{[]string{"--backup-keep", over}, false, "bad --backup-keep " + over + " (1 … " + max + "). Nothing was changed."},
		{[]string{"--backup-keep", "500"}, false, "(1 … " + max + ")"},
		{[]string{"--backup-keep", "007"}, false, "bad --backup-keep"},
		{[]string{"--backup-copy-keep", "1000"}, false, "bad --backup-copy-keep 1000 (1 … 999)"},
	} {
		before := len(b.calls())
		code, _, errOut := b.run(asRoot, append([]string{"--yes", "--plumb-binary", b.plumb}, c.args...)...)
		if code == 0 || !strings.Contains(errOut, c.msg) {
			t.Errorf("%v: code %d, stderr %q, want %q", c.args, code, errOut, c.msg)
		}
		for _, call := range b.calls()[before:] {
			if !strings.HasPrefix(call, "pveversion") && !strings.HasPrefix(call, "hostname") {
				t.Errorf("%v: a call before the refusal: %s", c.args, call)
			}
		}
	}
	// The controller's maximum and the copy keep's maximum are accepted.
	b.install("--backup-keep", max, "--backup-copy-keep", "999")
	env, _ := os.ReadFile(filepath.Join(b.state, "push", "_etc_plumb_plumb.env"))
	if !strings.Contains(string(env), "PLUMB_BACKUP_KEEP="+max+"\n") {
		t.Fatalf("plumb.env without PLUMB_BACKUP_KEEP=%s:\n%s", max, env)
	}
}
