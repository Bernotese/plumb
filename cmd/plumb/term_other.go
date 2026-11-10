//go:build !linux

package main

import (
	"errors"
	"os"
)

// Plumb runs on Linux only (an LXC on Proxmox). Elsewhere the key commands
// refuse: no terminal check, no file system check.

func isTerminal(*os.File) bool { return false }

func readHidden(*os.File) (string, error) { return "", errors.New("not supported on this system") }

func sameFileSystem(a, b string) (bool, error) {
	return true, errors.New("the file system check is supported on Linux only")
}
