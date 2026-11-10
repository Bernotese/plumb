package secret

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// KeySize is the size of the master key in bytes (D-086).
const KeySize = 32

// HKDF purposes (D-086). A new use of the master key gets a new purpose.
const (
	PurposeSecretStore = "secret-store/v1"
	PurposeKeyCheck    = "key-check/v1"
)

// Key is the master key. Its bytes leave the package only as a recovery
// key (Recovery) or a key file (WriteKeyFile).
type Key struct{ b [KeySize]byte }

// NewKey returns a new random master key.
func NewKey() Key {
	var k Key
	if _, err := rand.Read(k.b[:]); err != nil {
		panic(err) // no randomness: nothing is safe
	}
	return k
}

// derive returns the 32-byte key of one purpose (HKDF-SHA256, no salt).
func (k Key) derive(purpose string) []byte {
	out, err := hkdf.Key(sha256.New, k.b[:], nil, purpose, 32)
	if err != nil {
		panic(err) // only for a bad length
	}
	return out
}

// CheckValue is the value that the database keeps to detect a wrong key.
// It is a purpose key of its own, so it tells nothing about the others.
func (k Key) CheckValue() []byte { return k.derive(PurposeKeyCheck) }

// Matches reports in constant time whether a stored check value belongs to k.
func (k Key) Matches(check []byte) bool {
	cv := k.CheckValue()
	defer clear(cv)
	return subtle.ConstantTimeCompare(cv, check) == 1
}

// --- recovery key (D-086, D-103) ---

// The recovery key is base32 (RFC 4648, no padding) of the 32 key bytes
// and a 3-byte checksum (the first bytes of SHA-256 of the key): 56
// characters, shown in 14 groups of 4.
const (
	checksumSize = 3
	recoveryLen  = (KeySize + checksumSize) * 8 / 5
)

var recoveryEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// ErrRecoveryKey is returned for a recovery key that is not valid. The
// error never repeats the input.
var ErrRecoveryKey = errors.New("secret: the recovery key is not valid (wrong length, character or checksum)")

func checksum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:checksumSize]
}

// Recovery returns the recovery key in groups of 4 characters.
func (k Key) Recovery() string {
	raw := append(append([]byte{}, k.b[:]...), checksum(k.b[:])...)
	enc := recoveryEncoding.EncodeToString(raw)
	clear(raw)
	var groups []string
	for i := 0; i < len(enc); i += 4 {
		groups = append(groups, enc[i:min(i+4, len(enc))])
	}
	return strings.Join(groups, "-")
}

// ParseRecovery reads a recovery key. Case, spaces and dashes do not
// matter.
func ParseRecovery(s string) (Key, error) {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '-', ' ', '\t', '\r', '\n':
			return -1
		}
		if r >= 'a' && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}, s)
	if len(clean) != recoveryLen {
		return Key{}, ErrRecoveryKey
	}
	raw, err := recoveryEncoding.DecodeString(clean)
	defer clear(raw) // the key bytes; the input string cannot be cleared
	if err != nil || len(raw) != KeySize+checksumSize {
		return Key{}, ErrRecoveryKey
	}
	if subtle.ConstantTimeCompare(checksum(raw[:KeySize]), raw[KeySize:]) != 1 {
		return Key{}, ErrRecoveryKey
	}
	var k Key
	copy(k.b[:], raw[:KeySize])
	return k, nil
}

// --- key file (D-086, D-102) ---

// nofollow: a key file that is a symbolic link is refused (D-102).
const nofollow = syscall.O_NOFOLLOW

// ErrNoKeyFile is returned by LoadKeyFile when the file does not exist.
var ErrNoKeyFile = errors.New("secret: no key file")

// ErrKeyFileExists is returned by WriteKeyFile when the file exists.
var ErrKeyFileExists = errors.New("secret: the key file exists already")

// LoadKeyFile reads a key file: a regular file of exactly 32 bytes that
// group and others cannot access (D-064 rule for key files).
func LoadKeyFile(path string) (Key, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|nofollow, 0)
	if errors.Is(err, os.ErrNotExist) {
		return Key{}, fmt.Errorf("%w: %s", ErrNoKeyFile, path)
	}
	if err != nil {
		return Key{}, fmt.Errorf("secret: open key file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Key{}, fmt.Errorf("secret: key file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Key{}, fmt.Errorf("secret: key file %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Key{}, fmt.Errorf("secret: key file %s has mode %v; it must be 0600", path, info.Mode().Perm())
	}
	if info.Size() != KeySize {
		return Key{}, fmt.Errorf("secret: key file %s has %d bytes, want %d", path, info.Size(), KeySize)
	}
	var k Key
	if _, err := io.ReadFull(f, k.b[:]); err != nil {
		return Key{}, fmt.Errorf("secret: read key file: %w", err)
	}
	return k, nil
}

// WriteKeyFile creates the key file with mode 0600. It never replaces a
// file. The directory must exist. The file and the directory are synced.
func WriteKeyFile(path string, k Key) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|nofollow, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %s", ErrKeyFileExists, path)
	}
	if err != nil {
		return fmt.Errorf("secret: create key file: %w", err)
	}
	defer func() {
		if err != nil {
			os.Remove(path) // a half-written key is worse than none
		}
	}()
	// The umask cannot widen 0600, but a narrower one would make the file
	// unreadable for the server. Set it exactly.
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("secret: key file mode: %w", err)
	}
	if _, err := f.Write(k.b[:]); err != nil {
		f.Close()
		return fmt.Errorf("secret: write key file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("secret: sync key file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("secret: close key file: %w", err)
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
