package resource

import (
	"context"
	"net/netip"
	"testing"
)

func TestNetworkMAC(t *testing.T) {
	const good = "bc:24:11:00:00:01"
	for _, tc := range []struct{ name, reply, want string }{
		{"current", `[{"address":"BC:24:11:00:00:01"}]`, good},
		{"permanent", `[{"address":"bc:24:11:00:00:02","permaddr":"BC:24:11:00:00:01"}]`, good},
		{"invalid current ignored", `[{"address":"bad","permaddr":"bc:24:11:00:00:01"}]`, good},
		{"empty", `[{"address":""}]`, ""},
		{"malformed", `[{"address":"bad"}]`, ""},
		{"zero", `[{"address":"00:00:00:00:00:00"}]`, ""},
		{"broadcast", `[{"address":"ff:ff:ff:ff:ff:ff"}]`, ""},
		{"eui64", `[{"address":"bc:24:11:00:00:00:00:01"}]`, ""},
		{"empty permanent", `[{"address":"bc:24:11:00:00:01","permaddr":""}]`, ""},
		{"bad permanent", `[{"address":"bc:24:11:00:00:01","permaddr":"bad"}]`, ""},
		{"no link", `[]`, ""},
		{"two links", `[{},{}]`, ""},
		{"bad JSON", `{`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := &network{base: base{env: Env{Run: groupRunner(func(context.Context, string, ...string) ([]byte, error) { return []byte(tc.reply), nil })}}}
			got, err := n.liveMAC(context.Background(), "ens18")
			if tc.want == "" {
				if err == nil {
					t.Fatalf("accepted invalid MAC: %q", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			view := map[string]npView{"eth0": {DHCP4: true, Match: map[string]any{"macaddress": good}}}
			if key, err := mainKey(view, "ens18", got, netip.MustParseAddr("192.0.2.1")); err != nil || key != "eth0" {
				t.Fatalf("permanent match: %q, %v", key, err)
			}
		})
	}
}
