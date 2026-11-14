//go:build !plumbtest

package trust

import "github.com/ProtonMail/go-crypto/openpgp"

// Release builds add no keys to the pinned vendor keyring.
func testPinnedKeys(Source) openpgp.EntityList { return nil }
