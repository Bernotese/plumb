// Package trust finds the newest checksum of a base image from the list
// that its vendor publishes, and checks the vendor signature of that list
// with a key pinned in this binary (D-335, D-336, D-344, D-347).
//
// Only the signed bytes are parsed. The file name of the image URL must
// match exactly one entry of the list. No caller can add a key: the keys
// are files of this package, and a new key comes with a reviewed release.
package trust

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// Source names where the checksum of a base image comes from (the enum
// verify.source of internal/state/schema/image.json).
type Source string

const (
	// Ubuntu: SHA256SUMS + SHA256SUMS.gpg in the directory of the image.
	Ubuntu Source = "ubuntu-cloud"
	// Proxmox: the appliance index aplinfo-pve-9.dat.gz, its detached
	// signature aplinfo-pve-9.dat.asc over the unpacked bytes.
	Proxmox Source = "proxmox-aplinfo"
	// Debian: SHA512SUMS in the directory of the image, over HTTPS. Debian
	// signs no list for its cloud images, so a refresh needs a person.
	Debian Source = "debian-https"
)

// Limits (D-336).
const (
	MaxList      = 1 << 20 // a SHA*SUMS file
	MaxIndexGz   = 2 << 20 // aplinfo-pve-9.dat.gz as downloaded
	MaxIndex     = 8 << 20 // aplinfo-pve-9.dat unpacked
	MaxSignature = 64 << 10
	Timeout      = 30 * time.Second
)

// ProxmoxIndexURL is the one appliance index that Plumb reads.
const ProxmoxIndexURL = "http://download.proxmox.com/images/aplinfo-pve-9.dat.gz"

// Result is a checked pin.
type Result struct {
	Source Source
	// Signed is false only for Debian.
	Signed bool
	// Fingerprint is the pinned key that signed the list (upper-case hex,
	// 40 digits); empty when Signed is false.
	Fingerprint string
	// SignedAt is the creation time of the signature; zero when Signed is
	// false.
	SignedAt time.Time
	// File is the name in the list that matched the image URL.
	File string
	// Algorithm is "sha256" or "sha512".
	Algorithm string
	// Sum is the lower-case hex checksum.
	Sum string
}

// Errors. The text of an error names the step; it never holds fetched data.
var (
	ErrSource    = errors.New("trust: the source does not fit the image URL")
	ErrFetch     = errors.New("trust: fetch failed")
	ErrTooLarge  = errors.New("trust: the file is larger than its limit")
	ErrSignature = errors.New("trust: the signature is not valid for the pinned key")
	ErrNoEntry   = errors.New("trust: the list has no entry for the file")
	ErrDuplicate = errors.New("trust: the list has more than one entry for the file")
	ErrFormat    = errors.New("trust: the list is malformed")
)

//go:embed keys/*.asc
var keyFiles embed.FS

// Pinned fingerprints, one key per source (D-347). A test checks that the
// key files hold exactly these keys.
var Fingerprints = map[Source]string{
	Ubuntu:  "D2EB44626FDDC30B513D5BB71A5D6C4C7DB87C81",
	Proxmox: "24B30F06ECC1836A4E5EFECBA7BCD1420BFE778E",
}

// Client fetches and checks lists.
type Client struct {
	// HTTP is the client for the downloads; nil means a client with
	// Timeout.
	HTTP *http.Client
	// Now is the clock of the signature check; nil means time.Now.
	Now func() time.Time

	keys    map[Source]openpgp.EntityList // nil: the pinned keys
	rewrite func(string) string           // tests: points URLs at a test server
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: Timeout}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Keyring returns the pinned key of a source.
func (c *Client) keyring(src Source) (openpgp.EntityList, error) {
	if c.keys != nil {
		return c.keys[src], nil
	}
	return PinnedKey(src)
}

// PinnedKey reads the key file of a source and checks its fingerprint.
func PinnedKey(src Source) (openpgp.EntityList, error) {
	want, ok := Fingerprints[src]
	if !ok {
		return nil, fmt.Errorf("trust: no pinned key for %s", src)
	}
	b, err := keyFiles.ReadFile("keys/" + string(src) + ".asc")
	if err != nil {
		return nil, err
	}
	el, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("trust: key of %s: %w", src, err)
	}
	if len(el) != 1 || fingerprint(el[0].PrimaryKey) != want {
		return nil, fmt.Errorf("trust: the key file of %s is not the pinned key %s", src, want)
	}
	return append(el, testPinnedKeys(src)...), nil
}

