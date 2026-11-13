package issue

import (
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// Accept never takes guest data into a firewall (D-300, review M1 of
// #218): every attribute is refused, also an empty change list.
func TestAcceptRefusedFirewall(t *testing.T) {
	for _, attr := range []string{"allow_no_ssh", "ssh_port", "ssh_sources", "ssh", "table", "policy", "tcp_ports", "udp_ports", "sources", "ping", "state"} {
		ch := []wire.Change{{Attr: attr, From: "x", Risk: wire.RiskDisruptive}}
		if err := AcceptRefused("firewall:main", ch); !errors.Is(err, ErrAcceptFirewall) {
			t.Errorf("%s: %v", attr, err)
		}
	}
	if err := AcceptRefused("firewall:main", nil); !errors.Is(err, ErrAcceptFirewall) {
		t.Errorf("no changes: %v", err)
	}
	// Only the firewall: a file attribute of such a name is accepted.
	if err := AcceptRefused("file:/x", []wire.Change{{Attr: "ssh_port", From: "22"}}); err != nil {
		t.Errorf("file: %v", err)
	}
}
