package resource

import (
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// T-219 (D-404): the firewall Block says it filters input on every
// interface, also on more NICs. The script matches no interface but lo,
// so a NIC on another bridge or VLAN is filtered like eth0.
func TestFirewallInputOnEveryInterface(t *testing.T) {
	for _, policy := range []string{"drop", "accept"} {
		script := fwScript(fwState{State: wire.StatePresent, Policy: policy, SSH: "tcp/22 from everywhere", TCPPorts: "80,443", Sources: "10.9.20.0/24"})
		if !strings.Contains(script, "type filter hook input priority filter; policy "+policy+";") {
			t.Fatalf("no input hook:\n%s", script)
		}
		for _, line := range strings.Split(script, "\n") {
			for _, m := range []string{"iif", "iifname", "oif", "meta iif"} {
				if strings.Contains(line, m+" ") && strings.TrimSpace(line) != `iif "lo" accept` {
					t.Errorf("a rule matches an interface: %q", line)
				}
			}
		}
	}
}
