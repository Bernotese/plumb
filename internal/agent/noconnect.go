//go:build !plumbtest_noconnect

package agent

// brokenLink is true only in a guest test binary (tag plumbtest_noconnect,
// D-273): every controller call fails, the self-test still passes.
const brokenLink = false