func fingerprint(k *packet.PublicKey) string {
	return strings.ToUpper(fmt.Sprintf("%x", k.Fingerprint))
}

// CheckURL reports whether the source fits the image URL (D-344).
func CheckURL(src Source, imageURL string) error {
	u, err := url.Parse(imageURL)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || path.Base(u.Path) == "/" || path.Base(u.Path) == "." {
		return ErrSource
	}
	switch src {
	case Ubuntu:
		if u.Scheme == "https" && u.Host == "cloud-images.ubuntu.com" {
			return nil
		}
	case Debian:
		if u.Scheme == "https" && u.Host == "cloud.debian.org" && strings.HasPrefix(u.Path, "/images/cloud/") {
			return nil
		}
	case Proxmox:
		if (u.Scheme == "http" || u.Scheme == "https") && u.Host == "download.proxmox.com" &&
			strings.HasPrefix(u.Path, "/images/system/") && strings.Count(u.Path, "/") == 3 {
			return nil
		}
	}
	return ErrSource
}

// Resolve fetches the list of the source, checks it and returns the pin of
// the image URL.
func (c *Client) Resolve(ctx context.Context, src Source, imageURL string) (Result, error) {
	if err := CheckURL(src, imageURL); err != nil {
		return Result{}, err
	}
	u, _ := url.Parse(imageURL)
	dir := *u
	dir.Path = path.Dir(u.Path) + "/"
	file := path.Base(u.Path)
	switch src {
	case Ubuntu:
		list, err := c.fetch(ctx, dir.String()+"SHA256SUMS", MaxList)
		if err != nil {
			return Result{}, err
		}
		sig, err := c.fetch(ctx, dir.String()+"SHA256SUMS.gpg", MaxSignature)
		if err != nil {
			return Result{}, err
		}
		res, err := c.verify(src, list, sig)
		if err != nil {
			return Result{}, err
		}
		sum, err := ParseSums(list, file, 64)
		if err != nil {
			return Result{}, err
		}
		res.File, res.Algorithm, res.Sum = file, "sha256", sum
		return res, nil
	case Debian:
		list, err := c.fetch(ctx, dir.String()+"SHA512SUMS", MaxList)
		if err != nil {
			return Result{}, err
		}
		sum, err := ParseSums(list, file, 128)
		if err != nil {
			return Result{}, err
		}
		return Result{Source: src, File: file, Algorithm: "sha512", Sum: sum}, nil
	case Proxmox:
		gz, err := c.fetch(ctx, ProxmoxIndexURL, MaxIndexGz)
		if err != nil {
			return Result{}, err
		}
		index, err := gunzip(gz, MaxIndex)
		if err != nil {
			return Result{}, err
		}
		sig, err := c.fetch(ctx, strings.TrimSuffix(ProxmoxIndexURL, ".gz")+".asc", MaxSignature)
		if err != nil {
			return Result{}, err
		}
		res, err := c.verify(src, index, sig)
		if err != nil {
			return Result{}, err
		}
		location := strings.TrimPrefix(u.Path, "/images/")
		sum, err := ParseAplinfo(index, location)
		if err != nil {
			return Result{}, err
		}
		res.File, res.Algorithm, res.Sum = location, "sha512", sum
		return res, nil
	}
	return Result{}, ErrSource
}

// fetch downloads at most limit bytes; a longer body is ErrTooLarge.
func (c *Client) fetch(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if c.rewrite != nil {
		rawURL = c.rewrite(rawURL)
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrFetch, path.Base(rawURL))
	}
	hc := *c.http()
	hc.CheckRedirect = sameOrigin
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrFetch, path.Base(rawURL))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s: HTTP %d", ErrFetch, path.Base(rawURL), resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrFetch, path.Base(rawURL))
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%w: %s", ErrTooLarge, path.Base(rawURL))
	}
	return b, nil
}

// errRedirect refuses a redirect to another scheme or host (review R1 of
// #247): a list must come from the vendor's own server, so a redirect may
// change the path only.
var errRedirect = errors.New("trust: redirect to another scheme or host refused")

// sameOrigin is the redirect rule of every fetch: at most 5 redirects, each
// on the scheme and host of the first request.
func sameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errRedirect
	}
	first := via[0].URL
	if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
		return errRedirect
	}
	return nil
}

