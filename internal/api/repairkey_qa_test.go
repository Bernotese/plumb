package api

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// T-195 (T-191 part), B6: the repair key is sealed at rest. The raw DB
// file and its WAL hold no byte form of the private key (the leak scan of
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
	var hay []byte
	for _, p := range []string{"plumb.db", "plumb.db-wal"} {
		b, err := os.ReadFile(filepath.Join(k.dir, p))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		hay = append(hay, b...)
	}
	for _, n := range needles {
		if bytes.Contains(hay, n) {
			t.Fatal("the DB file or its WAL holds private key bytes")
		}
	}
	for _, key := range keys {
		if !bytes.Contains(hay, []byte(key.Fingerprint)) {
			t.Fatalf("positive control: the fingerprint %s is not in the DB file", key.Fingerprint)
		}
	}
}
