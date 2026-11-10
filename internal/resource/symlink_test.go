package resource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// outside makes a directory outside the guest root with a secret file.
func outside(t *testing.T) (dir, secretFile string) {
	t.Helper()
	dir = t.TempDir()
	secretFile = filepath.Join(dir, "secret")
	if err := os.WriteFile(secretFile, []byte("TOP SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, secretFile
}

func unchangedOutside(t *testing.T, dir, secretFile string) {
	t.Helper()
	b, err := os.ReadFile(secretFile)
	fi, _ := os.Stat(secretFile)
	if err != nil || string(b) != "TOP SECRET\n" || fi.Mode().Perm() != 0o600 {
		t.Fatalf("outside file changed: %q %v %v", b, fi.Mode(), err)
	}
	sub, _ := os.Stat(filepath.Join(dir, "sub"))
	if sub.Mode().Perm() != 0o700 {
		t.Fatalf("outside directory changed: %v", sub.Mode())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("outside directory has new entries: %v", entries)
	}
}

// A guest user who owns /home/deploy swaps app for a symlink.
func TestSymlinkedParent(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name string
		r    wire.Resource
	}{
		{"file content", res("file", "/home/deploy/app/secret", map[string]any{"content": "x\n"})},
		{"file mode only", res("file", "/home/deploy/app/secret", map[string]any{"content": "TOP SECRET\n", "mode": "0644"})},
		{"file delete", res("file", "/home/deploy/app/secret", map[string]any{"state": "absent"})},
		{"directory create", res("directory", "/home/deploy/app/new", nil)},
		{"directory chmod", res("directory", "/home/deploy/app/sub", map[string]any{"mode": "0777"})},
		{"directory delete", res("directory", "/home/deploy/app/sub", map[string]any{"state": "absent"})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := newFakeGuest(t)
			dir, secretFile := outside(t)
			if err := os.MkdirAll(filepath.Join(g.root, "home/deploy"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(dir, filepath.Join(g.root, "home/deploy/app")); err != nil {
				t.Fatal(err)
			}
			rs := build(t, tt.r, g.env())
			st, err := rs.Observe(ctx)
			if err == nil || !strings.Contains(err.Error(), "path component /home/deploy/app is a symlink; use the real path") {
				t.Fatalf("Observe: state %v, err %v", st, err)
			}
			if strings.Contains(err.Error(), dir) {
				t.Fatalf("error shows the link target: %v", err)
			}
			// Apply with a forged plan must not touch the outside either.
			forged := []wire.Change{
				{Attr: wire.AttrState, From: wire.StateAbsent, To: wire.StatePresent, Risk: wire.RiskReversible},
				{Attr: "content", Risk: wire.RiskReversible},
				{Attr: "mode", Risk: wire.RiskReversible},
				{Attr: "owner", Risk: wire.RiskReversible},
			}
			if err := rs.Apply(ctx, forged); err == nil || !strings.Contains(err.Error(), "is a symlink") {
				t.Fatalf("Apply: err %v", err)
			}
			unchangedOutside(t, dir, secretFile)
		})
	}
}

func TestSymlinkRules(t *testing.T) {
	ctx := context.Background()
	g := newFakeGuest(t)
	// A distro-style link in the middle: /var/run → /run.
	if err := os.MkdirAll(filepath.Join(g.root, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(g.root, "var"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/run", filepath.Join(g.root, "var/run")); err != nil {
		t.Fatal(err)
	}
	r := build(t, res("file", "/var/run/app.pid", map[string]any{"content": "1"}), g.env())
	if _, err := r.Observe(ctx); err == nil || !strings.Contains(err.Error(), "path component /var/run is a symlink") {
		t.Fatalf("link in the middle: %v", err)
	}
	converge(t, build(t, res("file", "/run/app.pid", map[string]any{"content": "1"}), g.env()))

	// A symlink as last component: an error for present and for absent.
	mustWrite(t, filepath.Join(g.root, "srv/real"), "x")
	if err := os.Symlink("real", filepath.Join(g.root, "srv/link")); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"present", "absent"} {
		for _, typ := range []string{"file", "directory"} {
			rs := build(t, res(typ, "/srv/link", map[string]any{"state": state}), g.env())
			if _, err := rs.Observe(ctx); err == nil || !strings.Contains(err.Error(), "path component /srv/link is a symlink") {
				t.Errorf("%s %s: Observe err %v", typ, state, err)
			}
			if err := rs.Apply(ctx, []wire.Change{{Attr: wire.AttrState, Risk: wire.RiskDestructive}}); err == nil {
				t.Errorf("%s %s: Apply removed or replaced the link", typ, state)
			}
		}
	}
	if _, err := os.Lstat(filepath.Join(g.root, "srv/link")); err != nil {
		t.Fatalf("link is gone: %v", err)
	}

	// A hard-linked file is refused: it may be a file outside the directory.
	if err := os.Link(filepath.Join(g.root, "srv/real"), filepath.Join(g.root, "srv/hard")); err != nil {
		t.Fatal(err)
	}
	hard := build(t, res("file", "/srv/hard", map[string]any{"content": "y", "mode": "0600"}), g.env())
	if _, err := hard.Observe(ctx); err == nil || !strings.Contains(err.Error(), "2 hard links") {
		t.Fatalf("hard link: Observe err %v", err)
	}
	if err := hard.Apply(ctx, []wire.Change{{Attr: "mode", Risk: wire.RiskReversible}}); err == nil {
		t.Fatal("hard link: Apply changed the mode")
	}
	if b, _ := os.ReadFile(filepath.Join(g.root, "srv/real")); string(b) != "x" {
		t.Fatalf("hard-linked file changed: %q", b)
	}

	// A missing parent: absent on Observe, a clear error on Apply.
	missing := build(t, res("file", "/opt/app/conf", map[string]any{"content": "x"}), g.env())
	if st, err := missing.Observe(ctx); err != nil || st[wire.AttrState] != wire.StateAbsent {
		t.Fatalf("missing parent: %v %v", st, err)
	}
}

func TestPathChecks(t *testing.T) {
	env := newFakeGuest(t).env()
	for _, p := range []string{"/etc/../root/x", "/etc//x", "/etc/./x", "relative/x", "/etc/x\x00y", "/etc/"} {
		for _, typ := range []string{"file", "directory"} {
			r := wire.Resource{ID: typ + ":" + p, Type: typ, Attrs: map[string]any{"path": p}}
			if _, err := New(r, env); err == nil {
				t.Errorf("%s %q: want error", typ, p)
			}
		}
	}
}
