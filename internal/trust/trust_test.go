package trust

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// The day the test data was fetched; the real signatures are from before.
var testNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPinnedKeys(t *testing.T) {
	for src, fp := range Fingerprints {
		el, err := PinnedKey(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if got := fingerprint(el[0].PrimaryKey); got != fp {
			t.Fatalf("%s: fingerprint %s, want %s", src, got, fp)
		}
	}
	if _, err := PinnedKey(Debian); err == nil {
		t.Fatal("Debian has no key")
	}
	// One key per source (D-347): no other key file exists.
	ents, err := keyFiles.ReadDir("keys")
	if err != nil || len(ents) != len(Fingerprints) {
		t.Fatalf("key files: %d, want %d (%v)", len(ents), len(Fingerprints), err)
	}
}

// The real lists and signatures of the vendors, fetched on 2026-11-13.
func TestRealUbuntuList(t *testing.T) {
	keys, err := PinnedKey(Ubuntu)
	if err != nil {
		t.Fatal(err)
	}
	list := read(t, "ubuntu-SHA256SUMS")
	res, err := Verify(keys, list, read(t, "ubuntu-SHA256SUMS.gpg"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Fingerprint != Fingerprints[Ubuntu] || res.SignedAt.IsZero() || res.SignedAt.After(testNow) {
		t.Fatalf("result %+v", res)
	}
	sum, err := ParseSums(list, "ubuntu-26.04-server-cloudimg-amd64.img", 64)
	if err != nil || sum != "5645d0318ad21f643ebb1ac4567514620e9893380b07b0201fda163baa5b7eb5" {
		t.Fatalf("sum %q %v", sum, err)
	}
	// One changed byte of the list breaks the signature.
	bad := bytes.Replace(list, []byte("5645d0"), []byte("5645d1"), 1)
	if _, err := Verify(keys, bad, read(t, "ubuntu-SHA256SUMS.gpg"), testNow); !errors.Is(err, ErrSignature) {
		t.Fatalf("changed list: %v", err)
	}
	// The Proxmox key does not verify the Ubuntu list.
	pk, _ := PinnedKey(Proxmox)
	if _, err := Verify(pk, list, read(t, "ubuntu-SHA256SUMS.gpg"), testNow); !errors.Is(err, ErrSignature) {
		t.Fatalf("other key: %v", err)
	}
}

func TestRealProxmoxIndex(t *testing.T) {
	keys, err := PinnedKey(Proxmox)
	if err != nil {
		t.Fatal(err)
	}
	index, err := gunzip(read(t, "aplinfo-pve-9.dat.gz"), MaxIndex)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Verify(keys, index, read(t, "aplinfo-pve-9.dat.asc"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Fingerprint != Fingerprints[Proxmox] {
		t.Fatalf("result %+v", res)
	}
	// The signature is over the unpacked index, not over the .gz file.
	if _, err := Verify(keys, read(t, "aplinfo-pve-9.dat.gz"), read(t, "aplinfo-pve-9.dat.asc"), testNow); !errors.Is(err, ErrSignature) {
		t.Fatalf("gz bytes: %v", err)
	}
	sum, err := ParseAplinfo(index, "system/debian-13-standard_13.6-1_amd64.tar.zst")
	want := "4c0c27ca6ceab5ef0b84db57825a00f26157ef1854bafe97297813e1cbe8ecb8cc9c453cab6b3b0efe1ba193a50c47ece1e41d950e411b8730b835b71e9e754b"
	if err != nil || sum != want {
		t.Fatalf("sum %q %v", sum, err)
	}
}

func TestCheckURL(t *testing.T) {
	for _, c := range []struct {
		src Source
		url string
		ok  bool
	}{
		{Ubuntu, "https://cloud-images.ubuntu.com/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64.img", true},
		{Ubuntu, "http://cloud-images.ubuntu.com/releases/26.04/release/x.img", false},
		{Ubuntu, "https://cloud-images.ubuntu.com.example/x.img", false},
		{Ubuntu, "https://cloud-images.ubuntu.com/x.img?a=b", false},
		{Debian, "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2", true},
		{Debian, "https://cloud.debian.org/other/debian.qcow2", false},
		{Proxmox, "http://download.proxmox.com/images/system/debian-13-standard_13.6-1_amd64.tar.zst", true},
		{Proxmox, "http://download.proxmox.com/images/system/sub/x.tar.zst", false},
		{Proxmox, "http://user@download.proxmox.com/images/system/x.tar.zst", false},
		{Source("other"), "https://cloud-images.ubuntu.com/x.img", false},
	} {
		if err := CheckURL(c.src, c.url); (err == nil) != c.ok {
			t.Errorf("%s %s: %v", c.src, c.url, err)
		}
	}
}

// testKey makes a signing key; sign signs data with it at time at.
func testKey(t *testing.T) *openpgp.Entity {
	t.Helper()
	e, err := openpgp.NewEntity("test", "", "test@example.invalid", &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA, Time: func() time.Time { return testNow.Add(-48 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func sign(t *testing.T, e *openpgp.Entity, data []byte, at time.Time) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&buf, e, bytes.NewReader(data), &packet.Config{Time: func() time.Time { return at }}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type fakeVendor struct {
	files map[string][]byte
}

func (v *fakeVendor) client(t *testing.T, keys map[Source]openpgp.EntityList) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := v.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return &Client{
		HTTP: srv.Client(), Now: func() time.Time { return testNow }, keys: keys,
		rewrite: func(u string) string {
			i := strings.Index(u, "://")
			rest := u[i+3:]
			return srv.URL + rest[strings.Index(rest, "/"):]
		},
	}
}

func TestResolveUbuntu(t *testing.T) {
	key := testKey(t)
	list := []byte(strings.Repeat("a", 64) + " *other.img\n" + strings.Repeat("b", 64) + " *ubuntu.img\n")
	img := "https://cloud-images.ubuntu.com/releases/26.04/release/ubuntu.img"
	v := &fakeVendor{files: map[string][]byte{
		"/releases/26.04/release/SHA256SUMS":     list,
		"/releases/26.04/release/SHA256SUMS.gpg": sign(t, key, list, testNow.Add(-time.Hour)),
	}}
	c := v.client(t, map[Source]openpgp.EntityList{Ubuntu: {key}})
	res, err := c.Resolve(context.Background(), Ubuntu, img)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Signed || res.Sum != strings.Repeat("b", 64) || res.Algorithm != "sha256" || res.File != "ubuntu.img" ||
		!res.SignedAt.Equal(testNow.Add(-time.Hour).Truncate(time.Second)) {
		t.Fatalf("result %+v", res)
	}

	// Signed by another key: refused.
	other := testKey(t)
	v.files["/releases/26.04/release/SHA256SUMS.gpg"] = sign(t, other, list, testNow.Add(-time.Hour))
	if _, err := c.Resolve(context.Background(), Ubuntu, img); !errors.Is(err, ErrSignature) {
		t.Fatalf("other key: %v", err)
	}
	// A signature one minute in the future: refused by the library (no
	// clock skew is allowed).
	v.files["/releases/26.04/release/SHA256SUMS.gpg"] = sign(t, key, list, testNow.Add(time.Minute))
	if _, err := c.Resolve(context.Background(), Ubuntu, img); !errors.Is(err, ErrSignature) {
		t.Fatalf("one minute in the future: %v", err)
	}
	// A signature from the future: refused.
	v.files["/releases/26.04/release/SHA256SUMS.gpg"] = sign(t, key, list, testNow.Add(48*time.Hour))
	if _, err := c.Resolve(context.Background(), Ubuntu, img); !errors.Is(err, ErrSignature) {
		t.Fatalf("future: %v", err)
	}
	// Two entries for the file: refused.
	dup := append(append([]byte{}, list...), []byte(strings.Repeat("c", 64)+" *ubuntu.img\n")...)
	v.files["/releases/26.04/release/SHA256SUMS"] = dup
	v.files["/releases/26.04/release/SHA256SUMS.gpg"] = sign(t, key, dup, testNow.Add(-time.Hour))
	if _, err := c.Resolve(context.Background(), Ubuntu, img); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	// Too large: refused before any parse.
	big := bytes.Repeat([]byte("x"), MaxList+1)
	v.files["/releases/26.04/release/SHA256SUMS"] = big
	if _, err := c.Resolve(context.Background(), Ubuntu, img); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	// Missing signature file.
	v.files["/releases/26.04/release/SHA256SUMS"] = list
	delete(v.files, "/releases/26.04/release/SHA256SUMS.gpg")
	if _, err := c.Resolve(context.Background(), Ubuntu, img); !errors.Is(err, ErrFetch) {
		t.Fatalf("no signature: %v", err)
	}
}

func TestResolveProxmox(t *testing.T) {
	key := testKey(t)
	sum := strings.Repeat("d", 128)
	index := []byte("Package: a\nLocation: system/other.tar.zst\nsha512sum: " + strings.Repeat("e", 128) + "\n\n" +
		"Package: b\nDescription: x\n  more\nLocation: system/debian.tar.zst\nsha512sum: " + sum + "\n")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(index)
	_ = zw.Close()
	v := &fakeVendor{files: map[string][]byte{
		"/images/aplinfo-pve-9.dat.gz":  gz.Bytes(),
		"/images/aplinfo-pve-9.dat.asc": sign(t, key, index, testNow.Add(-time.Hour)),
	}}
	c := v.client(t, map[Source]openpgp.EntityList{Proxmox: {key}})
	res, err := c.Resolve(context.Background(), Proxmox, "http://download.proxmox.com/images/system/debian.tar.zst")
	if err != nil {
		t.Fatal(err)
	}
	if res.Sum != sum || res.Algorithm != "sha512" || res.File != "system/debian.tar.zst" {
		t.Fatalf("result %+v", res)
	}
	// A signature over the .gz bytes (not the unpacked index) is refused.
	v.files["/images/aplinfo-pve-9.dat.asc"] = sign(t, key, gz.Bytes(), testNow.Add(-time.Hour))
	if _, err := c.Resolve(context.Background(), Proxmox, "http://download.proxmox.com/images/system/debian.tar.zst"); !errors.Is(err, ErrSignature) {
		t.Fatalf("gz signature: %v", err)
	}
}

func TestResolveDebianUnsigned(t *testing.T) {
	list := []byte(strings.Repeat("f", 128) + "  debian-13-genericcloud-amd64.qcow2\n")
	v := &fakeVendor{files: map[string][]byte{"/images/cloud/trixie/latest/SHA512SUMS": list}}
	c := v.client(t, nil)
	res, err := c.Resolve(context.Background(), Debian, "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2")
	if err != nil {
		t.Fatal(err)
	}
	if res.Signed || res.Fingerprint != "" || !res.SignedAt.IsZero() || res.Algorithm != "sha512" || res.Sum != strings.Repeat("f", 128) {
		t.Fatalf("result %+v", res)
	}
}

func TestParseSums(t *testing.T) {
	h := strings.Repeat("a", 64)
	for _, c := range []struct {
		list string
		want error
	}{
		{h + "  x.img\n", nil},
		{h + " *x.img\r\n", nil},
		{h + " x.img\n", ErrFormat}, // one space, no mode char
		{"zz" + h[2:] + "  x.img\n", ErrFormat},
		{h[:63] + "  x.img\n", ErrFormat},
		{h + "  y.img\n", ErrNoEntry},
		{h + "  x.img\n" + h + " *x.img\n", ErrDuplicate},
		{h + "  x.img\x00\n", ErrFormat},
		{h + "  x.img.sig\n", ErrNoEntry}, // exact name only
	} {
		_, err := ParseSums([]byte(c.list), "x.img", 64)
		if !errors.Is(err, c.want) && !(err == nil && c.want == nil) {
			t.Errorf("%q: %v, want %v", c.list, err, c.want)
		}
	}
}

func TestParseAplinfo(t *testing.T) {
	s := strings.Repeat("a", 128)
	for _, c := range []struct {
		index string
		want  error
	}{
		{"Location: system/x\nsha512sum: " + s + "\n", nil},
		{"Location: system/y\nsha512sum: " + s + "\n", ErrNoEntry},
		{"Location: system/x\nsha512sum: " + s + "\n\nLocation: system/x\nsha512sum: " + s + "\n", ErrDuplicate},
		{"Location: system/x\nsha512sum: " + s + "\nsha512sum: " + s + "\n", ErrFormat},
		{"Location: system/x\nsha512sum: " + s[:127] + "\n", ErrFormat},
		{"Location: system/x\n", ErrFormat},
		// A description line cannot fake a field.
		{"Location: system/x\nsha512sum: " + s + "\nDescription: d\n sha512sum: " + strings.Repeat("b", 128) + "\n", nil},
	} {
		_, err := ParseAplinfo([]byte(c.index), "system/x")
		if !errors.Is(err, c.want) && !(err == nil && c.want == nil) {
			t.Errorf("%q: %v, want %v", c.index, err, c.want)
		}
	}
}

func FuzzParseSums(f *testing.F) {
	f.Add([]byte(strings.Repeat("a", 64)+"  x.img\n"), "x.img")
	f.Add([]byte(strings.Repeat("a", 128)+" *x.img\n"), "x.img")
	f.Fuzz(func(t *testing.T, list []byte, file string) {
		for _, n := range []int{64, 128} {
			sum, err := ParseSums(list, file, n)
			if err == nil && (len(sum) != n || !isHex(sum) || sum != strings.ToLower(sum)) {
				t.Fatalf("bad sum %q", sum)
			}
		}
	})
}

func FuzzParseAplinfo(f *testing.F) {
	f.Add([]byte("Location: system/x\nsha512sum: "+strings.Repeat("a", 128)+"\n"), "system/x")
	f.Fuzz(func(t *testing.T, index []byte, loc string) {
		sum, err := ParseAplinfo(index, loc)
		if err == nil && (len(sum) != 128 || !isHex(sum)) {
			t.Fatalf("bad sum %q", sum)
		}
	})
}

func FuzzVerify(f *testing.F) {
	ubu, _ := os.ReadFile("testdata/ubuntu-SHA256SUMS.gpg")
	f.Add([]byte("data"), ubu)
	f.Add([]byte("data"), []byte("-----BEGIN PGP SIGNATURE-----\n\n-----END PGP SIGNATURE-----\n"))
	keys, err := PinnedKey(Ubuntu)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data, sig []byte) {
		if _, err := Verify(keys, data, sig, testNow); err == nil {
			t.Fatal("a fuzzed signature verified")
		}
	})
}

// Review R1 of #247: a redirect to another host is refused; one on the
// same host is followed.
func TestFetchRedirects(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("foreign"))
	}))
	t.Cleanup(other.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, other.URL+"/list", http.StatusFound)
		case "/here":
			http.Redirect(w, r, "/list", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/list":
			_, _ = w.Write([]byte("list"))
		}
	}))
	t.Cleanup(srv.Close)
	c := &Client{HTTP: srv.Client()}
	if _, err := c.fetch(context.Background(), srv.URL+"/away", MaxList); !errors.Is(err, ErrFetch) {
		t.Fatalf("redirect to another host: %v", err)
	}
	if _, err := c.fetch(context.Background(), srv.URL+"/loop", MaxList); !errors.Is(err, ErrFetch) {
		t.Fatalf("redirect loop: %v", err)
	}
	b, err := c.fetch(context.Background(), srv.URL+"/here", MaxList)
	if err != nil || string(b) != "list" {
		t.Fatalf("same-host redirect: %q %v", b, err)
	}
	// The caller's client keeps its own redirect rule.
	if srv.Client().CheckRedirect != nil {
		t.Fatal("the caller's client was changed")
	}
}
