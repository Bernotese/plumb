//go:build guest

package guest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSymlinkQAAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("guest test must run as root")
	}
	link := filepath.Join(t.TempDir(), "link")
	for _, target := range []string{"link", "../missing/../target", strings.Repeat("../", 1365), "a b\nc", "-rf"} {
		want := rsrc("symlink", link, map[string]any{"target": target})
		converge(t, want)
		if got, err := os.Readlink(link); err != nil || got != target {
			t.Fatalf("readlink = %q: %v", got, err)
		}
		// A second Apply must preserve the existing link inode.
		before, err := os.Lstat(link)
		if err != nil {
			t.Fatal(err)
		}
		rs := build(t, want)
		if err := rs.Apply(context.Background(), plan(t, rs)); err != nil {
			t.Fatal(err)
		}
		after, err := os.Lstat(link)
		if err != nil || !os.SameFile(before, after) {
			t.Fatalf("second apply changed link: %v", err)
		}
	}
	for _, state := range []string{"present", "absent"} {
		rs := build(t, rsrc("symlink", link, map[string]any{"target": "new", "state": state}))
		changes := plan(t, rs)
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(link, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := rs.Apply(context.Background(), changes); err == nil {
			t.Fatal("stale plan replaced file")
		}
		if b, err := os.ReadFile(link); err != nil || string(b) != "keep" {
			t.Fatalf("file changed: %q %v", b, err)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("old", link); err != nil {
			t.Fatal(err)
		}
	}
}
