package plan

import (
	"strings"
	"testing"
)

// T-278: the timeout text names where to look, with the container's VMID
// when the binding has one; a ref that is not a VMID is never printed.
func TestNotOnlineHint(t *testing.T) {
	for ref, want := range map[string]string{"9001": "pct exec 9001 -- journalctl -u plumb-agent-install -u plumb-agent", "web1": "pct exec <VMID> --", "": "pct exec <VMID> --"} {
		h := notOnlineHint(ref)
		if !strings.Contains(h, want) || !strings.Contains(h, "a VM: /var/log/cloud-init-output.log") {
			t.Errorf("notOnlineHint(%q) = %q, want %q", ref, h, want)
		}
	}
}
