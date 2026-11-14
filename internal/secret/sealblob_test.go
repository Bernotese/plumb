package secret

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/Bernotese/plumb/internal/store"
)

// A blob opens only with its purpose, its name and the same key; the
// secret-store purpose is refused for blobs (T-191).
func TestSealBlob(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, err := Open(ctx, st, NewKey())
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("seed-bytes-0123456789abcdef01234")
	nonce, sealed, err := s.SealBlob(PurposeRepairKey, "repair-key/a1", plain)
	if err != nil || bytes.Contains(sealed, plain) {
		t.Fatalf("seal: %v", err)
	}
	if got, err := s.OpenBlob(PurposeRepairKey, "repair-key/a1", nonce, sealed); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := s.OpenBlob("plumb other purpose", "repair-key/a1", nonce, sealed); err == nil {
		t.Fatal("another purpose opened the blob")
	}
	if _, err := s.OpenBlob(PurposeRepairKey, "repair-key/b2", nonce, sealed); err == nil {
		t.Fatal("another name opened the blob")
	}
	if _, err := s.OpenBlob(PurposeSecretStore, "repair-key/a1", nonce, sealed); err == nil {
		t.Fatal("the secret-store purpose opened the blob")
	}
	if _, _, err := s.SealBlob(PurposeSecretStore, "x", plain); err == nil {
		t.Fatal("a blob with the secret-store purpose was sealed")
	}
	st2, err := store.Open(ctx, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	other, err := Open(ctx, st2, NewKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.OpenBlob(PurposeRepairKey, "repair-key/a1", nonce, sealed); err == nil {
		t.Fatal("another key opened the blob")
	}
}
