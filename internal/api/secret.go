package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
	"golang.org/x/crypto/argon2"
)

// PasswordParams are the argon2id parameters for new password hashes.
// Verification reads the parameters from the stored hash.
type PasswordParams struct {
	Time    uint32
	Memory  uint32 // KiB
	Threads uint8
}

// DefaultPasswordParams follow the RFC 9106 second recommendation.
var DefaultPasswordParams = PasswordParams{Time: 3, Memory: 64 << 10, Threads: 4}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // no randomness: nothing is safe
	}
	return b
}

func randomText(n int) string { return base64.RawURLEncoding.EncodeToString(randomBytes(n)) }

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// HashPassword returns an argon2id PHC string.
func HashPassword(password string, p PasswordParams) string {
	salt := randomBytes(16)
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, 32)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key))
}

// VerifyPassword checks a password against a PHC string in constant time.
func VerifyPassword(password, phc string) bool {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return false
	}
	var p PasswordParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 || p.Memory > 1<<20 || p.Time > 16 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// BootstrapAdmin creates the user "admin" with a random password when
// there is no user yet. The password is returned once; the caller prints
// it. The first login must change it.
func BootstrapAdmin(ctx context.Context, st *store.Store, p PasswordParams) (password string, created bool, err error) {
	n, err := st.CountUsers(ctx)
	if err != nil || n > 0 {
		return "", false, err
	}
	password = randomText(18) // 24 characters, 144 bits
	_, err = st.CreateUser(ctx, store.User{Name: "admin", PasswordHash: HashPassword(password, p), MustChangePassword: true})
	if errors.Is(err, store.ErrExists) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return password, true, nil
}

// Token tiers (D-046). admin is the tier of a human session only.
const (
	TierRead  = "read"
	TierPlan  = "plan"
	TierApply = "apply"
	TierAdmin = "admin"
)

var tierLevel = map[string]int{TierRead: 1, TierPlan: 2, TierApply: 3, TierAdmin: 4}

// ErrBadToken is returned for a token request that D-046 does not allow.
var ErrBadToken = errors.New("api: invalid token request")

// CreateToken makes an API token and returns its bearer value, which is
// shown once. Only an apply token has a max_risk, and it is reversible or
// disruptive; no token has admin or destructive (D-046).
func CreateToken(ctx context.Context, st *store.Store, name, tier string, maxRisk wire.Risk, createdBy string) (bearer string, t store.Token, err error) {
	switch {
	case tier != TierRead && tier != TierPlan && tier != TierApply:
		return "", store.Token{}, fmt.Errorf("%w: tier %q (read, plan or apply)", ErrBadToken, tier)
	case tier == TierApply && maxRisk != wire.RiskReversible && maxRisk != wire.RiskDisruptive:
		return "", store.Token{}, fmt.Errorf("%w: an apply token needs max_risk reversible or disruptive, not %q", ErrBadToken, maxRisk)
	case tier != TierApply && maxRisk != "":
		return "", store.Token{}, fmt.Errorf("%w: only an apply token has a max_risk", ErrBadToken)
	case name == "" || len(name) > 100:
		return "", store.Token{}, fmt.Errorf("%w: name", ErrBadToken)
	}
	id := "tok_" + hex.EncodeToString(randomBytes(8))
	sec := randomText(32)
	t, err = st.CreateToken(ctx, store.Token{ID: id, Name: name, Hash: sha256Hex(sec), Tier: tier, MaxRisk: string(maxRisk), CreatedBy: createdBy})
	if err != nil {
		return "", store.Token{}, err
	}
	return id + "." + sec, t, nil
}

// failLimiter counts failures per key. After max failures in the window,
// the key is blocked until the window has passed since its first failure.
type failLimiter struct {
	mu     sync.Mutex
	now    func() time.Time
	max    int
	window time.Duration
	keys   map[string]*failures
}

type failures struct {
	n     int
	first time.Time
}

// maxLimiterKeys bounds the keys of one limiter.
const maxLimiterKeys = 10000

func newFailLimiter(now func() time.Time, max int, window time.Duration) *failLimiter {
	return &failLimiter{now: now, max: max, window: window, keys: map[string]*failures{}}
}

// blocked returns the time to wait, or 0.
func (l *failLimiter) blocked(keys ...string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var wait time.Duration
	for _, k := range keys {
		f, ok := l.keys[k]
		if !ok {
			continue
		}
		if now.Sub(f.first) >= l.window {
			delete(l.keys, k)
			continue
		}
		if f.n >= l.max {
			wait = max(wait, l.window-now.Sub(f.first))
		}
	}
	return wait
}

func (l *failLimiter) fail(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.keys) >= maxLimiterKeys { // bound the memory (review T-011 m4)
		for k, f := range l.keys {
			if now.Sub(f.first) >= l.window {
				delete(l.keys, k)
			}
		}
		for len(l.keys) >= maxLimiterKeys { // still full: drop the oldest
			var oldest string
			var at time.Time
			for k, f := range l.keys {
				if oldest == "" || f.first.Before(at) {
					oldest, at = k, f.first
				}
			}
			delete(l.keys, oldest)
		}
	}
	for _, k := range keys {
		f, ok := l.keys[k]
		if !ok || now.Sub(f.first) >= l.window {
			f = &failures{first: now}
			l.keys[k] = f
		}
		f.n++
	}
}

func (l *failLimiter) reset(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.keys, k)
	}
}
