//go:build plumbtest

package trust

import (
	"bytes"
	_ "embed"

	"github.com/ProtonMail/go-crypto/openpgp"
)

// Disposable test public key. No private key is shipped.
//
//go:embed testdata/f5/test-key.asc
var f5PublicKey []byte

func testPinnedKeys(src Source) openpgp.EntityList {
	if src != Ubuntu {
		return nil
	}
	keys, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(f5PublicKey))
	if err != nil {
		panic(err)
	}
	return keys
}
