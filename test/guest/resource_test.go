//go:build guest

// Resource tests on a real Debian or Ubuntu guest with systemd (T-005).
// They run as root inside the containers of test/guest/run.sh.
package guest_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

var env = resource.NewEnv("/", resource.ExecRunner{})

func rsrc(typ, key string, attrs map[string]any, sensitive ...string) wire.Resource {
	t, _ := resource.Lookup(typ)
	a := map[string]any{t.Key: key}
	for k, v := range attrs {
		a[k] = v
	}
	return wire.Resource{ID: wire.ID(typ, key), Type: typ, Block: "guest-test", Attrs: a, Sensitive: sensitive}
}

func build(t *testing.T, r wire.Resource) resource.Resource {
	t.Helper()
	rs, err := resource.New(r, env)
	if err != nil {
		t.Fatalf("New(%s): %v", r.ID, err)
	}
	return rs
}

func plan(t *testing.T, rs resource.Resource) []wire.Change {
	t.Helper()
	obs, err := rs.Observe(context.Background())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	changes := rs.Diff(obs)
	for _, c := range changes {
		if !c.Risk.Valid() {
			t.Fatalf("change without risk: %+v", c)
		}
	}
	return changes
}

// converge applies the plan; a second round must report no change.
func converge(t *testing.T, r wire.Resource) []wire.Change {
	t.Helper()
	rs := build(t, r)
	changes := plan(t, rs)
	if err := rs.Apply(context.Background(), changes); err != nil {
		t.Fatalf("Apply %s %+v: %v", r.ID, changes, err)
	}
	if again := plan(t, rs); len(again) != 0 {
		t.Fatalf("%s: second round has changes: %+v", r.ID, again)
	}
	return changes
}

