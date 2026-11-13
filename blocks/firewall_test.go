package blocks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
)

// firewallDocs is an Image, the Block firewall and a Target of the kind
// with the given uses (T-119).
func firewallDocs(t *testing.T, kind string, uses []any) []state.Doc {
	t.Helper()
	image := map[string]any{"type": "vm", "url": "https://example.invalid/debian.img", "sha256": strings.Repeat("0", 64)}
	guest := map[string]any{"cores": 1, "memory": 1024, "disk": 10, "bridge": "vmbr0"}
	if kind == "lxc" {
		image = map[string]any{"type": "lxc", "url": "http://example.invalid/debian.tar.zst", "sha512": strings.Repeat("0", 128)}
		guest["disk"] = 4
	}
	img, _ := json.Marshal(image)
	spec, err := json.Marshal(map[string]any{"type": kind, "image": "img", "guest": guest, "blocks": uses})
	if err != nil {
		t.Fatal(err)
	}
	return []state.Doc{
		{Kind: state.KindImage, Name: "img", Spec: img},
		blockDoc(t, "firewall.yaml"),
		blockDoc(t, "ssh-hardening.yaml"),
		{Kind: state.KindTarget, Name: "t1", Spec: spec},
	}
}

func TestFirewallBlockRender(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
		want   map[string]any
	}{
		{"defaults", nil, map[string]any{
			"name": "main", "policy": "drop", "tcp_ports": "", "udp_ports": "", "sources": "",
			"ssh_port": "22", "ssh_sources": "",
		}},
		{"web", map[string]any{
			"tcp_ports": "80,443,8000-8100", "udp_ports": "51820", "sources": "192.0.2.0/24,2001:db8::/32",
			"ssh_port": "2222", "ssh_sources": "198.51.100.0/24",
		}, map[string]any{
			"name": "main", "policy": "drop", "tcp_ports": "80,443,8000-8100", "udp_ports": "51820",
			"sources": "192.0.2.0/24,2001:db8::/32", "ssh_port": "2222", "ssh_sources": "198.51.100.0/24",
		}},
	} {
		for _, kind := range []string{"vm", "lxc"} {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				use := map[string]any{"use": "firewall"}
				if tc.params != nil {
					use["params"] = tc.params
				}
				set, err := state.Validate(firewallDocs(t, kind, []any{use, map[string]any{"use": "ssh-hardening"}}))
				if err != nil {
					t.Fatal(err)
				}
				r, err := set.Render("t1")
				if err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, res := range r.Resources {
					if res.Block != "firewall" {
						continue
					}
					ids = append(ids, res.ID)
					if res.ID != "firewall:main" {
						continue
					}
					for k, v := range tc.want {
						if res.Attrs[k] != v {
							t.Errorf("%s = %#v, want %#v", k, res.Attrs[k], v)
						}
					}
					if _, ok := res.Attrs["allow_no_ssh"]; ok {
						t.Error("the Block must never set allow_no_ssh")
					}
				}
				// nft must be installed before the agent writes the table.
				if strings.Join(ids, " ") != "package:nftables firewall:main" {
					t.Fatalf("ids %v", ids)
				}
			})
		}
	}
}

// A Target has one firewall: a second use of the Block is refused at save.
func TestFirewallBlockOncePerTarget(t *testing.T) {
	uses := []any{
		map[string]any{"use": "firewall"},
		map[string]any{"use": "firewall", "id": "firewall-2", "params": map[string]any{"tcp_ports": "80"}},
	}
	set, err := state.Validate(firewallDocs(t, "vm", uses))
	if err == nil {
		_, err = set.Render("t1")
	}
	if err == nil {
		t.Fatal("two firewall uses accepted")
	}
	if !strings.Contains(err.Error(), "is already defined") {
		t.Fatalf("refused for another reason: %v", err)
	}
}
