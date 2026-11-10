//go:build guest

package agent

import (
	"context"
	"encoding/json"
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

func TestGuestAgentLoop(t *testing.T) {
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires root in the systemd guest")
	}
	if err := os.MkdirAll("/var/lib/plumb-guest", 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/var/lib/plumb-guest", "agent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "link.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reports := t.TempDir()
	deliveries := make(chan link.Delivery, 32)
	ctl, err := link.New(link.Config{Dir: filepath.Join(t.TempDir(), "link"), Store: st,
		OnReport: func(_ context.Context, d link.Delivery) error {
			payload, err := json.Marshal(d.Report)
			if err == nil {
				err = os.WriteFile(filepath.Join(reports, d.Report.CommandID+"-"+string(d.Report.Type)), payload, 0o600)
			}
			if err == nil {
				deliveries <- d
			}
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(ctl.Handler())
	srv.TLS = ctl.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	token, _, err := ctl.NewEnrollToken(ctx, "guest")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "token"), token)
	state := filepath.Join(dir, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".desired.json-crash", ".credential-crash"} {
		if err := os.WriteFile(filepath.Join(state, name), []byte("stale secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opt := Options{Config: Config{URL: srv.URL, CA: string(ctl.CA().CertPEM),
		TokenFile: filepath.Join(dir, "token"), StateDir: state, FullCheck: Duration(time.Hour)},
		Root: "/", Runner: resource.ExecRunner{}, Hostname: "guest", CloudInitFlag: filepath.Join(dir, "cloud-init.disabled"),
		Debounce: 500 * time.Millisecond, PollWait: 1,
	}
	a, err := New(opt)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".desired.json-crash", ".credential-crash"} {
		if _, err := os.Lstat(filepath.Join(state, name)); !os.IsNotExist(err) {
			t.Fatalf("startup did not remove %s: %v", name, err)
		}
	}
	bad := opt
	bad.Config.StateDir = filepath.Join(dir, "bad-state")
	if err := os.Mkdir(bad.Config.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	badAgent, err := New(bad) // New logs cleanup errors; persistence must refuse the directory.
	if err != nil {
		t.Fatal(err)
	}
	if err := badAgent.state.saveCredential("refuse"); err == nil || !strings.Contains(err.Error(), "mode 0700") {
		t.Fatalf("accepted state_dir mode 0755 as root: %v", err)
	}
	if mode(t, bad.Config.StateDir) != 0o755 {
		t.Fatal("agent changed the invalid state directory")
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	receive := func(typ wire.ReportType, id string) wire.Report {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case d := <-deliveries:
				if d.Report.Type == wire.ReportProgress && d.Report.CommandID == id {
					continue
				}
				if d.Target != "guest" || d.Report.Type != typ || d.Report.CommandID != id {
					t.Fatalf("unexpected delivery: %+v", d)
				}
				return d.Report
			case <-deadline:
				t.Fatalf("no %s report for %q", typ, id)
			}
		}
	}
	receive(wire.ReportHello, "")
	if mode(t, a.cfg.StateDir) != 0o700 {
		t.Fatal("state dir must be 0700")
	}
	if out, err := exec.Command("systemctl", "stop", "--", "cron.service").CombinedOutput(); err != nil {
		t.Fatalf("stop cron: %s: %v", out, err)
	}
	const path = "/srv/plumb-agent-guest"
	t.Cleanup(func() { os.Remove(path) })
	file := fileRes("guest", path, map[string]any{"content": "managed\n"})
	svc := wire.Resource{ID: "service:cron", Type: "service", Block: "guest", Attrs: map[string]any{"name": "cron"}}
	list := []wire.Resource{file, svc}
	var planned []wire.Planned
	for _, r := range list {
		rs, err := resource.New(r, a.env)
		if err != nil {
			t.Fatal(err)
		}
		obs, err := rs.Observe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		planned = append(planned, wire.Planned{Resource: r.ID, Changes: rs.Diff(obs)})
	}
	id, err := ctl.Send("guest", wire.CommandApply, wire.ApplyBody{Resources: list, Planned: planned, MaxRisk: wire.RiskReversible})
	if err != nil {
		t.Fatal(err)
	}
	rep := receive(wire.ReportResult, id)
	if rep.Error != "" || len(rep.Items) != 2 {
		t.Fatalf("apply: %+v", rep)
	}
	for i, it := range rep.Items {
		if it.Resource != list[i].ID || it.Status != wire.StatusApplied {
			t.Fatalf("apply item: %+v", it)
		}
	}
	quiet := func() {
		t.Helper()
		select {
		case d := <-deliveries:
			t.Fatalf("unexpected report: %+v", d)
		case <-time.After(2 * a.opt.Debounce):
		}
	}
	quiet() // The agent's own apply must not produce drift (D-055).
	write(t, path, "edited\n")
	select {
	case d := <-deliveries:
		if d.Report.Type != wire.ReportDrift || len(d.Report.Items) != 1 || d.Report.Items[0].Resource != file.ID || d.Report.Items[0].Status != wire.StatusDiffers {
			t.Fatalf("hand edit drift: %+v", d)
		}
	case <-time.After(a.maxDebounceWait()):
		t.Fatal("no inotify drift within the maximum debounce wait")
	}
	quiet() // The same difference is reported once.

	// A real apt failure contains the sensitive version before redaction.
	const secret = "plumb-canary-version-9a7f23"
	_, rawErr := (resource.ExecRunner{}).Run(ctx, "apt-get", "--simulate", "install", "--", "less="+secret)
	if rawErr == nil || !strings.Contains(rawErr.Error(), secret) {
		t.Fatalf("canary did not reach real tool stderr: %v", rawErr)
	}
	pkg := wire.Resource{ID: "package:less", Type: "package", Block: "canary",
		Attrs: map[string]any{"name": "less", "strictness": "exact", "version": secret}, Sensitive: []string{"version"}}
	id, err = ctl.Send("guest", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{pkg},
		Planned: unverified(pkg), MaxRisk: wire.RiskDisruptive})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(30 * time.Second)
	for {
		select {
		case d := <-deliveries:
			if d.Report.Type != wire.ReportResult || d.Report.CommandID != id {
				continue
			}
			if len(d.Report.Items) != 1 || d.Report.Items[0].Status != wire.StatusFailed || d.Report.Items[0].Output != resource.HiddenOutput {
				t.Fatalf("canary did not exercise a tool failure: %+v", d)
			}
			stored, err := os.ReadFile(filepath.Join(reports, id+"-"+string(wire.ReportResult)))
			if err != nil || strings.Contains(string(stored), secret) || !strings.Contains(string(stored), resource.HiddenOutput) {
				t.Fatalf("controller stored an unsafe canary result: %s: %v", stored, err)
			}
			return
		case <-deadline:
			t.Fatal("no canary result")
		}
	}
}
