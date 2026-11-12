package resource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQAAptTrustBoundary(t *testing.T) {
	g := newFakeGuest(t)
	for name, extra := range map[string]map[string]any{
		"trusted field":          {"trusted": "yes"},
		"uri injection":          {"uris": []any{"https://example.org/\nTrusted: yes"}},
		"architecture injection": {"architectures": []any{"amd64\nTrusted: yes"}},
		"second key":             {"key": testKey + armor("mQINBFit2ioBEADh")},
		"foreign Signed-By":      {"signed_by": "/etc/apt/keyrings/other.asc"},
		"global Signed-By":       {"signed_by": "/etc/apt/trusted.gpg.d/other.asc"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(res("apt_repository", "qa", aptAttrs(extra)), g.env()); err == nil {
				t.Fatal("unsafe repository accepted")
			}
		})
	}
	mkdirs(t, g.root, "etc/apt/sources.list.d")
	r := build(t, res("apt_repository", "qa", aptAttrs(nil)), g.env())
	converge(t, r)
	src := filepath.Join(g.root, "etc/apt/sources.list.d/plumb-qa.sources")
	key := filepath.Join(g.root, "etc/apt/keyrings/plumb-qa.asc")
	original, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"foreign key": strings.ReplaceAll(string(original), "/etc/apt/keyrings/plumb-qa.asc", "/etc/apt/keyrings/other.asc"),
		"global key":  strings.ReplaceAll(string(original), "/etc/apt/keyrings/plumb-qa.asc", "/etc/apt/trusted.gpg.d/other.asc"),
		"trusted":     string(original) + "Trusted: yes\n",
	} {
		t.Run(name, func(t *testing.T) {
			mustWrite(t, src, content)
			if got := converge(t, r); attrsOf(got) != "sources=reversible" {
				t.Fatalf("drift = %+v", got)
			}
			restored, err := os.ReadFile(src)
			if err != nil || string(restored) != string(original) {
				t.Fatalf("source not restored: %q, %v", restored, err)
			}
		})
	}
	mustWrite(t, key, armor("mQINBFit2ioBEADh"))
	if got := converge(t, r); attrsOf(got) != "key=reversible" {
		t.Fatalf("key drift = %+v", got)
	}
	restored, err := os.ReadFile(key)
	if err != nil || string(restored) != testKey {
		t.Fatalf("key not restored: %q, %v", restored, err)
	}
}

func TestQAUnitAgentNames(t *testing.T) {
	g := newFakeGuest(t)
	for _, name := range []string{"plumb-agent.service", "plumb-agent-update.timer", "plumb-agent@qa.service", "plumb-agent-extra.socket"} {
		for _, state := range []string{"present", "absent"} {
			if _, err := New(res("unit", name, map[string]any{"content": "[Unit]\n", "state": state}), g.env()); err == nil {
				t.Errorf("accepted %s with state %s", name, state)
			}
		}
	}
}

func TestQAUnitDeletionDrift(t *testing.T) {
	g := newFakeGuest(t)
	mkdirs(t, g.root, "etc/systemd/system")
	r := build(t, res("unit", "qa.service", map[string]any{"content": "[Service]\nExecStart=/bin/true\n"}), g.env())
	converge(t, r)
	if err := os.Remove(filepath.Join(g.root, "etc/systemd/system/qa.service")); err != nil {
		t.Fatal(err)
	}
	if got := converge(t, r); !strings.Contains(attrsOf(got), "state=reversible") {
		t.Fatalf("deleted unit drift = %+v", got)
	}
	if g.count("systemctl", "daemon-reload") != 2 {
		t.Fatal("restoring the unit did not reload systemd")
	}
}

func TestQASysctlCombinedDrift(t *testing.T) {
	g := newFakeGuest(t)
	mkdirs(t, g.root, "etc/sysctl.d")
	proc := filepath.Join(g.root, "proc/sys/vm/swappiness")
	conf := filepath.Join(g.root, "etc/sysctl.d/60-plumb-vm.swappiness.conf")
	mustWrite(t, proc, "1\n")
	r := build(t, res("sysctl", "vm.swappiness", map[string]any{"value": 1}), g.env())
	converge(t, r)
	want, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, proc, "2\n")
	mustWrite(t, conf, "vm.swappiness = 2\n")
	if got := converge(t, r); attrsOf(got) != "file=reversible,value=disruptive" {
		t.Fatalf("combined drift = %+v", got)
	}
	for p, expected := range map[string]string{proc: "1\n", conf: string(want)} {
		got, err := os.ReadFile(p)
		if err != nil || string(got) != expected {
			t.Fatalf("%s = %q, %v", p, got, err)
		}
	}
	if _, err := r.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
}
