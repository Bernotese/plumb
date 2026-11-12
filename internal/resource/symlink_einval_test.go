package resource

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// D-228, T-078: a file system without RENAME_NOREPLACE/RENAME_EXCHANGE
// (ZFS before 2.2) gives EINVAL. The link fails with a clear text, and
// there is no fallback to a plain rename.
func TestSymlinkRenameEINVAL(t *testing.T) {
	old := renameat2
	renameat2 = func(int, string, int, string, uint) error { return unix.EINVAL }
	t.Cleanup(func() { renameat2 = old })
	ctx := context.Background()
	g := newFakeGuest(t)
	os.MkdirAll(filepath.Join(g.root, "srv"), 0o755)
	mustWrite(t, filepath.Join(g.root, "srv/real"), "x")
	if err := os.Symlink("real", filepath.Join(g.root, "srv/old")); err != nil {
		t.Fatal(err)
	}
	want := "the file system of /srv does not support atomic rename (RENAME_NOREPLACE/RENAME_EXCHANGE); on ZFS it needs ZFS 2.2 or newer"
	for _, c := range []struct {
		path  string
		attrs map[string]any
	}{
		{"/srv/new", map[string]any{"target": "real"}},                    // over nothing: NOREPLACE
		{"/srv/old", map[string]any{"target": "other"}},                   // over a link: EXCHANGE
		{"/srv/old", map[string]any{"target": "real", "state": "absent"}}, // remove: NOREPLACE
	} {
		rs := build(t, res("symlink", c.path, c.attrs), g.env())
		err := rs.Apply(ctx, plan(t, rs))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s %v: err %v", c.path, c.attrs, err)
		}
	}
	// Nothing changed, and no temp link is left.
	if _, err := os.Lstat(filepath.Join(g.root, "srv/new")); err == nil {
		t.Error("the link was made without renameat2")
	}
	if tgt, err := os.Readlink(filepath.Join(g.root, "srv/old")); err != nil || tgt != "real" {
		t.Errorf("old link: %q %v", tgt, err)
	}
	ents, _ := os.ReadDir(filepath.Join(g.root, "srv"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".plumb-link-") {
			t.Errorf("temp link left: %s", e.Name())
		}
	}
}

func TestRename2WrappedEINVAL(t *testing.T) {
	old := renameat2
	t.Cleanup(func() { renameat2 = old })
	for _, flags := range []uint{unix.RENAME_NOREPLACE, unix.RENAME_EXCHANGE} {
		calls := 0
		renameat2 = func(_ int, _ string, _ int, _ string, got uint) error {
			calls++
			if got != flags {
				t.Error("rename flags changed")
			}
			return fmt.Errorf("rename: %w", unix.EINVAL)
		}
		err := rename2(-1, "from", "to", flags, "/etc/nginx/sites-enabled/default")
		want := "the file system of /etc/nginx/sites-enabled does not support atomic rename (RENAME_NOREPLACE/RENAME_EXCHANGE); on ZFS it needs ZFS 2.2 or newer"
		if err == nil || err.Error() != want {
			t.Errorf("error = %v, want %s", err, want)
		}
		if calls != 1 {
			t.Errorf("rename called %d times, want 1", calls)
		}
	}
	// Other errno values must retain their identity for caller handling.
	renameat2 = func(int, string, int, string, uint) error { return unix.EACCES }
	if err := rename2(-1, "from", "to", unix.RENAME_NOREPLACE, "/srv/link"); !errors.Is(err, unix.EACCES) {
		t.Errorf("non-EINVAL error changed: %v", err)
	}
}
