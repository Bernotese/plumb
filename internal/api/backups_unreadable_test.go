package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bernotese/plumb/internal/backup"
)

// TestBackupsUnreadableListed (T-208, host finding F2): an unreadable
// backup file is in GET /backups with state unreadable and sha256 null,
// checked against the contract.
func TestBackupsUnreadableListed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file")
	}
	e := newEnv(t, true)
	adm := e.admin()
	rc := &recorder{t: t}
	if code, body := rc.do(adm, "POST", "/api/v1/backups", "/backups", nil); code != 202 {
		t.Fatalf("POST: %d %s", code, body)
	}
	e.backups.Wait()
	matches, _ := filepath.Glob(filepath.Join(e.dir, "*", "plumb-*.pbk*"))
	if len(matches) != 2 {
		t.Fatalf("backup files: %v", matches)
	}
	for _, p := range matches {
		os.Chmod(p, 0)
		t.Cleanup(func() { os.Chmod(p, 0o600) })
	}
	code, body := rc.do(adm, "GET", "/api/v1/backups", "/backups", nil)
	var v struct {
		Items []struct {
			State  string  `json:"state"`
			SHA256 *string `json:"sha256"`
		} `json:"items"`
	}
	if code != 200 || json.Unmarshal(body, &v) != nil || len(v.Items) != 1 || v.Items[0].State != backup.ItemUnreadable || v.Items[0].SHA256 != nil {
		t.Fatalf("list: %d %s", code, body)
	}
}
