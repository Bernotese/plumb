//go:build plumbtest

package agent

import "time"

// Short guard timing for the guest test only (D-273). make build never sets
// the tag plumbtest.
const (
	guardTick   = 5 * time.Second
	guardWindow = 40 * time.Second
)
