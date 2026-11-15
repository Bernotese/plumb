package driver

import (
	"regexp"
	"testing"
)

// D-400: Plumb's MAC is fixed per install, Target and NIC, local and unicast.
func TestNICMAC(t *testing.T) {
	m := NICMAC("6f1c0b4e-0000-4000-8000-000000000001", "web1", 1)
	if !regexp.MustCompile(`^02(:[0-9a-f]{2}){5}$`).MatchString(m) || m != NICMAC("6f1c0b4e-0000-4000-8000-000000000001", "web1", 1) {
		t.Fatalf("mac %q", m)
	}
	seen := map[string]bool{m: true}
	for _, o := range []string{NICMAC("6f1c0b4e-0000-4000-8000-000000000002", "web1", 1), NICMAC("6f1c0b4e-0000-4000-8000-000000000001", "web2", 1),
		NICMAC("6f1c0b4e-0000-4000-8000-000000000001", "web1", 2), NICMAC("6f1c0b4e-0000-4000-8000-000000000001web1", "", 1)} {
		if seen[o] {
			t.Errorf("same MAC for other inputs: %s", o)
		}
		seen[o] = true
	}
	for in, want := range map[string]int{"nic1": 1, "nic3": 3, "nic0": 0, "nic4": 0, "nic01": 0, "nic": 0, "nics": 0, "bridge": 0} {
		if NICIndex(in) != want {
			t.Errorf("NICIndex(%q) = %d", in, NICIndex(in))
		}
	}
	n := NIC{Bridge: "vmbr9", Tag: 20, IP: "10.9.20.5/24", Gateway: "10.9.20.1", MAC: m}
	back, err := ParseNICValue(n.Value())
	if err != nil || back != n {
		t.Errorf("round trip: %+v %v", back, err)
	}
	for _, bad := range []any{"", "bridge=vmbr9", "mac=02:00:00:00:00:01", "bridge=vmbr9,tag=0,mac=x", "bridge=vmbr9,mac=x,trunks=1", 7} {
		if _, err := ParseNICValue(bad); err == nil {
			t.Errorf("ParseNICValue(%v) accepted", bad)
		}
	}
}
