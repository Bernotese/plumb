package resource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// armor wraps base64 text as a public key block.
func armor(b64 string) string {
	return "-----BEGIN PGP PUBLIC KEY BLOCK-----\nComment: test\n\n" + b64 + "\n=abcd\n-----END PGP PUBLIC KEY BLOCK-----\n"
}

// testKey is a public key packet header (new format, tag 6): the agent
// checks the armor and the packet tag, apt checks the rest.
var testKey = armor("xgEE")

func mkdirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func aptAttrs(extra map[string]any) map[string]any {
	a := map[string]any{
		"uris":       []any{"https://download.docker.com/linux/debian"},
		"suites":     []any{"trixie"},
		"components": []any{"stable"},
		"key":        testKey,
	}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func TestAptRepository(t *testing.T) {
	g := newFakeGuest(t)
	mkdirs(t, g.root, "etc/apt/sources.list.d", "etc/apt/trusted.gpg.d")
	src := filepath.Join(g.root, "etc/apt/sources.list.d/plumb-docker.sources")
	key := filepath.Join(g.root, "etc/apt/keyrings/plumb-docker.asc")

	r := build(t, res("apt_repository", "docker", aptAttrs(map[string]any{"architectures": []any{"amd64"}})), g.env())
	got := converge(t, r)
	if wire.ActionOf(got) != wire.ActionCreate || attrsOf(got) != "state=reversible,sources=reversible,key=reversible,perms=reversible" {
		t.Fatalf("create = %s", attrsOf(got))
	}
	want := "# Managed by Plumb (resource apt_repository:docker). Local changes are drift.\n" +
		"Types: deb\nURIs: https://download.docker.com/linux/debian\nSuites: trixie\nComponents: stable\nArchitectures: amd64\n" +
		"Signed-By: /etc/apt/keyrings/plumb-docker.asc\n"
	if b, _ := os.ReadFile(src); string(b) != want {
		t.Fatalf("sources =\n%s", b)
	}
	if b, _ := os.ReadFile(key); string(b) != testKey {
		t.Fatalf("key = %q", b)
	}
	if fi, _ := os.Stat(filepath.Dir(key)); !fi.IsDir() || fi.Mode().Perm() != 0o755 {
		t.Fatalf("keyrings dir = %v", fi.Mode())
	}
	if entries, _ := os.ReadDir(filepath.Join(g.root, "etc/apt/trusted.gpg.d")); len(entries) != 0 {
		t.Fatalf("trusted.gpg.d was written: %v", entries)
	}
	if n := g.countArg("apt-get", "update"); n != 1 {
		t.Fatalf("apt-get update calls = %d", n)
	}

	// Drift: a hand edit adds Trusted, a guest user makes the key writable.
	mustWrite(t, src, want+"Trusted: yes\n")
	if err := os.Chmod(key, 0o666); err != nil {
		t.Fatal(err)
	}
	got = converge(t, r)
	if attrsOf(got) != "sources=reversible,perms=reversible" || !strings.Contains(got[1].From.(string), "0666") {
		t.Fatalf("drift = %+v", got)
	}
	if fi, _ := os.Stat(key); fi.Mode().Perm() != 0o644 {
		t.Fatalf("key mode = %v", fi.Mode())
	}

	// Only the key file missing: present, the key is restored.
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if got = converge(t, r); attrsOf(got) != "key=reversible,perms=reversible" {
		t.Fatalf("missing key = %s", attrsOf(got))
	}

	// A flat repository: an exact path, no components.
	flat := build(t, res("apt_repository", "local", aptAttrs(map[string]any{"suites": []any{"repo/"}, "components": []any{}})), g.env())
	converge(t, flat)
	if b, _ := os.ReadFile(filepath.Join(g.root, "etc/apt/sources.list.d/plumb-local.sources")); strings.Contains(string(b), "Components") || !strings.Contains(string(b), "Suites: repo/\n") {
		t.Fatalf("flat sources =\n%s", b)
	}

	// Delete removes both files; a missing repository is no change.
	del := build(t, res("apt_repository", "docker", map[string]any{"state": "absent"}), g.env())
	got = converge(t, del)
	if wire.ActionOf(got) != wire.ActionDelete || got[0].Risk != wire.RiskReversible {
		t.Fatalf("delete = %+v", got)
	}
	for _, p := range []string{src, key} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still there", p)
		}
	}
}

