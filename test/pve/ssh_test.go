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
	case "neigh":
		lxc := os.Getenv("PLUMB_TEST_LXC_ID")
		if strings.Trim(lxc, "0123456789") != "" || lxc == "" {
			t.Fatal("invalid LXC id setting")
		}
		command = "pct exec " + lxc + " -- ip -4 neigh show"
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

// hostFreeImport frees one import file of the image storage by its exact
// name (pl-lead, 2026-11-11: only files of this run's images).
func hostFreeImport(t *testing.T, file string) (string, bool) {
	t.Helper()
	storage := os.Getenv("PLUMB_TEST_IMAGE_STORAGE")
	if storage == "" || strings.Trim(storage, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-") != "" ||
		!reRunImport.MatchString(file) {
		t.Fatal("refusing to free an import file outside the run pattern")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		os.Getenv("PLUMB_TEST_PVE_SSH"), "pvesm free "+storage+":import/"+file)
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return "", true
}

// tryLeaseAddress finds the DHCP address of an owned test VM: the MAC of
// net0 (qm config, read-only), then the controller LXC's neighbor table
// (read-only). The address is never logged.
func tryLeaseAddress(t *testing.T, n int) (string, bool) {
	t.Helper()
	mac := ""
	for _, line := range strings.Split(hostRead(t, n, "config"), "\n") {
		if v, ok := strings.CutPrefix(line, "net0: "); ok {
			for _, p := range strings.Split(v, ",") {
				if k, m, ok := strings.Cut(p, "="); ok && k != "bridge" && len(m) == 17 {
					mac = strings.ToLower(m)
				}
			}
		}
	}
	if mac == "" {
		t.Fatal("no MAC in net0 of the test VM")
	}
	out, ok := hostSSH(t, nil, n, "neigh")
	if !ok {
		return "", false
	}
	// Prefer a live entry: an old STALE one can remain after a lease change.
	fallback := ""
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "lladdr" && strings.ToLower(f[i+1]) == mac && f[len(f)-1] != "FAILED" {
				if a, err := netip.ParseAddr(f[0]); err == nil && a.Is4() {
					switch f[len(f)-1] {
					case "REACHABLE", "DELAY", "PERMANENT":
						return a.String() + "/32", true
					}
					if fallback == "" {
						fallback = a.String() + "/32"
					}
				}
			}
		}
	}
	return fallback, fallback != ""
}

func leaseAddress(t *testing.T, n int) string {
	t.Helper()
	var a string
	wait(t, "DHCP address of the guest found", 2*time.Minute, func() bool {
		var ok bool
		a, ok = tryLeaseAddress(t, n)
		return ok
	})
	return a
}