func sh(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPackages(t *testing.T) {
	// Two installs at the same time share the package lock.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, name := range []string{"tree", "less"} {
		rs := build(t, rsrc("package", name, nil))
		changes := plan(t, rs)
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- rs.Apply(context.Background(), changes)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	converge(t, rsrc("package", "tree", nil))
	converge(t, rsrc("package", "less", nil))
	version := sh(t, "dpkg-query", "-W", "-f=${Version}", "--", "tree")

	if c := converge(t, rsrc("package", "tree", map[string]any{"strictness": "exact", "version": version})); len(c) != 0 {
		t.Fatalf("exact with the installed version: %+v", c)
	}
	c := plan(t, build(t, rsrc("package", "tree", map[string]any{"strictness": "exact", "version": version + "x"})))
	if len(c) != 1 || c[0].Attr != "version" || c[0].Risk != wire.RiskDisruptive {
		t.Fatalf("exact with another version: %+v", c)
	}

	c = converge(t, rsrc("package", "tree", map[string]any{"state": "absent"}))
	if wire.ActionOf(c) != wire.ActionDelete {
		t.Fatalf("remove: %+v", c)
	}
}

func TestService(t *testing.T) {
	converge(t, rsrc("package", "cron", nil))
	converge(t, rsrc("service", "cron", nil))
	if sh(t, "systemctl", "is-active", "--", "cron.service") != "active" {
		t.Fatal("cron not running")
	}

	c := converge(t, rsrc("service", "cron", map[string]any{"state": "stopped", "enabled": false}))
	if len(c) != 2 {
		t.Fatalf("stop: %+v", c)
	}
	if out, _ := exec.Command("systemctl", "is-enabled", "--", "cron.service").Output(); strings.TrimSpace(string(out)) != "disabled" {
		t.Fatalf("is-enabled = %q", out)
	}
	converge(t, rsrc("service", "cron", nil))

	const dropin = "/run/systemd/system/cron.service.d"
	if err := os.MkdirAll(dropin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dropin)
		os.Remove("/run/plumb-starts")
		sh(t, "systemctl", "daemon-reload")
	})
	if err := os.WriteFile(dropin+"/count.conf", []byte("[Service]\nExecStartPost=/bin/sh -c 'echo x >> /run/plumb-starts'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, "systemctl", "daemon-reload")
	if err := os.WriteFile("/run/plumb-starts", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// Two changed restart_on resources restart the service once.
	svc := rsrc("service", "cron", map[string]any{"restart_on": []any{"file:/etc/a", "file:/etc/b"}})
	rc, ok := resource.RestartChange(svc, []string{"file:/etc/a", "file:/etc/b"})
	if !ok {
		t.Fatal("no restart change")
	}
	before := sh(t, "systemctl", "show", "--property=InvocationID", "--value", "--", "cron.service")
	if err := build(t, svc).Apply(context.Background(), []wire.Change{rc, rc}); err != nil {
		t.Fatal(err)
	}
	after := sh(t, "systemctl", "show", "--property=InvocationID", "--value", "--", "cron.service")
	if before == after {
		t.Fatal("service did not restart")
	}
	if starts, err := os.ReadFile("/run/plumb-starts"); err != nil || string(starts) != "x\n" {
		t.Fatalf("want exactly one start, got %q: %v", starts, err)
	}
}

func TestFilesAndUser(t *testing.T) {
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOmPlumbGuestTestKeyPlumbGuestTestKey01 test@plumb"
	converge(t, rsrc("user", "plumbtest", map[string]any{"groups": []any{"adm"}, "ssh_keys": []any{key}}))
	if g := sh(t, "id", "-nG", "--", "plumbtest"); !strings.Contains(g, "adm") {
		t.Fatalf("groups = %q", g)
	}
	if k := sh(t, "cat", "--", "/home/plumbtest/.ssh/authorized_keys"); k != key {
		t.Fatalf("authorized_keys = %q", k)
	}
	if o := sh(t, "stat", "-c", "%U %a", "--", "/home/plumbtest/.ssh/authorized_keys"); o != "plumbtest 600" {
		t.Fatalf("authorized_keys stat = %q", o)
	}

	changes := converge(t, rsrc("user", "plumbtest", map[string]any{"shell": "/bin/sh", "groups": []any{}}))
	if len(changes) == 0 || wire.MaxRisk(changes) != wire.RiskDisruptive {
		t.Fatalf("user update must be disruptive: %+v", changes)
	}
	if passwd := sh(t, "getent", "passwd", "plumbtest"); !strings.HasSuffix(passwd, ":/bin/sh") {
		t.Fatalf("passwd = %q", passwd)
	}
	for _, group := range strings.Fields(sh(t, "id", "-nG", "--", "plumbtest")) {
		if group == "adm" {
			t.Fatal("user still belongs to adm")
		}
	}

	converge(t, rsrc("directory", "/srv/plumb", map[string]any{"owner": "plumbtest", "group": "adm", "mode": "2750"}))
	if o := sh(t, "stat", "-c", "%U %G %a", "--", "/srv/plumb"); o != "plumbtest adm 2750" {
		t.Fatalf("directory stat = %q", o)
	}
	converge(t, rsrc("file", "/srv/plumb/motd", map[string]any{"content": "hi\n", "owner": "plumbtest", "mode": "0640"}))
	if o := sh(t, "stat", "-c", "%U %G %a", "--", "/srv/plumb/motd"); o != "plumbtest root 640" {
		t.Fatalf("file stat = %q", o)
	}

	// Drift by hand, then revert.
	if err := os.WriteFile("/srv/plumb/motd", []byte("edited\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if c := converge(t, rsrc("file", "/srv/plumb/motd", map[string]any{"content": "hi\n", "owner": "plumbtest", "mode": "0640"})); len(c) != 1 || c[0].From != "edited\n" {
		t.Fatalf("drift: %+v", c)
	}

	// A sensitive value never leaves the guest.
	const secret = "token=plumb-guest-secret-0123456789"
	r := rsrc("file", "/srv/plumb/app.conf", map[string]any{"content": secret, "mode": "0600"}, "content")
	c := converge(t, r)
	b, _ := json.Marshal(wire.Item{Resource: r.ID, Status: wire.StatusApplied, Changes: c, Output: resource.SafeOutput(r, "wrote "+secret)})
	if strings.Contains(string(b), secret) {
		t.Fatalf("report holds the secret: %s", b)
	}

	converge(t, rsrc("file", "/srv/plumb/app.conf", map[string]any{"state": "absent"}))
	converge(t, rsrc("file", "/srv/plumb/motd", map[string]any{"state": "absent"}))
	converge(t, rsrc("directory", "/srv/plumb", map[string]any{"state": "absent"}))
	converge(t, rsrc("user", "plumbtest", map[string]any{"state": "absent"}))
	if _, err := exec.Command("id", "--", "plumbtest").Output(); err == nil {
		t.Fatal("user still exists")
	}
}

// As real root: a symlinked parent that a user owns must not lead the
// agent to a root-owned file outside it (D-054).
func TestSymlinkedParentAsRoot(t *testing.T) {
	converge(t, rsrc("user", "plumbevil", nil))
	if err := os.WriteFile("/root/plumb-secret", []byte("TOP SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove("/root/plumb-secret")
	if err := os.Symlink("/root", "/home/plumbevil/app"); err != nil {
		t.Fatal(err)
	}
	r := build(t, rsrc("file", "/home/plumbevil/app/plumb-secret", map[string]any{"content": "x", "owner": "plumbevil"}))
	if _, err := r.Observe(context.Background()); err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("Observe: %v", err)
	}
	if err := r.Apply(context.Background(), []wire.Change{{Attr: "content", Risk: wire.RiskReversible}}); err == nil {
		t.Fatal("Apply followed the symlink")
	}
	if o := sh(t, "stat", "-c", "%U %a", "--", "/root/plumb-secret"); o != "root 600" {
		t.Fatalf("outside file changed: %s", o)
	}
	if b, _ := os.ReadFile("/root/plumb-secret"); string(b) != "TOP SECRET\n" {
		t.Fatalf("outside file content changed: %q", b)
	}
	converge(t, rsrc("user", "plumbevil", map[string]any{"state": "absent"}))
}
