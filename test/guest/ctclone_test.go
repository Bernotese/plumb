//go:build guest

package guest_test

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/agent"
	"github.com/Bernotese/plumb/internal/driver/proxmox/ctpack"
)

// Review B1 of #174 (D-253): the first boot of a clone of a built lxc
// Image. The guest holds the template-file state (D-230: agent.json, CA,
// installer, plumb-agent.service enabled) plus an installed agent; the
// container seal removes its paths; then the real first-boot installer
// runs against a TLS stub of the controller and the agent unit must run.
func TestGuestCTCloneFirstBoot(t *testing.T) {
	if _, err := os.Stat("/.dockerenv"); err != nil && os.Getenv("container") != "docker" {
		t.Fatal("guest test requires a Docker container")
	}
	comm, err := os.ReadFile("/proc/1/comm")
	if err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatalf("PID 1 must be systemd: comm=%q err=%v", comm, err)
	}
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	write := func(p, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	// The controller: the agent binary is a script that stays up.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent/v1/binary" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("#!/bin/sh\nexec sleep 3600\n"))
	}))
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	t.Cleanup(func() {
		exec.Command("systemctl", "disable", "--now", "plumb-agent.service").Run()
		for _, p := range []string{"/etc/plumb-agent", "/usr/local/sbin/plumb-agent-install", "/usr/local/bin/plumb-agent",
			"/etc/systemd/system/plumb-agent.service", "/var/lib/plumb-agent"} {
			os.RemoveAll(p)
		}
		exec.Command("systemctl", "daemon-reload").Run()
	})
	// The template-file state (ctpack, D-230) and an agent that ran in
	// the build container.
	write("/etc/plumb-agent/ca.pem", string(ca), 0o644)
	write("/etc/plumb-agent/agent.json", `{"url":"`+srv.URL+`","token_env":"PLUMB_ENROLL_TOKEN"}`, 0o644)
	write("/usr/local/sbin/plumb-agent-install", ctpack.FirstBootScript(srv.URL), 0o755)
	write("/etc/systemd/system/plumb-agent.service", ctpack.AgentUnit, 0o644)
	write("/usr/local/bin/plumb-agent", "#!/bin/sh\nexit 1\n", 0o755)
	write("/var/lib/plumb-agent/credential", "secret", 0o600)
	run("systemctl", "daemon-reload")
	run("systemctl", "enable", "plumb-agent.service")

	// The container seal (D-253).
	for _, p := range agent.ContainerSealRemoves() {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"/usr/local/bin/plumb-agent", "/var/lib/plumb-agent/credential"} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s survived the seal", p)
		}
	}
	if _, err := os.Stat("/etc/systemd/system/multi-user.target.wants/plumb-agent.service"); err != nil {
		t.Fatalf("the seal removed the wants link of the agent: %v", err)
	}

	// The clone's first boot: the installer of the template file.
	run("/usr/local/sbin/plumb-agent-install")
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, _ := exec.Command("systemctl", "is-active", "plumb-agent.service").Output()
		if strings.TrimSpace(string(out)) == "active" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("plumb-agent.service is %q after the installer:\n%s", out, run("systemctl", "status", "--no-pager", "plumb-agent.service"))
		}
		time.Sleep(500 * time.Millisecond)
	}
	if b, _ := os.ReadFile("/usr/local/bin/plumb-agent"); !strings.Contains(string(b), "sleep 3600") {
		t.Errorf("the installer did not put the controller's binary in place: %q", b)
	}
}
