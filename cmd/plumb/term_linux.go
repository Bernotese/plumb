//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is a terminal (TCGETS works on it). A
// character device such as /dev/null is not a terminal.
func isTerminal(f *os.File) bool {
	_, err := getTermios(f.Fd())
	return err == nil
}

func getTermios(fd uintptr) (*syscall.Termios, error) {
	var t syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&t))); e != 0 {
		return nil, e
	}
	return &t, nil
}

func setTermios(fd uintptr, t *syscall.Termios) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(t))); e != 0 {
		return e
	}
	return nil
}

// readHidden reads one line from the terminal f with echo off. The echo
// comes back also when the read fails.
func readHidden(f *os.File) (string, error) {
	old, err := getTermios(f.Fd())
	if err != nil {
		return "", err
	}
	quiet := *old
	quiet.Lflag &^= syscall.ECHO
	quiet.Lflag |= syscall.ICANON | syscall.ECHONL
	if err := setTermios(f.Fd(), &quiet); err != nil {
		return "", err
	}
	defer setTermios(f.Fd(), old)
	return readLine(f, 1024)
}

// sameFileSystem reports whether two existing paths are on one file system.
func sameFileSystem(a, b string) (bool, error) {
	var sa, sb syscall.Stat_t
	if err := syscall.Stat(a, &sa); err != nil {
		return false, &os.PathError{Op: "stat", Path: a, Err: err}
	}
	if err := syscall.Stat(b, &sb); err != nil {
		return false, &os.PathError{Op: "stat", Path: b, Err: err}
	}
	return sa.Dev == sb.Dev, nil
}
