package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// migratedTemplate is one database with every migration applied, made
// once per test binary (T-227 row 30, the #316 follow-up): openTemp copies
// it instead of running every migration again, which under -race costs
// the most CPU of this package. A test that checks a migration never
// uses it: openAt builds the chain from 0001 up to its version.
var migratedTemplate struct {
	once sync.Once
	dir  string
	err  error
}

func copyMigratedTemplate(t *testing.T, dst string) {
	t.Helper()
	migratedTemplate.once.Do(func() {
		dir, err := os.MkdirTemp("", "plumb-store-migrated-")
		if err != nil {
			migratedTemplate.err = err
			return
		}
		migratedTemplate.dir = dir
		path := filepath.Join(dir, "plumb.db")
		s, err := Open(context.Background(), path)
		if err != nil {
			migratedTemplate.err = err
			return
		}
		if err := s.Close(); err != nil {
			migratedTemplate.err = err
			return
		}
		// A clean close leaves no WAL: the main file holds everything.
		if _, err := os.Stat(path + "-wal"); err == nil {
			migratedTemplate.err = errors.New("the migrated template still has a WAL file")
		}
	})
	if migratedTemplate.err != nil {
		t.Fatal(migratedTemplate.err)
	}
	b, err := os.ReadFile(filepath.Join(migratedTemplate.dir, "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestMain removes the migrated template (T-227 row 30).
func TestMain(m *testing.M) {
	code := m.Run()
	if migratedTemplate.dir != "" {
		_ = os.RemoveAll(migratedTemplate.dir)
	}
	os.Exit(code)
}