func gunzip(b []byte, limit int64) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%w: index is not gzip", ErrFormat)
	}
	out, err := io.ReadAll(io.LimitReader(zr, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: index is not gzip", ErrFormat)
	}
	if int64(len(out)) > limit {
		return nil, fmt.Errorf("%w: unpacked index", ErrTooLarge)
	}
	return out, nil
}

// verify checks a detached signature (armored or binary) over data with the
// pinned key of src.
func (c *Client) verify(src Source, data, sig []byte) (Result, error) {
	keyring, err := c.keyring(src)
	if err != nil {
		return Result{}, err
	}
	return Verify(keyring, data, sig, c.now())
}

// Verify checks a detached signature over data with keyring at the time
// now. It returns the signer fingerprint and the signature time.
func Verify(keyring openpgp.EntityList, data, sig []byte, now time.Time) (Result, error) {
	if len(sig) > MaxSignature {
		return Result{}, ErrTooLarge
	}
	r := io.Reader(bytes.NewReader(sig))
	if bytes.HasPrefix(bytes.TrimSpace(sig), []byte("-----BEGIN PGP SIGNATURE-----")) {
		block, err := armor.Decode(bytes.NewReader(sig))
		if err != nil || block.Type != "PGP SIGNATURE" {
			return Result{}, ErrSignature
		}
		r = block.Body
	}
	cfg := &packet.Config{Time: func() time.Time { return now }}
	s, signer, err := openpgp.VerifyDetachedSignatureAndHash(keyring, bytes.NewReader(data), r,
		[]crypto.Hash{crypto.SHA256, crypto.SHA384, crypto.SHA512}, cfg)
	if err != nil || s == nil || signer == nil {
		return Result{}, ErrSignature
	}
	// A signature time after now is refused by the library itself
	// (packet.Signature.SigExpired: any CreationTime > now; no clock skew
	// is allowed). A controller clock behind the vendor's therefore refuses
	// a just-published list until the next try (ARCHITECTURE §17).
	return Result{Signed: true, Fingerprint: fingerprint(signer.PrimaryKey), SignedAt: s.CreationTime.UTC()}, nil
}

// ParseSums finds the one entry for file in a SHA*SUMS list ("<hex>  <name>"
// or "<hex> *<name>"). hexLen is 64 or 128.
func ParseSums(list []byte, file string, hexLen int) (string, error) {
	if !utf8Clean(list) {
		return "", ErrFormat
	}
	var found string
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(list))
	sc.Buffer(make([]byte, 4096), MaxList)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, " ")
		if !ok || len(sum) != hexLen || !isHex(sum) {
			return "", ErrFormat
		}
		switch {
		case strings.HasPrefix(name, " "), strings.HasPrefix(name, "*"):
			name = name[1:]
		default:
			return "", ErrFormat
		}
		if name == file {
			n++
			found = sum
		}
	}
	if sc.Err() != nil {
		return "", ErrFormat
	}
	switch n {
	case 0:
		return "", ErrNoEntry
	case 1:
		return strings.ToLower(found), nil
	}
	return "", ErrDuplicate
}

// ParseAplinfo finds the one record with "Location: <location>" in a
// Proxmox appliance index and returns its sha512sum.
func ParseAplinfo(index []byte, location string) (string, error) {
	if !utf8Clean(index) {
		return "", ErrFormat
	}
	var found string
	n := 0
	for _, rec := range strings.Split(strings.ReplaceAll(string(index), "\r\n", "\n"), "\n\n") {
		var loc, sum string
		locs, sums := 0, 0
		for _, line := range strings.Split(rec, "\n") {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue // a continuation line of a description
			}
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			switch k {
			case "Location":
				loc, locs = v, locs+1
			case "sha512sum":
				sum, sums = v, sums+1
			}
		}
		if loc != location {
			continue
		}
		if locs != 1 || sums != 1 || len(sum) != 128 || !isHex(sum) {
			return "", ErrFormat
		}
		n++
		found = sum
	}
	switch n {
	case 0:
		return "", ErrNoEntry
	case 1:
		return strings.ToLower(found), nil
	}
	return "", ErrDuplicate
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// utf8Clean refuses NUL bytes (a list is text).
func utf8Clean(b []byte) bool { return !bytes.Contains(b, []byte{0}) }
