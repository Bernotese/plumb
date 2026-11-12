//go:build guest

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver/proxmox/ctpack"
	"github.com/Bernotese/plumb/internal/wire"
)

// TestUpdateInGuest runs the self-update with real systemd and the real
// agent unit (T-091, D-271 … D-273). test/guest/run.sh builds the agents
// with the tag plumbtest (5 s guard ticks, 60 s window) into /tests:
// agent-v1, agent-v2, agent-noconnect (passes the self-test, never
// connects) and agent-crash (exits at start).
func TestUpdateInGuest(t *testing.T) {
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires root in the systemd guest")
	}
	dir := os.Getenv("PLUMB_GUEST_AGENTS")
	if dir == "" {
		dir = "/tests"
	}
	bins := map[string][]byte{}
	for _, n := range []string{"v1", "v2", "noconnect", "crash"} {
		b, err := os.ReadFile(filepath.Join(dir, "agent-"+n))
		if err != nil {
			t.Fatal(err)
		}
		bins[n] = b
	}
	sha := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

	for _, shape := range []string{"vm", "lxc"} {
		t.Run("good-"+shape, func(t *testing.T) {
			g := startUnitAgent(t, shape, bins["v1"])
			g.update(t, "u1", bins["v2"], wire.StatusApplied)
			h := g.hello(t, 60*time.Second, func(h *wire.Hello) bool { return h.Version == "v2" })
			if h.BinarySHA256 != sha(bins["v2"]) || h.Reverted != nil || h.Capabilities == nil || !h.Capabilities.CanUpdate() {
				t.Fatalf("hello of v2 %+v", h)
			}
			g.until(t, 30*time.Second, "confirm", func() bool {
				return absent(DefaultStateDir+"/"+updateMarkerFile) && absent(DefaultUnitDir+"/"+guardTimer)
			})
			if b, _ := os.ReadFile(DefaultBinaryPath + ".prev"); string(b) != string(bins["v1"]) {
				t.Error(".prev is not v1")
			}
			if out, _ := exec.Command("systemctl", "is-active", guardTimer).Output(); strings.TrimSpace(string(out)) == "active" {
				t.Error("guard timer still active")
			}
		})
	}
	for _, c := range []struct{ bin, reason string }{{"noconnect", wire.RevertNoHello}, {"crash", wire.RevertCrashLoop}} {
		t.Run(c.bin, func(t *testing.T) {
			g := startUnitAgent(t, "vm", bins["v1"])
			start := time.Now()
			g.update(t, "u1", bins[c.bin], wire.StatusApplied)
			h := g.hello(t, 150*time.Second, func(h *wire.Hello) bool { return h.Reverted != nil })
			t.Logf("reverted after %v", time.Since(start).Round(time.Second))
			want := wire.UpdateRevert{From: "v1", To: "v2-" + c.bin, ToSHA256: sha(bins[c.bin]), Reason: c.reason}
			got := *h.Reverted
			got.At = ""
			if h.Version != "v1" || h.BinarySHA256 != sha(bins["v1"]) || got != want {
				t.Fatalf("hello after the revert %+v, reverted %+v", h, got)
			}
			if b, _ := os.ReadFile(DefaultBinaryPath); string(b) != string(bins["v1"]) {
				t.Error("the binary is not v1")
			}
			g.until(t, 15*time.Second, "guard removed", func() bool { return absent(DefaultUnitDir + "/" + guardTimer) })
			// The next update command clears the revert.
			g.update(t, "u2", bins["v1"], wire.StatusUnchanged)
		})
	}
	t.Run("hash-mismatch", func(t *testing.T) {
		g := startUnitAgent(t, "vm", bins["v1"])
		g.ctl.mu.Lock()
		g.ctl.binary = bins["v2"]
		g.ctl.mu.Unlock()
		g.ctl.send(t, "u1", wire.CommandUpdate, wire.UpdateBody{PlanID: 1, SHA256: sha(bins["crash"])})
		rep := g.ctl.result(t, "u1")
		if len(rep.Items) != 1 || rep.Items[0].Status != wire.StatusFailed || !strings.Contains(rep.Items[0].Output, "sha256") {
			t.Fatalf("result %+v", rep)
		}
		if b, _ := os.ReadFile(DefaultBinaryPath); string(b) != string(bins["v1"]) || !absent(DefaultStateDir+"/"+updateMarkerFile) {
			t.Error("a failed update changed the guest")
		}
	})
}

type guestAgent struct {
	ctl *controller
}

func absent(p string) bool { _, err := os.Lstat(p); return os.IsNotExist(err) }

func guestRun(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func guestWrite(t *testing.T, p string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, mode); err != nil {
		t.Fatal(err)
	}
}

