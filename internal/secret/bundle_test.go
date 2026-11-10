package secret

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func bundleJSON(t *testing.T, b Bundle) []byte {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBundleRoundTrip(t *testing.T) {
	values := map[string]string{"app-db": "hunter2 ✓", "deploy-key": strings.Repeat("k", 4096)}
	const pass = "a long passphrase"
	b, err := SealBundle(values, pass, BundleKDF)
	if err != nil {
		t.Fatal(err)
	}
	raw := bundleJSON(t, b)
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), pass) {
		t.Fatal("the bundle holds a value or the passphrase")
	}
	parsed, err := ParseBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenBundle(parsed, pass)
	if err != nil || len(got) != 2 || got["app-db"] != values["app-db"] || got["deploy-key"] != values["deploy-key"] {
		t.Fatalf("open: %v %v", len(got), err)
	}
	// Wrong passphrase, changed ciphertext, nonce or salt: refused.
	if _, err := OpenBundle(parsed, pass+"x"); !errors.Is(err, ErrBundlePassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	for _, field := range []string{"data", "nonce", "salt"} {
		c := parsed
		flip := func(s string) string {
			b, _ := base64.StdEncoding.DecodeString(s)
			b[len(b)-1] ^= 1
			return base64.StdEncoding.EncodeToString(b)
		}
		switch field {
		case "data":
			c.Data = flip(c.Data)
		case "nonce":
			c.Nonce = flip(c.Nonce)
		case "salt":
			c.Salt = flip(c.Salt)
		}
		if _, err := OpenBundle(c, pass); !errors.Is(err, ErrBundlePassphrase) {
			t.Errorf("changed %s: %v", field, err)
		}
	}
	// Errors never hold the passphrase or a value.
	for _, e := range []error{ErrBundleFormat, ErrBundleKDF, ErrBundlePassphrase} {
		if strings.Contains(e.Error(), pass) || strings.Contains(e.Error(), "hunter2") {
			t.Fatal("an error holds a secret")
		}
	}
}

// A bundle with other KDF parameters, or a broken one, is refused before
// any argon2 work: a crafted file cannot choose the memory use (D-094).
func TestBundleRefusedWithoutWork(t *testing.T) {
	good, err := SealBundle(map[string]string{"a": "b"}, "passphrase!!", KDF{Name: "argon2id", Time: 1, MemoryKiB: 64, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(b *Bundle){
		"memory 4 GiB": func(b *Bundle) { b.KDF = BundleKDF; b.KDF.MemoryKiB = 4 << 20 },
		"time 1":       func(b *Bundle) { b.KDF = BundleKDF; b.KDF.Time = 1 },
		"threads 255":  func(b *Bundle) { b.KDF = BundleKDF; b.KDF.Threads = 255 },
		"scrypt":       func(b *Bundle) { b.KDF = BundleKDF; b.KDF.Name = "scrypt" },
	}
	for name, edit := range cases {
		b := good
		edit(&b)
		start := time.Now()
		if _, err := ParseBundle(bundleJSON(t, b)); !errors.Is(err, ErrBundleKDF) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := OpenBundle(b, "passphrase!!"); !errors.Is(err, ErrBundleKDF) {
			t.Errorf("%s open: %v", name, err)
		}
		if d := time.Since(start); d > 50*time.Millisecond {
			t.Errorf("%s took %v: argon2 ran", name, d)
		}
	}
	// Truncated or broken bundles.
	b := good
	b.KDF = BundleKDF
	raw := bundleJSON(t, b)
	for name, bad := range map[string][]byte{
		"truncated":     raw[:len(raw)/2],
		"not base64":    []byte(strings.Replace(string(raw), b.Data, "!!!", 1)),
		"short nonce":   []byte(strings.Replace(string(raw), b.Nonce, "AAAA", 1)),
		"wrong kind":    []byte(strings.Replace(string(raw), `"SecretBundle"`, `"Other"`, 1)),
		"unknown field": []byte(strings.Replace(string(raw), `{"kind"`, `{"x":1,"kind"`, 1)),
		"no data":       []byte(strings.Replace(string(raw), `"`+b.Data+`"`, `""`, 1)),
	} {
		if _, err := ParseBundle(bad); !errors.Is(err, ErrBundleFormat) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Nit 2 of the T-033 review: the plaintext reader refuses what
// encoding/json would change silently.
func TestBundleStrictPlaintext(t *testing.T) {
	bs := string(rune(92))
	for name, raw := range map[string]string{
		"duplicate name":   `{"a":"x","a":"y"}`,
		"lone high":        `{"a":"x` + bs + `ud800"}`,
		"lone low":         `{"a":"` + bs + `udc00"}`,
		"number value":     `{"a":1}`,
		"array":            `["a"]`,
		"data after":       `{"a":"x"} {}`,
		"high then escape": `{"a":"` + bs + `ud800` + bs + `n"}`,
	} {
		if _, ok := strictValues([]byte(raw)); ok {
			t.Errorf("%s accepted", name)
		}
	}
	for _, raw := range []string{`{"a":"` + bs + `ud83d` + bs + `ude00"}`, `{"a":"` + bs + bs + `ud800"}`, `{}`, `{"a":"` + bs + `ufffd"}`} {
		if _, ok := strictValues([]byte(raw)); !ok {
			t.Errorf("%s refused", raw)
		}
	}
}
