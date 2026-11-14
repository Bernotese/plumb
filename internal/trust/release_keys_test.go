//go:build !plumbtest

package trust

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This test runs in the release build configuration. Keep the expected
// vendor fingerprints independent of the production map (D-347, D-369).
func TestReleasePinsOnlyVendorKeys(t *testing.T) {
	want := map[Source]string{
		Ubuntu:  "D2EB44626FDDC30B513D5BB71A5D6C4C7DB87C81",
		Proxmox: "24B30F06ECC1836A4E5EFECBA7BCD1420BFE778E",
	}
	if !reflect.DeepEqual(Fingerprints, want) {
		t.Fatalf("release fingerprints: %v; want only %v", Fingerprints, want)
	}
	for src, fp := range want {
		keys, err := PinnedKey(src)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 1 || fingerprint(keys[0].PrimaryKey) != fp {
			t.Fatalf("release keyring for %s must contain only %s", src, fp)
		}
	}
}

// Keep the tag out of all paths that produce distributable binaries.
func TestReleaseBuildNeverSetsTestTag(t *testing.T) {
	for _, path := range []string{"../../Makefile", "../../.github/workflows/release.yml", "../../scripts/package-release.sh"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "plumbtest") {
			t.Fatalf("%s enables the test key tag", path)
		}
	}
	keys, err := PinnedKey(Ubuntu)
	if err != nil {
		t.Fatal(err)
	}
	list, err := os.ReadFile("testdata/f5/new-SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile("testdata/f5/new-SHA256SUMS.gpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(keys, list, sig, time.Now()); !errors.Is(err, ErrSignature) {
		t.Fatalf("release accepted test signature: %v", err)
	}
}
