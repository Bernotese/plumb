package api

import (
	"os"
	"testing"
)

// TestMain removes the migrated database template of newEnv (T-211).
func TestMain(m *testing.M) {
	code := m.Run()
	if migrated.dir != "" {
		_ = os.RemoveAll(migrated.dir)
	}
	os.Exit(code)
}