// startUnitAgent installs v1 as a systemd service against a fake
// controller. vm: the binary and the token file as the vendor snippet
// leaves them. lxc: the ctpack template state with token_env, the agent
// already enrolled, and the real first-boot installer fetches v1.
func startUnitAgent(t *testing.T, shape string, v1 []byte) *guestAgent {
	t.Helper()
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	t.Cleanup(func() {
		exec.Command("systemctl", "disable", "--now", agentService, guardTimer, "plumb-agent-install.service").Run()
		paths, _ := filepath.Glob("/usr/local/bin/*plumb-agent*")
		paths = append(paths, "/etc/plumb-agent", DefaultStateDir, "/var/lib/plumb-guest/update-token",
			"/usr/local/sbin/plumb-agent-install", DefaultUnitDir+"/"+agentService, DefaultUnitDir+"/"+guardService,
			DefaultUnitDir+"/"+guardTimer)
		for _, p := range paths {
			os.RemoveAll(p)
		}
		exec.Command("systemctl", "daemon-reload").Run()
		exec.Command("systemctl", "reset-failed").Run()
		if t.Failed() {
			out, _ := exec.Command("journalctl", "--no-pager", "-n", "80", "-u", agentService, "-u", guardService).CombinedOutput()
			t.Logf("journal:\n%s", out)
		}
	})
	guestWrite(t, "/etc/plumb-agent/ca.pem", []byte(ca.pem), 0o644)
	guestWrite(t, DefaultUnitDir+"/"+agentService, []byte(ctpack.AgentUnit), 0o644)
	cfg := map[string]string{"url": ctl.srv.URL, "ca": ca.pem}
	switch shape {
	case "vm":
		guestWrite(t, "/var/lib/plumb-guest/update-token", []byte("tok-1\n"), 0o600)
		cfg["token_file"] = "/var/lib/plumb-guest/update-token"
	case "lxc":
		cfg["token_env"] = "PLUMB_ENROLL_TOKEN"
		if err := os.MkdirAll(DefaultStateDir, 0o700); err != nil {
			t.Fatal(err)
		}
		guestWrite(t, DefaultStateDir+"/credential", []byte(`{"credential":"pre-1","enrolled":true}`), 0o600)
		ctl.mu.Lock()
		ctl.creds["pre-1"] = true
		ctl.mu.Unlock()
	}
	js, _ := json.Marshal(cfg)
	guestWrite(t, DefaultConfigPath, js, 0o644)
	if shape == "vm" {
		guestWrite(t, DefaultBinaryPath, v1, 0o755)
		guestRun(t, "systemctl", "daemon-reload")
		guestRun(t, "systemctl", "enable", "--now", agentService)
	} else {
		ctl.mu.Lock()
		ctl.binary = v1
		ctl.mu.Unlock()
		guestWrite(t, "/usr/local/sbin/plumb-agent-install", []byte(ctpack.FirstBootScript(ctl.srv.URL)), 0o755)
		guestRun(t, "/usr/local/sbin/plumb-agent-install")
	}
	g := &guestAgent{ctl: ctl}
	g.hello(t, 30*time.Second, func(h *wire.Hello) bool { return h.Version == "v1" })
	return g
}

// update serves bin and sends an update command for it.
func (g *guestAgent) update(t *testing.T, id string, bin []byte, want wire.Status) {
	t.Helper()
	g.ctl.mu.Lock()
	g.ctl.binary = bin
	g.ctl.mu.Unlock()
	s := sha256.Sum256(bin)
	g.ctl.send(t, id, wire.CommandUpdate, wire.UpdateBody{PlanID: 1, SHA256: hex.EncodeToString(s[:])})
	var rep wire.Report
	g.until(t, 30*time.Second, "result of "+id, func() bool {
		for _, r := range g.ctl.reportsOf(wire.ReportResult) {
			if r.CommandID == id {
				rep = r
				return true
			}
		}
		return false
	})
	if len(rep.Items) != 1 || rep.Items[0].Resource != wire.AgentBinaryID || rep.Items[0].Status != want {
		t.Fatalf("result of %s: %+v", id, rep)
	}
}

// hello waits for a hello that ok accepts, after the ones seen so far.
func (g *guestAgent) hello(t *testing.T, d time.Duration, ok func(*wire.Hello) bool) *wire.Hello {
	t.Helper()
	seen := len(g.ctl.reportsOf(wire.ReportHello))
	var h *wire.Hello
	g.until(t, d, "hello", func() bool {
		hs := g.ctl.reportsOf(wire.ReportHello)
		for _, r := range hs[min(seen, len(hs)):] {
			if ok(r.Hello) {
				h = r.Hello
				return true
			}
		}
		return false
	})
	return h
}

func (g *guestAgent) until(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			out, _ := exec.Command("systemctl", "status", "--no-pager", agentService, guardTimer).CombinedOutput()
			t.Fatalf("timeout after %v waiting for %s\n%s", d, what, fmt.Sprint(string(out)))
		}
		time.Sleep(200 * time.Millisecond)
	}
}
