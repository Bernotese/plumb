//go:build guest

package guest_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPackageAndService(t *testing.T) {
	comm, err := os.ReadFile("/proc/1/comm")
	if err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatalf("PID 1 must be systemd: comm=%q err=%v", comm, err)
	}
	run := func(command string, args ...string) {
		t.Helper()
		cmd := exec.Command(command, args...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", command, args, err, output)
		}
	}
	run("apt-get", "update")
	run("apt-get", "install", "-y", "--no-install-recommends", "cron")
	run("systemctl", "start", "cron.service")
	run("systemctl", "is-active", "--quiet", "cron.service")
}