func TestAptRepositoryRefuses(t *testing.T) {
	g := newFakeGuest(t)
	bad := map[string]map[string]any{
		"secret key":         {"key": armor("xQEE")},
		"old format secret":  {"key": armor("lQEE")},
		"two blocks":         {"key": testKey + testKey},
		"text around":        {"key": "see https://x\n" + testKey},
		"url as key":         {"key": "https://download.docker.com/linux/debian/gpg"},
		"bad base64":         {"key": armor("@@@@")},
		"no key":             {"key": ""},
		"file uri":           {"uris": []any{"file:///srv/repo"}},
		"uri with space":     {"uris": []any{"https://x/ y"}},
		"suite option":       {"suites": []any{"trixie\nTrusted: yes"}},
		"component line":     {"components": []any{"main\nTrusted: yes"}},
		"no components":      {"components": []any{}},
		"flat and component": {"suites": []any{"repo/"}},
		"mixed flat":         {"suites": []any{"repo/", "trixie"}, "components": []any{}},
		"arch":               {"architectures": []any{"amd64 i386"}},
		"unknown attribute":  {"trusted": true},
	}
	for name, extra := range bad {
		if _, err := New(res("apt_repository", "docker", aptAttrs(extra)), g.env()); err == nil {
			t.Errorf("%s: New accepted it", name)
		}
	}
	if _, err := New(res("apt_repository", "../x", map[string]any{"state": "absent"}), g.env()); err == nil {
		t.Error("a name with a path was accepted")
	}
	// A real key starts with the old packet format (0x99, "mQ").
	if err := checkPublicKey(armor("mQINBFit2ioBEADh")); err != nil {
		t.Errorf("old format public key: %v", err)
	}
}

func TestAptRepositorySymlinkedDir(t *testing.T) {
	g := newFakeGuest(t)
	mkdirs(t, g.root, "etc/apt/sources.list.d", "srv/keys")
	if err := os.Symlink(filepath.Join(g.root, "srv/keys"), filepath.Join(g.root, "etc/apt/keyrings")); err != nil {
		t.Fatal(err)
	}
	r := build(t, res("apt_repository", "docker", aptAttrs(nil)), g.env())
	if _, err := r.Observe(context.Background()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Observe through a symlinked keyrings dir: %v", err)
	}
	changes := []wire.Change{change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskReversible)}
	if err := r.Apply(context.Background(), changes); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Apply through a symlinked keyrings dir: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(g.root, "srv/keys")); len(entries) != 0 {
		t.Fatalf("wrote through the link: %v", entries)
	}
}

func TestUnit(t *testing.T) {
	g := newFakeGuest(t)
	mkdirs(t, g.root, "etc/systemd/system")
	path := filepath.Join(g.root, "etc/systemd/system/backup.timer")
	content := "[Timer]\nOnCalendar=daily\n\n[Install]\nWantedBy=timers.target\n"

	r := build(t, res("unit", "backup.timer", map[string]any{"content": content}), g.env())
	got := converge(t, r)
	if wire.ActionOf(got) != wire.ActionCreate || attrsOf(got) != "state=reversible,content=reversible,mode=reversible,owner=reversible,group=reversible" {
		t.Fatalf("create = %s", attrsOf(got))
	}
	if b, _ := os.ReadFile(path); string(b) != content {
		t.Fatalf("content = %q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v", fi.Mode())
	}
	if n := g.count("systemctl", "daemon-reload"); n != 1 {
		t.Fatalf("daemon-reload calls = %d", n)
	}

	// Drift: a hand edit and a writable unit file.
	mustWrite(t, path, "[Timer]\nOnCalendar=hourly\n")
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	got = converge(t, r)
	if attrsOf(got) != "content=reversible,mode=reversible" || got[0].To != content {
		t.Fatalf("drift = %+v", got)
	}
	if n := g.count("systemctl", "daemon-reload"); n != 2 {
		t.Fatalf("daemon-reload calls = %d", n)
	}

	del := build(t, res("unit", "backup.timer", map[string]any{"state": "absent"}), g.env())
	got = converge(t, del)
	if wire.ActionOf(got) != wire.ActionDelete || got[0].Risk != wire.RiskDestructive {
		t.Fatalf("delete = %+v", got)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("unit file still there")
	}
	if n := g.count("systemctl", "daemon-reload"); n != 3 {
		t.Fatalf("daemon-reload calls = %d", n)
	}
}

func TestUnitRefuses(t *testing.T) {
	g := newFakeGuest(t)
	for _, name := range []string{"plumb-agent.service", "plumb-agent.timer", "plumb-agent-install.service", "app", "a/b.service", "-x.service", ".x.service", "../x.service", "x.conf"} {
		if _, err := New(res("unit", name, map[string]any{"content": "[Unit]\n"}), g.env()); err == nil {
			t.Errorf("unit %q accepted", name)
		}
	}
	if _, err := New(res("unit", "app.service", map[string]any{"content": " \n"}), g.env()); err == nil {
		t.Error("blank content accepted")
	}
	if _, err := New(res("unit", "app.service", map[string]any{"content": strings.Repeat("x", 64<<10+1)}), g.env()); err == nil {
		t.Error("content over 64 KiB accepted")
	}

	// A symlink at the unit path is not followed.
	mkdirs(t, g.root, "etc/systemd/system")
	mustWrite(t, filepath.Join(g.root, "srv/target"), "keep\n")
	if err := os.Symlink(filepath.Join(g.root, "srv/target"), filepath.Join(g.root, "etc/systemd/system/app.service")); err != nil {
		t.Fatal(err)
	}
	r := build(t, res("unit", "app.service", map[string]any{"content": "[Unit]\n"}), g.env())
	if _, err := r.Observe(context.Background()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Observe on a symlink: %v", err)
	}
}

