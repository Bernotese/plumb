//go:build guest

package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// netplanEntry is the part of an ethernets entry of `netplan get` that the
// network resource writes or must keep.
type netplanEntry struct {
	Match       map[string]any `yaml:"match"`
	SetName     string         `yaml:"set-name"`
	Addresses   []string       `yaml:"addresses"`
	Routes      []struct{ To, Via string }
	Nameservers struct {
		Addresses []string `yaml:"addresses"`
		Search    []string `yaml:"search"`
	} `yaml:"nameservers"`
}

func netplanGet(t *testing.T, root string) netplanEntry {
	t.Helper()
	out, err := exec.Command("netplan", "get", "--root-dir", root, "ethernets.eth0").CombinedOutput()
	if err != nil {
		t.Fatalf("netplan get: %v\n%s", err, out)
	}
	var e netplanEntry
	if err := yaml.Unmarshal(out, &e); err != nil {
		t.Fatalf("parse netplan get: %v\n%s", err, out)
	}
	return e
}

// TestGuestNetplanMerge proves the netplan merge rule that D-348 builds on
// (T-150, review G3 of #244): a second file under the same ethernets key
// appends to addresses, routes, nameservers.addresses and search of the
// cloud-init file, and keeps match and set-name. It works in a temporary
// root only (netplan get --root-dir): the guest network does not change.
// The base file has the shape that cloud-init writes on a Plumb VM (T-160).
// PLUMB_NETPLAN_MERGE=1 makes a missing netplan a failure (CI).
// PLUMB_NETPLAN_WANT checks the version prefix of netplan.io.
func TestGuestNetplanMerge(t *testing.T) {
	if _, err := exec.LookPath("netplan"); err != nil {
		if os.Getenv("PLUMB_NETPLAN_MERGE") == "1" {
			t.Fatal("netplan is not installed")
		}
		t.Skip("netplan is not installed")
	}
	out, err := exec.Command("dpkg-query", "-W", "-f", "${Version}", "netplan.io").CombinedOutput()
	version := strings.TrimSpace(string(out))
	t.Logf("netplan.io %s (%v)", version, err)
	// PLUMB_NETPLAN_WANT: the version the CI guest must have (Debian: the
	// 1.1.2 of the vendor cloud image, T-160).
	if want := os.Getenv("PLUMB_NETPLAN_WANT"); want != "" && !strings.HasPrefix(version, want) {
		t.Fatalf("netplan.io %q, want %s", version, want)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "etc", "netplan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("50-cloud-init.yaml", `network:
  version: 2
  ethernets:
    eth0:
      match:
        macaddress: "bc:24:11:00:00:01"
      set-name: "eth0"
      addresses:
      - "198.51.100.5/24"
      routes:
      - to: "default"
        via: "198.51.100.1"
      nameservers:
        addresses:
        - 198.51.100.53
        search:
        - base.invalid
`)
	before := netplanGet(t, root)
	write("60-plumb-extra.yaml", `network:
  version: 2
  ethernets:
    eth0:
      addresses:
      - "192.0.2.10/24"
      - "2001:db8::10/64"
      routes:
      - to: "203.0.113.0/24"
        via: "192.0.2.1"
      nameservers:
        addresses:
        - 192.0.2.53
        search:
        - plumb.invalid
`)
	after := netplanGet(t, root)
	t.Logf("before: %+v", before)
	t.Logf("after:  %+v", after)

	appended := func(what string, base, got, ours []string) {
		t.Helper()
		if want := append(slices.Clone(base), ours...); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q (base, then ours): netplan replaced or reordered the list", what, got, want)
		}
	}
	appended("addresses", before.Addresses, after.Addresses, []string{"192.0.2.10/24", "2001:db8::10/64"})
	appended("nameservers.addresses", before.Nameservers.Addresses, after.Nameservers.Addresses, []string{"192.0.2.53"})
	appended("nameservers.search", before.Nameservers.Search, after.Nameservers.Search, []string{"plumb.invalid"})
	var to []string
	for _, r := range after.Routes {
		to = append(to, r.To+" via "+r.Via)
	}
	appended("routes", []string{"default via 198.51.100.1"}, to, []string{"203.0.113.0/24 via 192.0.2.1"})
	if after.SetName != "eth0" || after.Match["macaddress"] != "bc:24:11:00:00:01" {
		t.Errorf("match or set-name changed: %+v", after)
	}
	if len(before.Addresses) != 1 || len(before.Routes) != 1 {
		t.Fatalf("base file not read as written: %+v", before)
	}
}
