package resource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
	"golang.org/x/sys/unix"
)

func readLink(t *testing.T, p string) string {
	t.Helper()
	got, err := os.Readlink(p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSymlinkResource(t *testing.T) {
	ctx := context.Background()
	g := newFakeGuest(t)
	link := filepath.Join(g.root, "etc/nginx/sites-enabled/app")
	mustWrite(t, filepath.Join(g.root, "etc/nginx/sites-available/app"), "server {}\n")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	want := res("symlink", "/etc/nginx/sites-enabled/app", map[string]any{"target": "../sites-available/app"})

	// Create, then the second round has no change.
	c := converge(t, build(t, want, g.env()))
	if attrsOf(c) != "state=reversible,target=reversible" || wire.ActionOf(c) != wire.ActionCreate {
		t.Fatalf("create: %+v", c)
	}
	if got := readLink(t, link); got != "../sites-available/app" {
		t.Fatalf("link = %q", got)
	}

	// Drift 1: the link points elsewhere.
	os.Remove(link)
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	c = converge(t, build(t, want, g.env()))
	if len(c) != 1 || c[0].Attr != "target" || c[0].From != "/etc/passwd" || c[0].Risk != wire.RiskReversible {
		t.Fatalf("other target: %+v", c)
	}

	// Drift 2: the link is missing.
	os.Remove(link)
	if c = converge(t, build(t, want, g.env())); wire.ActionOf(c) != wire.ActionCreate {
		t.Fatalf("missing: %+v", c)
	}

	// Drift 3: a regular file replaced the link. The plan replaces it only
	// with a destructive change; a stale plan is refused.
	os.Remove(link)
	mustWrite(t, link, "local edit\n")
	stale := build(t, want, g.env())
	if err := stale.Apply(ctx, []wire.Change{{Attr: "target", Risk: wire.RiskReversible}}); err == nil || !strings.Contains(err.Error(), "plan again") {
		t.Fatalf("stale plan: %v", err)
	}
	if b, _ := os.ReadFile(link); string(b) != "local edit\n" {
		t.Fatalf("stale plan changed the file: %q", b)
	}
	c = converge(t, build(t, want, g.env()))
	if attrsOf(c) != "kind=destructive,target=destructive" || c[0].From != "file" || wire.ActionOf(c) != wire.ActionUpdate {
		t.Fatalf("file: %+v", c)
	}
	if got := readLink(t, link); got != "../sites-available/app" {
		t.Fatalf("link = %q", got)
	}

	// A directory on the path is never replaced or removed.
	dirLink := filepath.Join(g.root, "etc/nginx/sites-enabled/dir")
	if err := os.Mkdir(dirLink, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dirLink, "keep"), "x")
	rs := build(t, res("symlink", "/etc/nginx/sites-enabled/dir", map[string]any{"target": "x"}), g.env())
	obs, err := rs.Observe(ctx)
	if err != nil || obs[attrKind] != "directory" {
		t.Fatalf("observe %v %v", obs, err)
	}
	if err := rs.Apply(ctx, rs.Diff(obs)); err == nil {
		t.Fatal("Apply replaced a directory")
	}
	// absent over a directory: the link is absent, so no change and no Issue.
	if c := converge(t, build(t, res("symlink", "/etc/nginx/sites-enabled/dir", map[string]any{"state": "absent"}), g.env())); len(c) != 0 {
		t.Fatalf("absent over a directory: %+v", c)
	}
	if _, err := os.Stat(filepath.Join(dirLink, "keep")); err != nil {
		t.Fatalf("directory content lost: %v", err)
	}

	// Remove: destructive; a regular file under the name is kept.
	c = converge(t, build(t, res("symlink", "/etc/nginx/sites-enabled/app", map[string]any{"state": "absent"}), g.env()))
	if attrsOf(c) != "state=destructive" || wire.ActionOf(c) != wire.ActionDelete {
		t.Fatalf("remove: %+v", c)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link still there: %v", err)
	}
	// absent over a regular file: no change, and a forged delete keeps it.
	mustWrite(t, link, "data")
	gone := build(t, res("symlink", "/etc/nginx/sites-enabled/app", map[string]any{"state": "absent"}), g.env())
	if c := converge(t, gone); len(c) != 0 {
		t.Fatalf("absent over a file: %+v", c)
	}
	if err := gone.Apply(ctx, []wire.Change{{Attr: wire.AttrState, Risk: wire.RiskDestructive}}); err != nil {
		t.Fatalf("forged delete: %v", err)
	}
	if b, _ := os.ReadFile(link); string(b) != "data" {
		t.Fatalf("absent removed a regular file: %q", b)
	}
	os.Remove(link)

	// A missing parent: absent on Observe, a clear error on Apply.
	missing := build(t, res("symlink", "/opt/app/current", map[string]any{"target": "v1"}), g.env())
	obs, err = missing.Observe(ctx)
	if err != nil || obs[wire.AttrState] != wire.StateAbsent {
		t.Fatalf("missing parent: %v %v", obs, err)
	}
	if err := missing.Apply(ctx, missing.Diff(obs)); err == nil || !strings.Contains(err.Error(), "parent directory") {
		t.Fatalf("missing parent Apply: %v", err)
	}

	// Watch names the link itself.
	if w := build(t, want, g.env()).Watch(); len(w) != 1 || w[0] != link {
		t.Fatalf("Watch = %v", w)
	}
}

// The target is data: a link to a secret outside the guest root is never
// read, written or chowned, and a dangling target is fine.
func TestSymlinkTargetIsData(t *testing.T) {
	ctx := context.Background()
	g := newFakeGuest(t)
	dir, secretFile := outside(t)
	if err := os.MkdirAll(filepath.Join(g.root, "srv"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(g.root, "srv/l")
	for _, target := range []string{secretFile, filepath.Join(dir, "sub"), "/does/not/exist", "-rf", "a b\nc"} {
		converge(t, build(t, res("symlink", "/srv/l", map[string]any{"target": target}), g.env()))
		if got := readLink(t, link); got != target {
			t.Fatalf("link = %q, want %q", got, target)
		}
		st, err := build(t, res("symlink", "/srv/l", map[string]any{"target": target}), g.env()).Observe(ctx)
		if err != nil || st["target"] != target || len(st) != 2 {
			t.Fatalf("observe %q: %v %v", target, st, err)
		}
	}
	converge(t, build(t, res("symlink", "/srv/l", map[string]any{"state": "absent"}), g.env()))
	unchangedOutside(t, dir, secretFile)
	if len(g.calls) != 0 {
		t.Fatalf("symlink ran commands: %v", g.calls)
	}
}

// A guest user who owns a parent swaps it for a symlink (D-054).
func TestSymlinkResourceSymlinkedParent(t *testing.T) {
	ctx := context.Background()
	g := newFakeGuest(t)
	dir, secretFile := outside(t)
	if err := os.MkdirAll(filepath.Join(g.root, "home/deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(g.root, "home/deploy/app")); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"present", "absent"} {
		rs := build(t, res("symlink", "/home/deploy/app/secret", map[string]any{"target": "/etc/shadow", "state": state}), g.env())
		if st, err := rs.Observe(ctx); err == nil || !strings.Contains(err.Error(), "path component /home/deploy/app is a symlink; use the real path") {
			t.Fatalf("%s: Observe %v %v", state, st, err)
		} else if strings.Contains(err.Error(), dir) {
			t.Fatalf("error shows the link target: %v", err)
		}
		forged := []wire.Change{{Attr: wire.AttrState, Risk: wire.RiskDestructive}, {Attr: attrKind, Risk: wire.RiskDestructive}, {Attr: "target", Risk: wire.RiskReversible}}
		if err := rs.Apply(ctx, forged); err == nil || !strings.Contains(err.Error(), "is a symlink") {
			t.Fatalf("%s: Apply %v", state, err)
		}
	}
	unchangedOutside(t, dir, secretFile)
}

func TestSymlinkNewChecks(t *testing.T) {
	env := newFakeGuest(t).env()
	for _, attrs := range []map[string]any{
		{"path": "/etc/x"},                     // present needs a target
		{"path": "/etc/x", "target": ""},       // empty target
		{"path": "/etc/x", "target": "a\x00b"}, // NUL
		{"path": "/etc/x", "target": strings.Repeat("a", maxLinkTarget+1)},
		{"path": "/etc/../x", "target": "y"},
		{"path": "/etc/x/", "target": "y"},
		{"path": "/etc/x", "target": "y", "state": "gone"},
		{"path": "/etc/x", "target": "y", "owner": "root"},
	} {
		r := wire.Resource{ID: wire.ID("symlink", attrs["path"].(string)), Type: "symlink", Attrs: attrs}
		if _, err := New(r, env); err == nil {
			t.Errorf("%v: want error", attrs)
		}
	}
}

// Leaf races (D-163): a writer of the directory swaps the node after
// Apply checked it. The helpers get the stale kind, as in that race.
func TestSymlinkLeafRaces(t *testing.T) {
	g := newFakeGuest(t)
	dir := filepath.Join(g.root, "srv")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dfd, name, err := openParent(g.root, "/srv/l")
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dfd)
	p := filepath.Join(dir, "l")
	tmp := filepath.Join(dir, tempLinkName(name))
	keep := func(what string) {
		t.Helper()
		if b, err := os.ReadFile(p); err != nil || string(b) != "victim" {
			t.Fatalf("%s: swapped-in file lost: %q %v", what, b, err)
		}
		if _, err := os.Lstat(tmp); !os.IsNotExist(err) {
			t.Fatalf("%s: temp name left: %v", what, err)
		}
	}
	mustWrite(t, p, "victim")

	// Checked as nothing, a file appeared: RENAME_NOREPLACE refuses.
	if err := symlinkAt(dfd, name, "/srv/l", "x", ""); err == nil || !strings.Contains(err.Error(), "plan again") {
		t.Fatalf("noreplace: %v", err)
	}
	keep("noreplace")
	// Checked as a link, a file appeared: exchanged, checked, put back.
	if err := symlinkAt(dfd, name, "/srv/l", "x", "symlink"); err == nil || !strings.Contains(err.Error(), "plan again") {
		t.Fatalf("exchange: %v", err)
	}
	keep("exchange")
	// Checked as a link for removal, a file appeared: moved back.
	if err := removeLinkAt(dfd, name, "/srv/l"); err == nil || !strings.Contains(err.Error(), "plan again") {
		t.Fatalf("remove: %v", err)
	}
	keep("remove")

	// A stale temp link from a crash is replaced by the next Apply.
	os.Remove(p)
	if err := os.Symlink("stale", tmp); err != nil {
		t.Fatal(err)
	}
	converge(t, build(t, res("symlink", "/srv/l", map[string]any{"target": "new"}), g.env()))
	if got := readLink(t, p); got != "new" {
		t.Fatalf("link = %q", got)
	}
	if _, err := os.Lstat(tmp); !os.IsNotExist(err) {
		t.Fatalf("stale temp link left: %v", err)
	}
	// Another node under the temp name is kept and named.
	mustWrite(t, tmp, "not ours")
	err = build(t, res("symlink", "/srv/l", map[string]any{"target": "newer"}), g.env()).Apply(context.Background(),
		[]wire.Change{{Attr: "target", Risk: wire.RiskReversible}})
	if err == nil || !strings.Contains(err.Error(), "is a file") {
		t.Fatalf("occupied temp name: %v", err)
	}
	if b, _ := os.ReadFile(tmp); string(b) != "not ours" || readLink(t, p) != "new" {
		t.Fatalf("occupied temp name changed: %q", b)
	}
}