func TestSysctl(t *testing.T) {
	g := newFakeGuest(t)
	mkdirs(t, g.root, "etc/sysctl.d")
	proc := filepath.Join(g.root, "proc/sys/net/ipv4/ip_forward")
	mustWrite(t, proc, "0\n")
	conf := filepath.Join(g.root, "etc/sysctl.d/60-plumb-net.ipv4.ip_forward.conf")

	// YAML "value: 1" arrives as a JSON number.
	r := build(t, res("sysctl", "net.ipv4.ip_forward", map[string]any{"value": float64(1)}), g.env())
	got := converge(t, r)
	if wire.ActionOf(got) != wire.ActionCreate || attrsOf(got) != "state=reversible,value=disruptive" || got[1].From != "0" || got[1].To != "1" {
		t.Fatalf("create = %+v", got)
	}
	if b, _ := os.ReadFile(proc); string(b) != "1\n" {
		t.Fatalf("running value = %q", b)
	}
	if b, _ := os.ReadFile(conf); !strings.HasSuffix(string(b), "\nnet.ipv4.ip_forward = 1\n") {
		t.Fatalf("conf = %q", b)
	}

	// Drift of the running value only.
	mustWrite(t, proc, "0\n")
	if got = converge(t, r); attrsOf(got) != "value=disruptive" {
		t.Fatalf("runtime drift = %s", attrsOf(got))
	}
	// Drift of the file only.
	mustWrite(t, conf, "net.ipv4.ip_forward = 0\n")
	if got = converge(t, r); attrsOf(got) != "file=reversible" {
		t.Fatalf("file drift = %s", attrsOf(got))
	}

	// Multi-value keys: the kernel prints tabs.
	pr := filepath.Join(g.root, "proc/sys/net/ipv4/ip_local_port_range")
	mustWrite(t, pr, "32768\t60999\n")
	same := build(t, res("sysctl", "net.ipv4.ip_local_port_range", map[string]any{"value": "32768  60999"}), g.env())
	if got = converge(t, same); attrsOf(got) != "state=reversible" {
		t.Fatalf("tabs vs spaces = %s", attrsOf(got))
	}

	// Absent removes the file and keeps the running value.
	del := build(t, res("sysctl", "net.ipv4.ip_forward", map[string]any{"state": "absent"}), g.env())
	got = converge(t, del)
	if wire.ActionOf(got) != wire.ActionDelete || got[0].Risk != wire.RiskReversible {
		t.Fatalf("delete = %+v", got)
	}
	if _, err := os.Lstat(conf); !os.IsNotExist(err) {
		t.Fatal("conf still there")
	}
	if b, _ := os.ReadFile(proc); string(b) != "1\n" {
		t.Fatalf("running value changed on delete: %q", b)
	}

	// A key the kernel does not have is an error, not drift.
	unknown := build(t, res("sysctl", "net.ipv4.nope", map[string]any{"value": "1"}), g.env())
	if _, err := unknown.Observe(context.Background()); err == nil || !strings.Contains(err.Error(), "no parameter") {
		t.Fatalf("unknown key: %v", err)
	}
}

func TestSysctlRefuses(t *testing.T) {
	g := newFakeGuest(t)
	for name, attrs := range map[string]map[string]any{
		"kernel":              {"value": "1"},
		"net/ipv4/x":          {"value": "1"},
		"net..x":              {"value": "1"},
		"net.ipv4.x":          {"value": "1\n2"},
		"net.ipv4.y":          {"value": " "},
		"net.ipv4.z":          {"value": 1.5},
		"net.ipv4.w":          {"value": true},
		"net.ipv4.no_value":   {},
		"vm.swappiness.extra": {"value": "1", "persist": true},
	} {
		if _, err := New(res("sysctl", name, attrs), g.env()); err == nil {
			t.Errorf("sysctl %s %v accepted", name, attrs)
		}
	}
	typ, _ := Lookup("sysctl")
	if typ.Supports(wire.KindLXC) || !typ.Supports(wire.KindVM) {
		t.Errorf("sysctl kinds = %v, want vm only", typ.Kinds)
	}
}
