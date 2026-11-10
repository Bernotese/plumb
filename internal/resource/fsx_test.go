package resource

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

type randomReadFunc func([]byte) (int, error)

func (f randomReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestWriteAtRenameFailure(t *testing.T) {
	g := newFakeGuest(t)
	dirfd, name, err := openParent(g.root, "/srv/target")
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirfd)

	// writeAt reads randomness after checking the target, before creating
	// its temp file. Replace the target at that point without a timing race.
	original := rand.Reader
	t.Cleanup(func() { rand.Reader = original })
	rand.Reader = randomReadFunc(func(p []byte) (int, error) {
		mustWrite(t, filepath.Join(g.root, "srv/target/child"), "keep")
		return original.Read(p)
	})
	err = writeAt(dirfd, name, "/srv/target", []byte("new"), 0o600, os.Getuid(), os.Getgid())
	if !errors.Is(err, unix.EISDIR) {
		t.Fatalf("rename over a non-empty directory: got %v, want EISDIR", err)
	}
	entries, err := os.ReadDir(filepath.Join(g.root, "srv"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "target" {
		t.Fatalf("failed rename left a temp file: %v, %v", entries, err)
	}
	if data, err := os.ReadFile(filepath.Join(g.root, "srv/target/child")); err != nil || string(data) != "keep" {
		t.Fatalf("failed rename changed target: %q, %v", data, err)
	}
}
