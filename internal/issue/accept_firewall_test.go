package issue

import (
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// Accept never takes the SSH rule or the table of a firewall from guest
// drift (D-300, review L2 of #219).
func TestAcceptRefusedFirewall(t *testing.T) {
	ch := func(attr string, from any) []wire.Change {
		return []wire.Change{{Attr: attr, From: from, To: nil, Risk: wire.RiskDisruptive}}
	}
	for _, attr := range []string{"allow_no_ssh", "ssh_port", "ssh_sources", "ssh", "table"} {
		if err := AcceptRefused("firewall:main", ch(attr, true)); !errors.Is(err, ErrAcceptFirewall) {
			t.Errorf("%s: %v", attr, err)
		}
	}
	mixed := append(ch("policy", "accept"), ch("allow_no_ssh", true)...)
	if err := AcceptRefused("firewall:main", mixed); !errors.Is(err, ErrAcceptFirewall) {
		t.Errorf("mixed: %v", err)
	}
	for _, attr := range []string{"policy", "tcp_ports", "udp_ports", "sources", "ping"} {
		if err := AcceptRefused("firewall:main", ch(attr, "x")); err != nil {
			t.Errorf("%s: %v", attr, err)
		}
	}
	// Only the firewall: a file attribute of that name is no SSH rule.
	if err := AcceptRefused("file:/x", ch("ssh_port", "22")); err != nil {
		t.Errorf("file: %v", err)
	}
}
