package agent

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// realLink starts a real link with a real agent of web1 (temp root, fake
// runner). The deliveries channel gets every report the link takes.
func realLink(t *testing.T) (*link.Link, *store.Store, *testAgent, chan link.Delivery) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "link.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	deliveries := make(chan link.Delivery, 64)
	ctl, err := link.New(link.Config{Dir: filepath.Join(t.TempDir(), "link"), Store: st,
		OnReport: func(_ context.Context, d link.Delivery) error { deliveries <- d; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(ctl.Handler())
	srv.TLS = ctl.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	tok, _, err := ctl.NewEnrollToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	ta := startAgent(t, &controller{srv: srv}, testCA{pem: string(ctl.CA().CertPEM)}, tok)
	waitReport(t, deliveries, wire.ReportHello, "")
	return ctl, st, ta, deliveries
}

func waitReport(t *testing.T, ch chan link.Delivery, typ wire.ReportType, id string) wire.Report {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case d := <-ch:
			if d.Report.Type == typ && d.Report.CommandID == id {
				return d.Report
			}
		case <-deadline:
			t.Fatalf("no %s report for %q", typ, id)
		}
	}
}

// T-205 (host finding F8): unbind goes through the real link to a real
// agent, which removes itself and ends after its result was taken.
func TestUnbindThroughRealLink(t *testing.T) {
	ctl, _, ta, deliveries := realLink(t)
	keys := filepath.Join(ta.root, "root/.ssh/authorized_keys")
	os.MkdirAll(filepath.Dir(keys), 0o700)
	os.WriteFile(keys, []byte("ssh-ed25519 AAAAuser person@laptop\nrestrict ssh-ed25519 AAAAk plumb-repair-a1\n"), 0o600)
	id, err := ctl.Send("web1", wire.CommandUnbind, struct{}{})
	if err != nil {
		t.Fatalf("link.Send unbind: %v", err)
	}
	if rep := waitReport(t, deliveries, wire.ReportResult, id); rep.Error != "" {
		t.Fatalf("unbind result: %+v", rep)
	}
	select {
	case <-ta.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not end after its unbind")
	}
	if _, err := os.Stat(ta.cfg.StateDir); !os.IsNotExist(err) {
		t.Fatal("the agent state stays")
	}
	b, _ := os.ReadFile(keys)
	if string(b) != "ssh-ed25519 AAAAuser person@laptop\n" {
		t.Fatalf("authorized_keys %q", b)
	}
	if ta.runner.count("systemctl", "--no-block", "stop", "plumb-agent.service") != 1 {
		t.Fatal("the agent unit was not stopped")
	}
}

// T-205: every command type of internal/wire has a case in link.Send. A
// new type without one fails here (the engine tests use a fake command
// path, so CI missed unbind).
func TestEveryCommandTypeThroughRealLink(t *testing.T) {
	ctl, _, _, _ := realLink(t)
	bodies := map[wire.CommandType]any{
		wire.CommandObserve: wire.ObserveBody{},
		wire.CommandApply:   wire.ApplyBody{MaxRisk: wire.RiskReversible},
		wire.CommandWatch:   wire.WatchBody{},
		wire.CommandGrow:    wire.GrowBody{PlanID: 1, MinBytes: 1 << 30},
		wire.CommandUpdate:  wire.UpdateBody{},
		wire.CommandUnbind:  struct{}{},
		wire.CommandSeal:    wire.SealBody{},
	}
	for _, typ := range wire.AgentCommands() {
		body, ok := bodies[typ]
		if !ok {
			t.Errorf("command type %s has no test body: add it here", typ)
			continue
		}
		target := "web1"
		if typ == wire.CommandSeal {
			target = wire.BuildLinkPrefix + "img" // seal goes only to a build guest
		}
		if _, err := ctl.Send(target, typ, body); err != nil {
			t.Errorf("link.Send %s: %v", typ, err)
		}
	}
}
