package secret

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// PurposeGuestValue is the HKDF purpose of the copy rule key (D-331): the
// controller keeps keyed hashes of the values that guests reported as
// drift, never the values. A key of its own, so the hashes tell nothing
// about the other keys, and a database without the master key cannot test
// a guessed value.
const PurposeGuestValue = "guest-value/v1"

// GuestValueHash returns HMAC-SHA256 of v under the copy rule key, as
// lower-case hex.
func (s *Store) GuestValueHash(v string) string {
	m := hmac.New(sha256.New, s.guest)
	m.Write([]byte(v))
	return hex.EncodeToString(m.Sum(nil))
}
