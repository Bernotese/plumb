package issue_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Bernotese/plumb/internal/store"
)

// migratedTemplate is one database with every migration applied, made
// once per test binary (T-227 row 30, the #316 follow-up): newEnv copies
// it instead of running every migration again (under -race the SQLite
// parser of the migrations runs instrumented). This package tests no
// migration; store.Open still checks the version of the copy.
var migratedTemplate struct {
	once sync.Once
	dir  string
	err  error
}

func copyMigratedTemplate(t *testing.T, dst string) {
	t.Helper()
	migratedTemplate.once.Do(func() {
		dir, err := os.MkdirTemp("", "plumb-issue-migrated-")
		if err != nil {
			migratedTemplate.err = err
			return
		}
		migratedTemplate.dir = dir
		path := filepath.Join(dir, "plumb.db")
		st, err := store.Open(context.Background(), path)
		if err != nil {
			migratedTemplate.err = err
			return
		}
		if err := st.Close(); err != nil {
			migratedTemplate.err = err
			return
		}
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
