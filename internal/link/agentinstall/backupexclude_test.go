package agentinstall

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// T-231, F1 of #345: every name under which install.sh writes the
// one-shot token is left out of a container backup, so the list cannot
// drift from the script.
func TestTokenFilesExcludedFromBackup(t *testing.T) {
	s := string(script)
	if !strings.Contains(s, "\netc=/etc/plumb-agent\n") {
		t.Fatal("install.sh no longer sets etc=/etc/plumb-agent")
	}
	names := regexp.MustCompile(`\$etc/([A-Za-z0-9._-]*token[A-Za-z0-9._-]*)`).FindAllStringSubmatch(s, -1)
	if len(names) == 0 {
		t.Fatal("no token file in install.sh")
	}
	seen := map[string]bool{}
	for _, m := range names {
		p := "/etc/plumb-agent/" + m[1]
		seen[p] = true
		if !wire.BackupExcluded(p) {
			t.Errorf("install.sh writes the token to %s, and a container backup keeps it", p)
		}
	}
	for _, p := range []string{wire.RepairTokenFile, "/etc/plumb-agent/.enroll.token.new"} {
		if !seen[p] {
			t.Errorf("install.sh does not name %s any more: check this test", p)
		}
	}
}
