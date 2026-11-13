package resource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

func TestFile(t *testing.T) {
	g := newFakeGuest(t)
	path := filepath.Join(g.root, "etc/motd")

	r := build(t, res("file", "/etc/motd", map[string]any{"content": "hello\n", "mode": "640"}), g.env())
	got := converge(t, r)
	if wire.ActionOf(got) != wire.ActionCreate || attrsOf(got) != "state=reversible,content=reversible,mode=reversible,owner=reversible,group=reversible" {
		t.Fatalf("create changes = %s (%s)", attrsOf(got), wire.ActionOf(got))
	}
	if b, _ := os.ReadFile(path); string(b) != "hello\n" {
		t.Fatalf("content = %q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", fi.Mode())
	}

	// Drift by hand: content and mode. from = guest now, to = desired.
	if err := os.WriteFile(path, []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	got = converge(t, r)
	if attrsOf(got) != "content=reversible,mode=reversible" || got[0].From != "edited\n" || got[0].To != "hello\n" || got[1].From != "0600" || got[1].To != "0640" {
		t.Fatalf("drift changes = %+v", got)
	}

	// Only the mode differs: Apply must not rewrite the file.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	converge(t, r)
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) {
		t.Error("a mode change replaced the file")
	}

	// Delete.
	del := build(t, res("file", "/etc/motd", map[string]any{"state": "absent"}), g.env())
	got = converge(t, del)
	if wire.ActionOf(got) != wire.ActionDelete || got[0].Risk != wire.RiskDestructive {
		t.Fatalf("delete changes = %+v", got)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
}

func TestFileSafety(t *testing.T) {
	g := newFakeGuest(t)
	ctx := context.Background()

	// A symlink at the path is not followed.
	target := filepath.Join(g.root, "srv/secret")
	mustWrite(t, target, "do not leak\n")
	if err := os.Symlink(target, filepath.Join(g.root, "etc/link")); err != nil {
		t.Fatal(err)
	}
	r := build(t, res("file", "/etc/link", map[string]any{"content": "x"}), g.env())
	if _, err := r.Observe(ctx); err == nil || !strings.Contains(err.Error(), "path component /etc/link is a symlink") {
		t.Fatalf("Observe on a symlink: err = %v", err)
	}

	// No parent directory: a clear error, nothing created.
	r = build(t, res("file", "/opt/app/conf", map[string]any{"content": "x"}), g.env())
	if err := r.Apply(ctx, plan(t, r)); err == nil || !strings.Contains(err.Error(), "parent directory") {
		t.Fatalf("Apply without parent: err = %v", err)
	}

	// Unknown owner fails before anything is written.
	r = build(t, res("file", "/srv/x", map[string]any{"owner": "nobody"}), g.env())
	if err := r.Apply(ctx, plan(t, r)); err == nil || !strings.Contains(err.Error(), `user "nobody" does not exist`) {
		t.Fatalf("Apply with unknown owner: err = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(g.root, "srv/x")); !os.IsNotExist(err) {
		t.Fatal("file was written")
	}
}

func TestNewChecksAttributes(t *testing.T) {
	env := newFakeGuest(t).env()
	bad := []wire.Resource{
		{ID: "file:/etc/a", Type: "file", Attrs: map[string]any{"path": "/etc/b"}},              // id does not match
		{ID: "file:/etc/a", Type: "file", Attrs: map[string]any{"path": "/etc/a", "exec": "x"}}, // unknown attribute
		{ID: "file:/etc/a", Type: "file", Attrs: map[string]any{"path": "/etc/a", "mode": "9"}}, // bad mode
		{ID: "package:nginx", Type: "package", Attrs: map[string]any{"name": "nginx", "strictness": "exact"}},
		{ID: "command:x", Type: "command", Attrs: map[string]any{}},
	}
	for _, r := range bad {
		if _, err := New(r, env); err == nil {
			t.Errorf("New(%+v): want error", r)
		}
	}
	if _, err := New(res("file", "/etc/a", nil), Env{Root: "/"}); err == nil {
		t.Error("New with an incomplete Env: want error")
	}
}

func TestDirectory(t *testing.T) {
	g := newFakeGuest(t)
	path := filepath.Join(g.root, "srv/www")

	r := build(t, res("directory", "/srv/www", map[string]any{"mode": "2750"}), g.env())
	got := converge(t, r)
	if wire.ActionOf(got) != wire.ActionCreate {
		t.Fatalf("create changes = %+v", got)
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o750 || fi.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("directory = %v, %v", fi.Mode(), err)
	}

	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	if got = converge(t, r); attrsOf(got) != "mode=reversible" || got[0].From != "0777" || got[0].To != "2750" {
		t.Fatalf("drift changes = %+v", got)
	}

	// Only an empty directory is removed.
	mustWrite(t, filepath.Join(path, "index.html"), "x")
	del := build(t, res("directory", "/srv/www", map[string]any{"state": "absent"}), g.env())
	changes := plan(t, del)
	if wire.ActionOf(changes) != wire.ActionDelete || changes[0].Risk != wire.RiskDestructive {
		t.Fatalf("delete changes = %+v", changes)
	}
	if err := del.Apply(context.Background(), changes); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("delete non-empty: err = %v", err)
	}
	if err := os.Remove(filepath.Join(path, "index.html")); err != nil {
		t.Fatal(err)
	}
	converge(t, del)

	// A file at the path is an error, not drift.
	mustWrite(t, path, "x")
	if _, err := r.Observe(context.Background()); err == nil {
		t.Fatal("Observe on a file: want error")
	}
}

func TestPackage(t *testing.T) {
	g := newFakeGuest(t)

	present := build(t, res("package", "nginx", nil), g.env())
	got := converge(t, present)
	if wire.ActionOf(got) != wire.ActionCreate || attrsOf(got) != "state=reversible" {
		t.Fatalf("install changes = %s", attrsOf(got))
	}
	if g.packages["nginx"] != "1.26.0-2" {
		t.Fatalf("installed %q", g.packages["nginx"])
	}

	// Strictness present: another version is not drift.
	g.packages["nginx"] = "1.24.0-1"
	if c := plan(t, present); len(c) != 0 {
		t.Fatalf("present strictness reports %+v", c)
	}

	// Strictness exact: another version is a disruptive change.
	exact := build(t, res("package", "nginx", map[string]any{"strictness": "exact", "version": "1.26.0-1"}), g.env())
	got = converge(t, exact)
	if attrsOf(got) != "version=disruptive" || got[0].From != "1.24.0-1" || got[0].To != "1.26.0-1" {
		t.Fatalf("exact changes = %+v", got)
	}
	if g.countArg("apt-get", "nginx=1.26.0-1") != 1 {
		t.Fatalf("calls = %q", g.calls)
	}

	// Removal is destructive.
	gone := build(t, res("package", "nginx", map[string]any{"state": "absent"}), g.env())
	got = converge(t, gone)
	if wire.ActionOf(got) != wire.ActionDelete || got[0].Risk != wire.RiskDestructive {
		t.Fatalf("remove changes = %+v", got)
	}
}

func TestPackageUpdatesListsOnce(t *testing.T) {
	g := newFakeGuest(t)
	g.needUpdate = true
	converge(t, build(t, res("package", "tree", nil), g.env()))
	if n := g.count("apt-get", "-q", "-y"); n != 3 { // install, update, install
		t.Fatalf("apt-get calls = %d: %q", n, g.calls)
	}
}

func TestPackageLock(t *testing.T) {
	g := newFakeGuest(t)
	g.aptDelay = 30 * time.Millisecond
	env := g.env()
	a := build(t, res("package", "nginx", nil), env)
	b := build(t, res("package", "cron", nil), env)
	ca, cb := plan(t, a), plan(t, b)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, job := range []struct {
		r Resource
		c []wire.Change
	}{{a, ca}, {b, cb}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- job.r.Apply(context.Background(), job.c)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if g.aptMax != 1 {
		t.Fatalf("%d apt-get calls ran at the same time, want 1", g.aptMax)
	}
	if len(g.packages) != 2 {
		t.Fatalf("packages = %v", g.packages)
	}
}

func TestService(t *testing.T) {
	g := newFakeGuest(t)
	ctx := context.Background()

	// The unit does not exist yet: running means create; stopped may enable.
	r := build(t, res("service", "nginx", nil), g.env())
	if c := plan(t, r); wire.ActionOf(c) != wire.ActionCreate {
		t.Fatalf("missing unit changes = %+v", c)
	}
	if c := plan(t, build(t, res("service", "nginx", map[string]any{"state": "stopped", "enabled": false}), g.env())); len(c) != 0 {
		t.Fatalf("stopped and missing: %+v", c)
	}

	if c := plan(t, build(t, res("service", "nginx", map[string]any{"state": "stopped", "enabled": true}), g.env())); len(c) != 1 || c[0].Attr != "enabled" || c[0].From != nil || c[0].To != true || c[0].Risk != wire.RiskReversible {
		t.Fatalf("stopped, enabled and missing: %+v", c)
	}

	g.units["nginx.service"] = &unit{}
	got := converge(t, r)
	if attrsOf(got) != "state=reversible,enabled=reversible" || !g.units["nginx.service"].active || !g.units["nginx.service"].enabled {
		t.Fatalf("start changes = %s, unit = %+v", attrsOf(got), g.units["nginx.service"])
	}

	stop := build(t, res("service", "nginx", map[string]any{"state": "stopped", "enabled": false}), g.env())
	got = converge(t, stop)
	if attrsOf(got) != "state=disruptive,enabled=reversible" {
		t.Fatalf("stop changes = %s", attrsOf(got))
	}

	// A static unit cannot be enabled: no enabled change.
	g.units["static.service"] = &unit{static: true}
	if c := plan(t, build(t, res("service", "static", map[string]any{"state": "stopped"}), g.env())); len(c) != 0 {
		t.Fatalf("static unit: %+v", c)
	}

	// Units with a type suffix keep it.
	g.units["fstrim.timer"] = &unit{}
	converge(t, build(t, res("service", "fstrim.timer", nil), g.env()))
	if !g.units["fstrim.timer"].active {
		t.Fatal("timer not started")
	}
	if err := r.Apply(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func TestServiceRestart(t *testing.T) {
	svc := res("service", "nginx", map[string]any{"restart_on": []any{"file:/etc/nginx/a.conf", "file:/etc/nginx/b.conf"}})

	// Two listed resources changed: one restart change.
	c, ok := RestartChange(svc, []string{"file:/etc/nginx/a.conf", "file:/etc/nginx/b.conf", "package:tree"})
	if !ok || c.Attr != wire.AttrRestart || c.Risk != wire.RiskDisruptive || wire.ActionOf([]wire.Change{c}) != wire.ActionRestart {
		t.Fatalf("RestartChange = %+v, %v", c, ok)
	}
	if _, ok := RestartChange(svc, []string{"package:tree"}); ok {
		t.Fatal("restart for an unlisted resource")
	}
	if _, ok := RestartChange(res("file", "/etc/x", nil), []string{"file:/etc/x"}); ok {
		t.Fatal("restart for a non-service")
	}

	tests := []struct {
		name     string
		active   bool
		attrs    map[string]any
		restarts int
		starts   int
	}{
		{"running unit restarts once for duplicate changes", true, nil, 1, 0},
		{"stopped unit is started, not restarted", false, nil, 0, 1},
		{"unit that must stay stopped is not restarted", false, map[string]any{"state": "stopped"}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newFakeGuest(t)
			g.units["nginx.service"] = &unit{active: tt.active, enabled: true}
			attrs := map[string]any{"restart_on": []any{"file:/etc/nginx/a.conf", "file:/etc/nginx/b.conf"}}
			for k, v := range tt.attrs {
				attrs[k] = v
			}
			r := build(t, res("service", "nginx", attrs), g.env())
			changes := append(plan(t, r), c, c)
			if err := r.Apply(context.Background(), changes); err != nil {
				t.Fatal(err)
			}
			if n := g.count("systemctl", "restart"); n != tt.restarts {
				t.Errorf("restarts = %d, want %d", n, tt.restarts)
			}
			if n := g.count("systemctl", "start"); n != tt.starts {
				t.Errorf("starts = %d, want %d", n, tt.starts)
			}
		})
	}
}

const (
	keyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA a@host"
	keyB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB b@host"
)

func TestUser(t *testing.T) {
	g := newFakeGuest(t)
	keys := filepath.Join(g.root, "home/deploy/.ssh/authorized_keys")

	r := build(t, res("user", "deploy", map[string]any{"groups": []any{"sudo", "adm"}, "ssh_keys": []any{keyA}}), g.env())
	got := converge(t, r)
	if wire.ActionOf(got) != wire.ActionCreate || attrsOf(got) != "state=reversible,shell=reversible,home=reversible,groups=reversible,ssh_keys=reversible" {
		t.Fatalf("create changes = %s", attrsOf(got))
	}
	if b, _ := os.ReadFile(keys); string(b) != keyA+"\n" {
		t.Fatalf("authorized_keys = %q", b)
	}
	if fi, _ := os.Stat(keys); fi.Mode().Perm() != 0o600 {
		t.Fatalf("authorized_keys mode = %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Dir(keys)); fi.Mode().Perm() != 0o700 {
		t.Fatalf(".ssh mode = %v", fi.Mode())
	}

	// Drift: an extra key, a changed shell, one group less.
	mustWrite(t, keys, keyA+"\n# comment\n"+keyB+"\n")
	if _, err := g.Run(context.Background(), "usermod", "--shell", "/bin/sh", "--groups", "sudo", "--", "deploy"); err != nil {
		t.Fatal(err)
	}
	got = converge(t, r)
	// Shell change and key removal can lock out: disruptive. Adding a group: reversible.
	if attrsOf(got) != "shell=disruptive,groups=reversible,ssh_keys=disruptive" || got[0].From != "/bin/sh" || got[0].To != "/bin/bash" {
		t.Fatalf("drift changes = %+v", got)
	}
	if b, _ := os.ReadFile(keys); string(b) != keyA+"\n" {
		t.Fatalf("authorized_keys = %q", b)
	}

	// No keys wanted: the file goes away.
	noKeys := build(t, res("user", "deploy", map[string]any{"groups": []any{"adm", "sudo"}}), g.env())
	converge(t, noKeys)
	if _, err := os.Stat(keys); !os.IsNotExist(err) {
		t.Fatalf("authorized_keys still there: %v", err)
	}

	del := build(t, res("user", "deploy", map[string]any{"state": "absent"}), g.env())
	if got = converge(t, del); wire.ActionOf(got) != wire.ActionDelete || got[0].Risk != wire.RiskDestructive {
		t.Fatalf("delete changes = %+v", got)
	}
}

func TestUserKeysSymlinkEscape(t *testing.T) {
	g := newFakeGuest(t)
	r := build(t, res("user", "eve", map[string]any{"ssh_keys": []any{keyA}}), g.env())
	converge(t, r)

	// The user replaces ~/.ssh with a link to a directory outside the home.
	outside := filepath.Join(g.root, "srv/root-ssh")
	mustWrite(t, filepath.Join(outside, "authorized_keys"), "root secret\n")
	ssh := filepath.Join(g.root, "home/eve/.ssh")
	if err := os.RemoveAll(ssh); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, ssh); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Observe(context.Background()); err == nil {
		t.Fatal("Observe followed a symlink out of the home")
	}
	if err := r.Apply(context.Background(), []wire.Change{{Attr: "ssh_keys", Risk: wire.RiskReversible}}); err == nil {
		t.Fatal("Apply followed a symlink out of the home")
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "authorized_keys")); string(b) != "root secret\n" {
		t.Fatalf("file outside the home changed: %q", b)
	}
}

func TestWatch(t *testing.T) {
	env := newFakeGuest(t).env()
	tests := []struct {
		r    wire.Resource
		want string
	}{
		{res("file", "/etc/motd", nil), "/etc/motd"},
		{res("directory", "/srv", nil), "/srv"},
		{res("package", "nginx", nil), "/var/lib/dpkg/status"},
		{res("service", "nginx", nil), "/etc/systemd/system/nginx.service"},
		{res("user", "deploy", nil), "/etc/passwd"},
	}
	for _, tt := range tests {
		paths := build(t, tt.r, env).Watch()
		if len(paths) == 0 || paths[0] != filepath.Join(env.Root, tt.want) {
			t.Errorf("%s: Watch() = %v, want first %s under the root", tt.r.ID, paths, tt.want)
		}
	}
}

func TestUserAccountLock(t *testing.T) {
	g := newFakeGuest(t)
	g.aptDelay = 30 * time.Millisecond
	env := g.env()
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for _, name := range []string{"alice", "bob", "carol"} {
		r := build(t, res("user", name, nil), env)
		changes := plan(t, r)
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- r.Apply(context.Background(), changes)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if g.shadowMax != 1 {
		t.Fatalf("%d account-tool calls ran at the same time, want 1", g.shadowMax)
	}
}

func TestUserGuards(t *testing.T) {
	g := newFakeGuest(t)
	if _, err := New(res("user", "deploy", map[string]any{"ssh_keys": []any{keyA + "\n" + keyB}}), g.env()); err == nil {
		t.Fatal("key with a line break: want error")
	}
	// An account with uid 0 under another name.
	passwd := filepath.Join(g.root, "etc/passwd")
	data, _ := os.ReadFile(passwd)
	mustWrite(t, passwd, string(data)+"toor:x:0:0::/home/toor:/bin/bash\n")
	if err := os.MkdirAll(filepath.Join(g.root, "home/toor"), 0o755); err != nil {
		t.Fatal(err)
	}
	keys := build(t, res("user", "toor", map[string]any{"ssh_keys": []any{keyA}}), g.env())
	if err := keys.Apply(context.Background(), plan(t, keys)); err == nil || !strings.Contains(err.Error(), "uid 0") {
		t.Fatalf("keys of uid 0: err = %v", err)
	}
	del := build(t, res("user", "toor", map[string]any{"state": "absent"}), g.env())
	if err := del.Apply(context.Background(), plan(t, del)); err == nil || !strings.Contains(err.Error(), "uid 0") {
		t.Fatalf("delete uid 0: err = %v", err)
	}
	if g.count("userdel") != 0 {
		t.Fatal("userdel ran")
	}
}

func TestPackageNameGuard(t *testing.T) {
	env := newFakeGuest(t).env()
	for _, name := range []string{"nginx-", "-nginx"} {
		r := wire.Resource{ID: "package:" + name, Type: "package", Attrs: map[string]any{"name": name}}
		if _, err := New(r, env); err == nil {
			t.Errorf("package %q: want error", name)
		}
	}
	build(t, res("package", "g++", nil), env)
}

func TestServiceMasked(t *testing.T) {
	g := newFakeGuest(t)
	g.units["nginx.service"] = &unit{masked: true}
	r := build(t, res("service", "nginx", nil), g.env())
	changes := plan(t, r)
	if !has(changes, "enabled") {
		t.Fatalf("masked unit: changes = %+v", changes)
	}
	if err := r.Apply(context.Background(), changes); err == nil || !strings.Contains(err.Error(), "masked") {
		t.Fatalf("Apply on a masked unit: err = %v", err)
	}
	if g.count("systemctl", "enable") != 0 || g.count("systemctl", "start") != 0 {
		t.Fatal("systemctl changed a masked unit")
	}
	// A masked unit that must stay stopped and disabled is fine.
	if c := plan(t, build(t, res("service", "nginx", map[string]any{"state": "stopped", "enabled": false}), g.env())); len(c) != 0 {
		t.Fatalf("stopped and disabled: %+v", c)
	}
}
