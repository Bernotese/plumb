//go:build guest

package agent

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// TestGuestUnbind (T-205, host finding F8): unbind through the real link
// to an agent that runs as root in the systemd guest. Afterwards the agent
// unit, its binary, /etc/plumb-agent, the state and the repair line are
// gone; a person's own authorized_keys lines stay.
func TestGuestUnbind(t *testing.T) {
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires root in the systemd guest")
	}
	keys := "/root/.ssh/authorized_keys"
	if old, err := os.ReadFile(keys); err == nil {
		t.Cleanup(func() { os.WriteFile(keys, old, 0o600) })
	} else {
		t.Cleanup(func() { os.Remove(keys) })
	}
	const own = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGuestOwnKey person@laptop\n"
	install := map[string]string{
		keys:                          own + `restrict,command="true" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIRepair plumb-repair-k1` + "\n",
		"/usr/local/bin/plumb-agent":  "#!/bin/sh\nexit 0\n",
		"/etc/plumb-agent/agent.json": "{}\n",
		"/etc/systemd/system/plumb-agent.service":     "[Unit]\nDescription=plumb test\n[Service]\nExecStart=/usr/local/bin/plumb-agent\n[Install]\nWantedBy=multi-user.target\n",
		"/etc/systemd/system/plumb-agent-guard.timer": "[Timer]\nOnCalendar=daily\n[Install]\nWantedBy=timers.target\n",
	}
	for p, s := range install {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "plumb-agent.service", "plumb-agent-guard.timer"}} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			t.Fatalf("systemctl %v: %v %s", args, err, out)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "link.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	deliveries := make(chan link.Delivery, 32)
	ctl, err := link.New(link.Config{Dir: filepath.Join(t.TempDir(), "link"), Store: st,
		OnReport: func(_ context.Context, d link.Delivery) error { deliveries <- d; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(ctl.Handler())
	srv.TLS = ctl.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	token, _, err := ctl.NewEnrollToken(ctx, "wiki")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll("/var/lib/plumb-guest", 0o700)
	dir, err := os.MkdirTemp("/var/lib/plumb-guest", "unbind-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	write(t, filepath.Join(dir, "token"), token)
	state := filepath.Join(dir, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{Config: Config{URL: srv.URL, CA: string(ctl.CA().CertPEM),
		TokenFile: filepath.Join(dir, "token"), StateDir: state, FullCheck: Duration(time.Hour)},
		Root: "/", Runner: resource.ExecRunner{}, Hostname: "wiki", CloudInitFlag: filepath.Join(dir, "ci.disabled"),
		Debounce: 500 * time.Millisecond, PollWait: 1})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	deadline := time.Now().Add(20 * time.Second)
	for !ctl.Online("wiki") {
		if time.Now().After(deadline) {
			t.Fatal("agent not online")
		}
		time.Sleep(20 * time.Millisecond)
	}
	id, err := ctl.Send("wiki", wire.CommandUnbind, struct{}{})
	if err != nil {
		t.Fatalf("link.Send unbind: %v", err)
	}
	timeout := time.After(30 * time.Second)
	for got := false; !got; {
		select {
		case d := <-deliveries:
			if d.Report.Type == wire.ReportResult && d.Report.CommandID == id {
				if d.Report.Error != "" {
					t.Fatalf("unbind failed in the guest: %s", d.Report.Error)
				}
				got = true
			}
		case <-timeout:
			t.Fatal("no unbind result")
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent end: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the agent did not end after its unbind")
	}
	for _, p := range []string{"/usr/local/bin/plumb-agent", "/etc/plumb-agent", "/etc/systemd/system/plumb-agent.service",
		"/etc/systemd/system/plumb-agent-guard.timer", state} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
	if out, _ := exec.Command("systemctl", "is-enabled", "plumb-agent.service").CombinedOutput(); strings.TrimSpace(string(out)) == "enabled" {
		t.Error("plumb-agent.service is still enabled")
	}
	b, err := os.ReadFile(keys)
	if err != nil || string(b) != own {
		t.Fatalf("authorized_keys after unbind %q %v; want only the person's line", b, err)
	}
}
