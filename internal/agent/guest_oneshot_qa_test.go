//go:build guest

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T-195 (mutant A3, D-390): as root, a 0600 token file of another owner is
// not a token: it was not written by install.sh (root). The check needs a
// file of another uid, so it runs only in the guest jobs (root).
func TestGuestOneShotForeignOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to make a file of another owner")
	}
	p := filepath.Join(t.TempDir(), "enroll.token")
	if err := os.WriteFile(p, []byte("a-good-looking-token-0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(p, 65534, 65534); err != nil { // nobody
		t.Fatal(err)
	}
	if tok, err := readOneShot(p); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("a token file of uid 65534 was read: %q %v", tok, err)
	}
	// The same file owned by root is a token (the test checks the owner,
	// not something else).
	if err := os.Chown(p, 0, 0); err != nil {
		t.Fatal(err)
	}
	if tok, err := readOneShot(p); err != nil || tok != "a-good-looking-token-0123456789" {
		t.Fatalf("root's token file: %q %v", tok, err)
	}
}
