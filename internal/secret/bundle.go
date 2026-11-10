package secret

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"golang.org/x/crypto/argon2"
)

// The secret bundle of an export file (D-094): all secret values as one
// AES-256-GCM ciphertext under a key that argon2id derives from a
// passphrase. Format 1 has fixed argon2id parameters; Open refuses any
// other before it does any argon2 work, so a crafted file cannot choose
// the memory use of the server.

// KDF names the key derivation of a bundle.
type KDF struct {
	Name      string `json:"name" yaml:"name"`
	Time      uint32 `json:"time" yaml:"time"`
	MemoryKiB uint32 `json:"memory_kib" yaml:"memory_kib"`
	Threads   uint8  `json:"threads" yaml:"threads"`
}

// BundleKDF is the only KDF of format 1.
var BundleKDF = KDF{Name: "argon2id", Time: 3, MemoryKiB: 64 << 10, Threads: 4}

// Bundle is the SecretBundle document of an export file.
type Bundle struct {
	Kind  string `json:"kind" yaml:"kind"`
	KDF   KDF    `json:"kdf" yaml:"kdf"`
	Salt  string `json:"salt" yaml:"salt"`   // base64, 16 bytes
	Nonce string `json:"nonce" yaml:"nonce"` // base64, 12 bytes
	Data  string `json:"data" yaml:"data"`   // base64 ciphertext of a JSON object name → value
}

// bundleAD is the additional data of every bundle.
const bundleAD = "plumb-secret-bundle/v1"

// Errors of a bundle. None holds a value or the passphrase.
var (
	ErrBundleFormat     = errors.New("secret: the SecretBundle is not valid (kind, kdf, salt, nonce or data)")
	ErrBundleKDF        = errors.New("secret: the SecretBundle has other key derivation parameters than format 1 (argon2id, time 3, 64 MiB, 4 threads)")
	ErrBundlePassphrase = errors.New("secret: the passphrase does not open the SecretBundle, or the bundle was changed")
)

func bundleKey(passphrase string, salt []byte, k KDF) []byte {
	return argon2.IDKey([]byte(passphrase), salt, k.Time, k.MemoryKiB, k.Threads, 32)
}

// SealBundle encrypts values under the passphrase. kdf is BundleKDF;
// tests pass a cheaper one and then cannot open the bundle with Open.
func SealBundle(values map[string]string, passphrase string, kdf KDF) (Bundle, error) {
	plain, err := json.Marshal(values)
	if err != nil {
		return Bundle{}, err
	}
	defer clear(plain)
	salt, nonce := make([]byte, 16), make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return Bundle{}, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return Bundle{}, err
	}
	key := bundleKey(passphrase, salt, kdf)
	defer clear(key)
	aead, err := gcm(key)
	if err != nil {
		return Bundle{}, err
	}
	b64 := base64.StdEncoding
	return Bundle{Kind: "SecretBundle", KDF: kdf, Salt: b64.EncodeToString(salt), Nonce: b64.EncodeToString(nonce),
		Data: b64.EncodeToString(aead.Seal(nil, nonce, plain, []byte(bundleAD)))}, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ParseBundle reads a SecretBundle document (as JSON) and checks its form
// and its KDF. It does no argon2 work.
func ParseBundle(raw []byte) (Bundle, error) {
	var b Bundle
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil || b.Kind != "SecretBundle" {
		return Bundle{}, ErrBundleFormat
	}
	if b.KDF != BundleKDF {
		return Bundle{}, ErrBundleKDF
	}
	b64 := base64.StdEncoding
	salt, err1 := b64.DecodeString(b.Salt)
	nonce, err2 := b64.DecodeString(b.Nonce)
	data, err3 := b64.DecodeString(b.Data)
	if err1 != nil || err2 != nil || err3 != nil || len(salt) != 16 || len(nonce) != 12 || len(data) < 16 {
		return Bundle{}, ErrBundleFormat
	}
	return b, nil
}

// OpenBundle decrypts a bundle that ParseBundle accepted. It runs argon2id
// with the fixed parameters; the caller runs it in an argon2 slot (D-070).
// It refuses values that are not valid secret names or values.
func OpenBundle(b Bundle, passphrase string) (map[string]string, error) {
	if b.KDF != BundleKDF {
		return nil, ErrBundleKDF
	}
	return openBundle(b, passphrase)
}

func openBundle(b Bundle, passphrase string) (map[string]string, error) {
	b64 := base64.StdEncoding
	salt, _ := b64.DecodeString(b.Salt)
	nonce, _ := b64.DecodeString(b.Nonce)
	data, _ := b64.DecodeString(b.Data)
	key := bundleKey(passphrase, salt, b.KDF)
	defer clear(key)
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, ErrBundleFormat
	}
	plain, err := aead.Open(nil, nonce, data, []byte(bundleAD))
	if err != nil {
		return nil, ErrBundlePassphrase
	}
	defer clear(plain)
	var values map[string]string
	if err := json.Unmarshal(plain, &values); err != nil {
		return nil, ErrBundleFormat
	}
	for name, v := range values {
		if !ValidName(name) || !ValidValue(v) {
			return nil, fmt.Errorf("%w: a name or value in the bundle is not valid", ErrBundleFormat)
		}
	}
	return values, nil
}

// All returns the clear values of all secrets, for the bundle of
// POST /export. Never log or return the result.
func (s *Store) All(ctx context.Context) (map[string]string, error) {
	metas, err := s.st.SecretMetas(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(metas))
	for i, m := range metas {
		names[i] = m.Name
	}
	sort.Strings(names)
	vals, err := s.Resolve(ctx, names)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(vals))
	for n, v := range vals {
		out[n] = v.Text
	}
	return out, nil
}
