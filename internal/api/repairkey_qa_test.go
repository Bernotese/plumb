package api

import (
	"bytes"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T-195 (T-191 part), B6: the repair key is sealed at rest. No file under
// the data dir (the DB, its WAL, the link dir, backups, temp files) holds
// a byte form of the private key (the leak scan of
// TestRepairKeyLeakScan covers export, API answers, events, the log and
// the backup file); the public key is there (a positive control, so the
// scan cannot pass on an empty file).
func TestRepairKeyQADBFileSealed(t *testing.T) {
	k := newKeyEnv(t)
	adm := k.admin()
	k.seedAgent(adm, slice3Caps())
	adm.do("POST", "/api/v1/settings/repair-key/rotate", nil) // a current and a previous key
	needles := k.privateNeedles()
	if len(needles) == 0 {
		t.Fatal("no private key needles")
	}
	keys, err := k.st.RepairKeys(k.ctx)
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys %d %v", len(keys), err)
	}
	// n1 of #305: also the hex form of a 32-byte seed and the OpenSSH PEM
	// header (a bare "PRIVATE KEY" would match the controller's CA key).
	for _, nd := range append([][]byte(nil), needles...) {
		if len(nd) == 32 {
			needles = append(needles, []byte(hex.EncodeToString(nd)))
		}
	}
	needles = append(needles, []byte("OPENSSH PRIVATE KEY"))
	// Every file under the data dir: DB, WAL, link dir, backups, temp files.
	var hay []byte
	files := 0
	err = filepath.WalkDir(k.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files++
		for _, nd := range needles {
			if bytes.Contains(b, nd) {
				t.Errorf("%s holds private key bytes", strings.TrimPrefix(p, k.dir))
			}
		}
		if filepath.Base(p) == "plumb.db" || filepath.Base(p) == "plumb.db-wal" {
			hay = append(hay, b...)
		}
		return nil
	})
	if err != nil || files < 2 {
		t.Fatalf("walk: %v, %d files", err, files)
	}
	for _, key := range keys {
		if !bytes.Contains(hay, []byte(key.Fingerprint)) {
			t.Fatalf("positive control: the fingerprint %s is not in the DB file", key.Fingerprint)
		}
	}
}
