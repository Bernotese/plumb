package secret

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
)

// PurposeRepairKey binds a sealed repair private key (D-382): a
// ciphertext of a secret row never opens as one, and back.
const PurposeRepairKey = "plumb repair key v1"

func blobData(purpose, name string) []byte {
	var out []byte
	for _, part := range [][]byte{[]byte(purpose), []byte(name)} {
		out = binary.BigEndian.AppendUint32(out, uint32(len(part)))
		out = append(out, part...)
	}
	return out
}

// SealBlob seals plain with the secret-store key for a purpose and a name
// (T-191: the repair private key, in its own table, never in the secret
// table, the export or an API answer).
func (s *Store) SealBlob(purpose, name string, plain []byte) (nonce, sealed []byte, err error) {
	if purpose == "" || purpose == PurposeSecretStore {
		return nil, nil, errors.New("secret: a blob needs its own purpose")
	}
	nonce = make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, s.aead.Seal(nil, nonce, plain, blobData(purpose, name)), nil
}

// OpenBlob opens what SealBlob sealed. The caller clears the result
// after use.
func (s *Store) OpenBlob(purpose, name string, nonce, sealed []byte) ([]byte, error) {
	if len(nonce) != nonceSize {
		return nil, ErrCorrupt
	}
	plain, err := s.aead.Open(nil, nonce, sealed, blobData(purpose, name))
	if err != nil {
		return nil, ErrCorrupt
	}
	return plain, nil
}
