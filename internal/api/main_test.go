package api

import (
	"fmt"
	"os"
	"testing"
)

// TestMain removes the migrated database template of newEnv (T-211).
func TestMain(m *testing.M) {
	code := m.Run()
	if err := logScanCoverage(); code == 0 && err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	if migrated.dir != "" {
		_ = os.RemoveAll(migrated.dir)
	}
	os.Exit(code)
}
