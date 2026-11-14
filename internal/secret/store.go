package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/plumb/internal/store"
)

// Limits (D-087).
const (
	MaxValueBytes = 65536
	nonceSize     = 12
)

var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$`)

// Errors. None of them holds a value.
var (
	ErrWrongKey     = errors.New("secret: the master key does not match this database (wrong key file or wrong recovery key)")
	ErrInvalidName  = errors.New("secret: a name is 1 … 63 characters: lowercase letters, digits, '-' and '.', starting and ending with a letter or digit")
	ErrInvalidValue = errors.New("secret: a value is UTF-8 text of 1 … 65536 bytes")
	// ErrCorrupt means a row does not open: changed ciphertext, nonce,
	// name or version, or a row that was moved or replayed.
	ErrCorrupt = errors.New("secret: a stored secret does not open (changed or moved row)")
)

// Usage is who uses one secret: targets of the head revision and pending
// Plans.
type Usage struct {
	Targets []string
	Plans   []int64
}

// UsageFunc returns the usage of every secret that is in use. A name
// without an entry is not in use. T-029 provides it; nil means no secret
// is in use.
type UsageFunc func(ctx context.Context) (map[string]Usage, error)

// InUseError is returned by Delete for a secret that is in use.
type InUseError struct {
	Name string
	Usage
}

func (e *InUseError) Error() string {
	return fmt.Sprintf("secret %q is used by %d target(s) and %d pending Plan(s)", e.Name, len(e.Targets), len(e.Plans))
}

// MissingError is returned by Resolve when names do not exist.
type MissingError struct{ Names []string }

func (e *MissingError) Error() string {
	return "secret: missing secrets: " + strings.Join(e.Names, ", ")
}

// Info is the metadata of a secret. There is no type with a value besides
// Value, which only Resolve returns.
type Info struct {
	Name      string
	Version   int64
	UpdatedBy string
	UpdatedAt time.Time
	UsedBy    []string // targets
}

// Value is a clear secret value. Only Resolve returns it; only plan uses it.
type Value struct {
	Version int64
	Text    string
}

// Store is the encrypted secret store.
type Store struct {
	st    *store.Store
	aead  cipher.AEAD
	usage UsageFunc
	guest []byte // HMAC key of the copy rule (guestvalue.go)
}

// Open checks the master key against the database and returns the store.
// On the first start with a key it stores the key check value. A wrong
// key returns ErrWrongKey.
func Open(ctx context.Context, st *store.Store, k Key) (*Store, error) {
	check, err := st.KeyCheck(ctx)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// No key check yet: a database with secrets would have one.
		n, err := st.CountSecrets(ctx)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, fmt.Errorf("secret: the database holds %d secrets but no key check value", n)
		}
		if err := st.SetKeyCheck(ctx, k.CheckValue()); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case !k.Matches(check):
		return nil, ErrWrongKey
	}
	// Go keeps no reference to the key slice after NewCipher (it expands
	// the key), so the derived bytes are cleared at once. Strings and the
	// expanded key schedule cannot be cleared.
	sk := k.derive(PurposeSecretStore)
	block, err := aes.NewCipher(sk)
	clear(sk)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Store{st: st, aead: aead, guest: k.derive(PurposeGuestValue)}, nil
}

// SetUsage sets the usage hook (T-029). Call it before the store is used.
func (s *Store) SetUsage(f UsageFunc) { s.usage = f }

// ValidName reports whether name is a valid secret name.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// ValidValue reports whether v is a valid secret value.
func ValidValue(v string) bool {
	return len(v) >= 1 && len(v) <= MaxValueBytes && utf8.ValidString(v)
}

// additionalData binds a ciphertext to its purpose, name and version, each
// with a 4-byte length prefix (D-087). Two different (name, version) pairs
// never give the same bytes.
func additionalData(name string, version int64) []byte {
	var ver [8]byte
	binary.BigEndian.PutUint64(ver[:], uint64(version))
	var out []byte
	for _, part := range [][]byte{[]byte(PurposeSecretStore), []byte(name), ver[:]} {
		out = binary.BigEndian.AppendUint32(out, uint32(len(part)))
		out = append(out, part...)
	}
	return out
}

func (s *Store) seal(name, value string) store.SealFunc {
	return func(version int64) ([]byte, []byte, error) {
		nonce := make([]byte, nonceSize)
		if _, err := rand.Read(nonce); err != nil {
			return nil, nil, err
		}
		plain := []byte(value)
		defer clear(plain)
		return nonce, s.aead.Seal(nil, nonce, plain, additionalData(name, version)), nil
	}
}

func (s *Store) open(r store.SecretRow) (string, error) {
	if len(r.Nonce) != nonceSize {
		return "", ErrCorrupt
	}
	plain, err := s.aead.Open(nil, r.Nonce, r.Ciphertext, additionalData(r.Name, r.Version))
	if err != nil {
		return "", ErrCorrupt
	}
	defer clear(plain)
	return string(plain), nil
}

// Put creates a secret or replaces its value. by is the principal.
func (s *Store) Put(ctx context.Context, name, value, by string) (Info, error) {
	if !ValidName(name) {
		return Info{}, ErrInvalidName
	}
	if !ValidValue(value) {
		return Info{}, ErrInvalidValue
	}
	// Usage first: a failing hook must not leave a written value behind
	// an error answer.
	usage, err := s.usageOf(ctx)
	if err != nil {
		return Info{}, err
	}
	m, err := s.st.PutSecret(ctx, name, by, s.seal(name, value))
	if err != nil {
		return Info{}, err
	}
	return info(m, usage), nil
}

// Delete removes a secret that is not in use. It returns *InUseError for
// a secret in use and store.ErrNotFound for a missing one.
func (s *Store) Delete(ctx context.Context, name string) error {
	if !ValidName(name) {
		return store.ErrNotFound
	}
	usage, err := s.usageOf(ctx)
	if err != nil {
		return err
	}
	if u, ok := usage[name]; ok && (len(u.Targets) > 0 || len(u.Plans) > 0) {
		// Report in-use only for a secret that exists.
		if rows, err := s.st.SecretRows(ctx, []string{name}); err != nil {
			return err
		} else if len(rows) == 0 {
			return store.ErrNotFound
		}
		return &InUseError{Name: name, Usage: u}
	}
	return s.st.DeleteSecret(ctx, name)
}

// List returns the metadata of all secrets, sorted by name. No value.
func (s *Store) List(ctx context.Context) ([]Info, error) {
	metas, err := s.st.SecretMetas(ctx)
	if err != nil {
		return nil, err
	}
	usage, err := s.usageOf(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Info, 0, len(metas))
	for _, m := range metas {
		out = append(out, info(m, usage))
	}
	return out, nil
}

// Resolve returns the clear values of the named secrets. It returns
// *MissingError when a name does not exist and ErrCorrupt when a row does
// not open. Only plan calls it (T-029); never log or return its result.
// A complete old row put back opens with its old version (accepted, D-130).
func (s *Store) Resolve(ctx context.Context, names []string) (map[string]Value, error) {
	uniq := map[string]bool{}
	for _, n := range names {
		uniq[n] = true
	}
	list := make([]string, 0, len(uniq))
	for n := range uniq {
		list = append(list, n)
	}
	sort.Strings(list)
	rows, err := s.st.SecretRows(ctx, list)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Value, len(rows))
	for _, r := range rows {
		v, err := s.open(r)
		if err != nil {
			return nil, err
		}
		out[r.Name] = Value{Version: r.Version, Text: v}
	}
	var missing []string
	for _, n := range list {
		if _, ok := out[n]; !ok {
			missing = append(missing, n)
		}
	}
	if missing != nil {
		return nil, &MissingError{Names: missing}
	}
	return out, nil
}

func (s *Store) usageOf(ctx context.Context) (map[string]Usage, error) {
	if s.usage == nil {
		return nil, nil
	}
	return s.usage(ctx)
}

func info(m store.SecretMeta, usage map[string]Usage) Info {
	used := append([]string{}, usage[m.Name].Targets...)
	sort.Strings(used)
	return Info{Name: m.Name, Version: m.Version, UpdatedBy: m.UpdatedBy, UpdatedAt: m.UpdatedAt, UsedBy: used}
}

// Versions returns the current version of each named secret that exists.
// It reads no ciphertext.
func (s *Store) Versions(ctx context.Context, names []string) (map[string]int64, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	metas, err := s.st.SecretMetas(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, m := range metas {
		if want[m.Name] {
			out[m.Name] = m.Version
		}
	}
	return out, nil
}
