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
	deliveries := make(chan link.Delivery, 32)
	ctl, err := link.New(link.Config{Dir: filepath.Join(t.TempDir(), "link"), Store: st,
		OnReport: func(_ context.Context, d link.Delivery) error { deliveries <- d; return nil },
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
	a, err := New(Options{Config: Config{URL: srv.URL, CA: string(ctl.CA().CertPEM),
		TokenFile: filepath.Join(dir, "token"), StateDir: filepath.Join(dir, "state"), FullCheck: Duration(time.Hour)},
		Root: "/", Runner: resource.ExecRunner{}, Hostname: "guest", CloudInitFlag: filepath.Join(dir, "cloud-init.disabled"),
		Debounce: 500 * time.Millisecond, PollWait: 1,
	})
	if err != nil {
		t.Fatal(err)
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
}
