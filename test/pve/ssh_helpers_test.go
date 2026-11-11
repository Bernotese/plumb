//go:build pve

package pve_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A fake executable checks error redaction without a network connection.
func TestSSHFailureRedactsOutput(t *testing.T) {
	dir := t.TempDir()
	check(t, os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\necho private-login-output\necho private-login-error >&2\nexit 255\n"), 0700), "write fake ssh")
	t.Setenv("PATH", dir)
	t.Setenv("PLUMB_TEST_GUEST_KEY", filepath.Join(dir, "unused-key"))
	t.Setenv("PLUMB_TEST_PVE_SSH", "unused-host")
	g := guest{id: 9001, address: "192.0.2.1/24", hosts: filepath.Join(dir, "known_hosts")}
	if out, ok := guestSSH(t, g, "true"); ok || out != "" {
		t.Fatal("guest helper retained failed-login output")
	}
	if out, ok := hostSSH(t, nil, 9000, "list"); ok || out != "" {
		t.Fatal("host helper retained failed-login output")
	}
}

// Run refusals in child test processes so an expected Fatal is observable.
func TestSSHRangeGuard(t *testing.T) {
	if mode := os.Getenv("PLUMB_TEST_RANGE_GUARD"); mode != "" {
		n := 8999
		if strings.HasSuffix(mode, "high") {
			n = 9100
		}
		if strings.HasPrefix(mode, "guest") {
			guestSSH(t, guest{id: n, address: "192.0.2.1/24"}, "true")
		} else {
			hostSSH(t, map[int]bool{n: true}, n, "reboot")
		}
		t.Fatal("range guard returned")
	}
	for _, mode := range []string{"guest-low", "guest-high", "host-low", "host-high"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "executed")
			check(t, os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\n: > \"$PLUMB_TEST_GUARD_MARKER\"\n"), 0700), "write fake ssh")
			cmd := exec.Command(os.Args[0], "-test.run=^TestSSHRangeGuard$")
			cmd.Env = append(os.Environ(), "PLUMB_TEST_RANGE_GUARD="+mode, "PLUMB_TEST_GUARD_MARKER="+marker, "PATH="+dir)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "refusing VMID outside test range") {
				t.Fatal("VMID was not refused")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("SSH executed before range refusal")
			}
		})
	}
}
