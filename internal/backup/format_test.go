package backup

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNameRE(t *testing.T) {
	now := time.Date(2026, 11, 13, 3, 0, 0, 0, time.UTC)
	good := []string{
		"plumb-" + now.Format(NameLayout) + FileSuffix,
		"plumb-20261113T030000Z-2.pbk",
	}
	for _, n := range good {
		if !NameRE.MatchString(n) {
			t.Errorf("%q refused", n)
		}
	}
	bad := []string{
		"", "plumb-.pbk", "../plumb-1.pbk", "plumb-1.pbk/../x", "plumb-1.pbk\n",
		"/etc/plumb-1.pbk", "plumb-1 .pbk", "plumb-1.pbk.sha256", TempPrefix + "plumb-1.pbk",
		PointerName, "plumb-1.PBK", "x-plumb-1.pbk", "plumb----.pbk", "plumb-20261113T030000Z-1234.pbk",
		"plumb-20261113T0300Z.pbk", "plumb-20261113T030000.pbk",
	}
	for _, n := range bad {
		if NameRE.MatchString(n) {
			t.Errorf("%q accepted", n)
		}
	}
}

// The header must stay small and hold no secret field; the fixed values
// must fit the limits that a reader checks.
func TestHeaderShape(t *testing.T) {
	h := Header{
		V: Version, CreatedAt: "2026-11-13T03:00:00Z", PlumbVersion: "v0.9.0", SchemaVer: 13,
		InstallID: "0f6c2a8e-1b7d-4c55-9a51-3c1f4a2b9d10", Node: "pve-dev", Pool: "plumb",
		AgentURL: "https://192.0.2.10:8444", KDF: KDF, Cipher: Cipher, ChunkSize: ChunkSize,
		Salt: strings.Repeat("A", 43) + "=", NoncePrefix: "AAAAAAAAAA==", KeyFP: strings.Repeat("0", 2*FingerprintBytes),
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > MaxHeaderBytes/4 {
		t.Errorf("a normal header is %d bytes; the limit %d leaves too little room", len(b), MaxHeaderBytes)
	}
	for _, word := range []string{"secret", "password", "token", "key\""} {
		if strings.Contains(strings.ToLower(string(b)), word) {
			t.Errorf("header has a field like %q: %s", word, b)
		}
	}
	if len(Magic) != 8 || len(PlainEntries) != 4 || PlainEntries[0] != EntryManifest {
		t.Error("magic or entry list changed: that is a new Version")
	}
	if KDFInfoData == KDFInfoFingerprint {
		t.Error("the two HKDF labels must differ")
	}
}
