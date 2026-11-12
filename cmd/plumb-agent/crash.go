//go:build !plumbtest_crash

package main

// crashAtStart is true only in a guest test binary that must crash-loop
// (tag plumbtest_crash, D-273).
const crashAtStart = false
