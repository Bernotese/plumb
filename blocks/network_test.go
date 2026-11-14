package blocks

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/state"
	"go.yaml.in/yaml/v3"
)

// networkDocs is an Image, the Block network and a Target of the kind with
// the given uses (T-156, D-332). Example values come from the
// documentation ranges only.
func networkDocs(t *testing.T, kind string, uses []any) []state.Doc {
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
		blockDoc(t, "network.yaml"),
		{Kind: state.KindTarget, Name: "t1", Spec: spec},
	}
}

func renderNetwork(t *testing.T, kind string, uses ...any) (*state.Rendered, error) {
	t.Helper()
	set, err := state.Validate(networkDocs(t, kind, uses))
	if err != nil {
		return nil, err
	}
	return set.Render("t1")
}

func TestNetworkBlockRender(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
		want   map[string]any
	}{
		{"defaults", nil, map[string]any{
			"name": "main", "addresses": "", "routes": "", "nameservers": "", "search": "",
		}},
		{"lab", map[string]any{
			"addresses":   "192.0.2.10/24,2001:db8::10/64",
			"routes":      "198.51.100.0/24 via 192.0.2.1 metric 100;203.0.113.0/24 via 192.0.2.1",
			"nameservers": "192.0.2.53,2001:db8::53",
			"search":      "lab.example,example.org",
		}, map[string]any{
			"name": "main", "addresses": "192.0.2.10/24,2001:db8::10/64",
			"routes":      "198.51.100.0/24 via 192.0.2.1 metric 100;203.0.113.0/24 via 192.0.2.1",
			"nameservers": "192.0.2.53,2001:db8::53", "search": "lab.example,example.org",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			use := map[string]any{"use": "network"}
			if tc.params != nil {
				use["params"] = tc.params
			}
			r, err := renderNetwork(t, "vm", use)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, res := range r.Resources {
				if res.Block != "network" {
					continue
				}
				ids = append(ids, res.ID)
				for k, v := range tc.want {
					if res.Attrs[k] != v {
						t.Errorf("%s = %#v, want %#v", k, res.Attrs[k], v)
					}
				}
				// The server's own value check (the plan runs it too).
				if _, err := resource.CheckNetworkAttrs(res.Attrs); err != nil {
					t.Errorf("server check: %v", err)
				}
			}
			if strings.Join(ids, " ") != "network:main" {
				t.Fatalf("ids %v", ids)
			}
		})
	}
}

// Two uses with two names add up; the same name twice is refused at save.
func TestNetworkBlockTwice(t *testing.T) {
	dns := map[string]any{"use": "network", "id": "network-dns", "params": map[string]any{
		"name": "dns", "nameservers": "198.51.100.53",
	}}
	r, err := renderNetwork(t, "vm", map[string]any{"use": "network", "params": map[string]any{"addresses": "192.0.2.10/24"}}, dns)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, res := range r.Resources {
		ids = append(ids, res.ID)
	}
	if strings.Join(ids, " ") != "network:main network:dns" {
		t.Fatalf("ids %v", ids)
	}

	delete(dns["params"].(map[string]any), "name")
	if _, err := renderNetwork(t, "vm", map[string]any{"use": "network"}, dns); err == nil ||
		!strings.Contains(err.Error(), "is already defined") {
		t.Fatalf("same name twice: %v", err)
	}
}

// Proxmox owns the network of a container: the Block is refused on an LXC
// Target at save.
func TestNetworkBlockRefusedOnLXC(t *testing.T) {
	_, err := renderNetwork(t, "lxc", map[string]any{"use": "network", "params": map[string]any{"addresses": "192.0.2.10/24"}})
	if err == nil {
		t.Fatal("network on an LXC Target accepted")
	}
	if !strings.Contains(err.Error(), "resource type network does not support lxc targets") {
		t.Fatalf("refused for another reason: %v", err)
	}
}

// An Image build cannot hold the Block (D-355): every clone would get the
// same extra addresses and routes.
func TestNetworkBlockRefusedInImage(t *testing.T) {
	spec, _ := json.Marshal(map[string]any{
		"type": "vm", "url": "https://example.invalid/debian.img", "sha256": strings.Repeat("0", 64),
		"build": map[string]any{
			"blocks": []any{map[string]any{"use": "network", "params": map[string]any{"addresses": "192.0.2.10/24"}}},
			"guest":  map[string]any{"disk": 10, "bridge": "vmbr0"},
		},
	})
	_, err := state.Validate([]state.Doc{blockDoc(t, "network.yaml"), {Kind: state.KindImage, Name: "img", Spec: spec}})
	if err == nil {
		t.Fatal("network in an Image build accepted")
	}
	if !strings.Contains(err.Error(), "cannot hold a network resource") {
		t.Fatalf("refused for another reason: %v", err)
	}
}

// The parameter patterns refuse what could break the netplan file or the
// list syntax; the server check refuses what the patterns let through.
func TestNetworkBlockRejectsUnsafeValues(t *testing.T) {
	raw, err := os.ReadFile("network.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	props := doc["spec"].(map[string]any)["params"].(map[string]any)["properties"].(map[string]any)
	for param, invalid := range map[string][]any{
		"name":        {"", "Main", "-x", "a_b", "../x", "a\n", strings.Repeat("a", 33)},
		"addresses":   {"192.0.2.10", "192.0.2.10/24,", "192.0.2.10/24 ", "192.0.2.10/24\n", "192.0.2.10/24;x", 1},
		"routes":      {"198.51.100.0/24", "198.51.100.0/24 via", "198.51.100.0/24 via 192.0.2.1;", "198.51.100.0/24 dev eth0", "198.51.100.0/24 via 192.0.2.1\n"},
		"nameservers": {"192.0.2.53,", "192.0.2.53 192.0.2.54", "dns.example", "192.0.2.53\n"},
		"search":      {"lab.example,", ".lab", "lab example", "lab.example\n", "-lab"},
	} {
		schema := compileSchemaValue(t, props[param])
		for _, v := range invalid {
			if err := schema.Validate(v); err == nil {
				t.Errorf("accepted unsafe %s=%q", param, v)
			}
		}
	}

	// Through the pattern, refused by the server check (D-332, D-348).
	for _, tc := range []struct{ attr, value, why string }{
		{"routes", "0.0.0.0/0 via 192.0.2.1", "default route"},
		{"routes", "::/0 via 2001:db8::1", "default route"},
		{"addresses", "127.0.0.2/8", "loopback"},
		{"addresses", "169.254.0.10/16", "link-local"},
		{"addresses", "192.0.2.10/33", "prefix"},
		{"nameservers", "224.0.0.1", "multicast"},
	} {
		uses := map[string]any{"use": "network", "params": map[string]any{tc.attr: tc.value}}
		r, err := renderNetwork(t, "vm", uses)
		if err == nil {
			for _, res := range r.Resources {
				if res.Type == "network" {
					_, err = resource.CheckNetworkAttrs(res.Attrs)
				}
			}
		}
		if err == nil {
			t.Errorf("%s=%q accepted", tc.attr, tc.value)
		} else if !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%s=%q refused for another reason: %v", tc.attr, tc.value, err)
		}
	}
}
