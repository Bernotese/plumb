//go:build guest

package agent

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/resource"
)

// D-431 (T-243): the agent log scan of logscan_test.go on the real root
// of a guest, with the real tools (apt fails with the sensitive version
// on its command line). test/guest/run.sh runs it in the Debian and
// Ubuntu containers, test/guest/lxd.sh in an unprivileged LXC container
// and a VM of LXD.
func TestGuestLogHoldsNoSecret(t *testing.T) {
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires root in the systemd guest")
	}
	if err := os.MkdirAll("/srv", 0o755); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	log, values := logScanRun(t, "/", "/srv/plumb-logscan-"+hex.EncodeToString(b)+".env", resource.ExecRunner{})
	for _, line := range []string{"msg=enrolled", `msg="enrolled with the one-shot token"`} {
		if !strings.Contains(log, line) {
			t.Fatalf("the agent log has no %s line:\n%s", line, log)
		}
	}
	logScanCheck(t, log, values)
}
