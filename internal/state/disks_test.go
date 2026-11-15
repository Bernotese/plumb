package state

import (
	"strings"
	"testing"
)

// T-237 (review M2 of #360): the disk rules that the schema cannot hold
// are code. Each case must be refused with its own message, before and
// next to the planned refusal of T-238.
func TestDiskRules(t *testing.T) {
	img := Doc{Kind: KindImage, Name: "deb", Spec: []byte(`{"type":"vm","url":"https://example.invalid/i.img","sha256":"` + strings.Repeat("0", 64) + `"}`)}
	ctImg := Doc{Kind: KindImage, Name: "ct", Spec: []byte(`{"type":"lxc","url":"http://download.proxmox.com/a.tar.zst","sha512":"` + strings.Repeat("0", 128) + `"}`)}
	target := func(typ, disks string) Doc {
		image := "deb"
		if typ == "lxc" {
			image = "ct"
		}
		return Doc{Kind: KindTarget, Name: "web1", Spec: []byte(`{"type":"` + typ + `","image":"` + image + `","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","disks":` + disks + `}}`)}
	}
	for _, c := range []struct{ name, typ, disks, want string }{
		{"duplicate slot", "vm", `[{"slot":2,"size_gb":1},{"slot":2,"size_gb":5}]`, "slot 2 is also used by disk 0"},
		{"vm with path", "vm", `[{"slot":1,"size_gb":1,"path":"/srv/a"}]`, "a VM data disk has no path"},
		{"lxc without path", "lxc", `[{"slot":1,"size_gb":1}]`, "needs a path"},
		{"etc", "lxc", `[{"slot":1,"size_gb":1,"path":"/etc"}]`, "holds, a path of the Plumb agent"},
		{"var below", "lxc", `[{"slot":1,"size_gb":1,"path":"/var/lib/mysql"}]`, "system tree /var"},
		{"usr local", "lxc", `[{"slot":1,"size_gb":1,"path":"/usr/local"}]`, "holds, a path of the Plumb agent"},
		{"lib64", "lxc", `[{"slot":1,"size_gb":1,"path":"/lib64"}]`, "system tree /lib64"},
		{"root", "lxc", `[{"slot":1,"size_gb":1,"path":"/root/x"}]`, "system tree /root"},
		{"same path", "lxc", `[{"slot":1,"size_gb":1,"path":"/srv/a"},{"slot":2,"size_gb":1,"path":"/srv/a"}]`, "at or inside the path /srv/a of disk 0"},
		{"inside", "lxc", `[{"slot":1,"size_gb":1,"path":"/srv"},{"slot":2,"size_gb":1,"path":"/srv/a"}]`, "at or inside the path /srv of disk 0"},
		{"over", "lxc", `[{"slot":1,"size_gb":1,"path":"/srv/a/b"},{"slot":2,"size_gb":1,"path":"/srv/a"}]`, "at or inside the path /srv/a/b of disk 0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Validate([]Doc{img, ctImg, target(c.typ, c.disks)})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
	// A prefix that is not a parent is no conflict; allowed trees pass the
	// rules.
	for _, disks := range []string{
		`[{"slot":1,"size_gb":1,"path":"/srv/a"},{"slot":2,"size_gb":1,"path":"/srv/ab"}]`,
		`[{"slot":3,"size_gb":1,"path":"/opt/x"},{"slot":1,"size_gb":1,"path":"/home/u"},{"slot":2,"size_gb":1,"path":"/mnt/.cache"}]`,
		`[{"slot":1,"size_gb":1,"path":"/data/..."},{"slot":2,"size_gb":1,"path":"/varnish"}]`,
	} {
		if _, err := Validate([]Doc{img, ctImg, target("lxc", disks)}); err != nil {
			t.Errorf("%s: want valid, got %v", disks, err)
		}
	}
	// "." and ".." parts: the schema refuses them, and the code again.
	for _, p := range []string{"/srv/../etc", "/./etc", "/srv/..", "/srv/.", "/../srv"} {
		_, err := Validate([]Doc{img, ctImg, target("lxc", `[{"slot":1,"size_gb":1,"path":"`+p+`"}]`)})
		if err == nil || strings.Contains(err.Error(), "planned") {
			t.Errorf("%s: the schema must refuse it: %v", p, err)
		}
		if msg := diskPathProblem(p); msg == "" {
			t.Errorf("%s: the code must refuse it too", p)
		}
	}
	for _, p := range []string{"/srv//a", "/srv/", "srv", "/", "/etc/plumb-agent", "/var/lib/plumb-agent/x", "/usr/local/bin/plumb-agent", "/proc", "/sys/x", "/dev/sda", "/boot", "/run/x", "/bin", "/sbin", "/lib"} {
		if diskPathProblem(p) == "" {
			t.Errorf("%s: want a refusal", p)
		}
	}
	for _, p := range []string{"/srv/data", "/opt/app", "/data", "/mnt/x", "/home/u", "/srv/.hidden", "/srv/..x"} {
		if msg := diskPathProblem(p); msg != "" {
			t.Errorf("%s: %s", p, msg)
		}
	}
}

// N1 of the #360 re-check: a mount path that is a parent of an agent path
// is refused for that reason directly, not only as a system tree.
func TestDiskPathHoldsAgentPath(t *testing.T) {
	for _, p := range []string{"/etc", "/var/lib", "/var", "/usr/local", "/usr/local/bin", "/usr/local/sbin", "/etc/systemd", "/run/systemd", "/var/run", "/etc/plumb-agent", "/var/lib/plumb-agent/x"} {
		if msg := diskPathProblem(p); !strings.Contains(msg, "Plumb agent") {
			t.Errorf("%s: %q", p, msg)
		}
	}
	for _, p := range []string{"/srv/data", "/opt/etc", "/home/u/var/lib", "/mnt/plumb-agent"} {
		if msg := diskPathProblem(p); msg != "" {
			t.Errorf("%s: %q", p, msg)
		}
	}
}
