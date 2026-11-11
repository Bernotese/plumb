//go:build pve

package pve_test

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

type guest struct {
	id      int
	address string
	hosts   string
}

func vmid(t *testing.T, value string) int {
	t.Helper()
	n, err := strconv.Atoi(value)
	if err != nil || n < 9000 || n > 9099 {
		t.Fatal("refusing VMID outside test range")
	}
	return n
}

// No stderr, remote command, address, key, or failed-login output reaches logs.
func guestSSH(t *testing.T, g guest, command string) (string, bool) {
	t.Helper()
	vmid(t, strconv.Itoa(g.id))
	addr, err := netip.ParsePrefix(g.address)
	check(t, err, "parse guest address")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-i", os.Getenv("PLUMB_TEST_GUEST_KEY"),
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile="+g.hosts,
		"plumb@"+addr.Addr().String(), command)
	b, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// Only IDs obtained from this run's target status or owned template discovery
// may be placed in owned. Never accept an arbitrary configured VMID.
func hostSSH(t *testing.T, owned map[int]bool, n int, action string) (string, bool) {
	t.Helper()
	vmid(t, strconv.Itoa(n))

	var command string
	switch action {
	case "list":
		command = "qm list"
	case "config":
		command = fmt.Sprintf("qm config %d", n)
	case "pool":
		pool := os.Getenv("PLUMB_TEST_POOL")
		if pool == "" || strings.Trim(pool, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-") != "" {
			t.Fatal("invalid pool setting")
		}
		command = "pvesh get /pools/" + pool + " --output-format json"
	case "imports":
		storage := os.Getenv("PLUMB_TEST_IMAGE_STORAGE")
		if storage == "" || strings.Trim(storage, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-") != "" {
			t.Fatal("invalid image storage setting")
		}
		command = "pvesm list " + storage + " --content import"
	case "memory":
		command = fmt.Sprintf("qm set %d --memory 1536", n)
	case "reboot":
		command = fmt.Sprintf("qm reboot %d", n)
	case "template-delete":
		command = fmt.Sprintf("qm destroy %d --purge 1", n)
	default:
		t.Fatal("refusing unsupported host action")
	}
	if action == "memory" || action == "reboot" || action == "template-delete" {
		if !owned[n] {
			t.Fatalf("refusing host action on unowned VMID %d", n)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		os.Getenv("PLUMB_TEST_PVE_SSH"), command)
	// Output retains successful reads only; failed-login diagnostics are discarded.
	b, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(b), true
}

// sshFailureClass tries one guest login and returns only a class of the
// failure (no output, address or key): auth, refused, timeout, hostkey, other.
func sshFailureClass(g guest) string {
	addr, err := netip.ParsePrefix(g.address)
	if err != nil {
		return "other"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-i", os.Getenv("PLUMB_TEST_GUEST_KEY"),
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile="+g.hosts,
		"plumb@"+addr.Addr().String(), "true")
	out, _ := cmd.CombinedOutput()
	s := string(out)
	switch {
	case strings.Contains(s, "Permission denied"):
		return "auth"
	case strings.Contains(s, "Connection refused"):
		return "refused"
	case strings.Contains(s, "timed out") || ctx.Err() != nil:
		return "timeout"
	case strings.Contains(s, "Host key verification failed") || strings.Contains(s, "REMOTE HOST IDENTIFICATION"):
		return "hostkey"
	case strings.Contains(s, "No route to host"):
		return "noroute"
	}
	return "other"
}
