package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// nftRunner simulates nft with one table (inet plumb) and systemd.
type nftRunner struct {
	mu    sync.Mutex
	table string
	calls [][]string
}

func (r *nftRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, args...))
	switch {
	case name == "nft" && slices.Equal(args, []string{"list", "table", "inet", "plumb"}):
		if r.table == "" {
			return nil, &resource.ExitError{Name: "nft", Code: 1, Stderr: "Error: No such file or directory"}
		}
		return []byte(r.table), nil
	case name == "nft" && len(args) == 2 && args[0] == "-f":
		b, err := os.ReadFile(args[1])
		if err != nil {
			return nil, err
		}
		r.table = ""
		if i := strings.Index(string(b), "table inet plumb {"); i >= 0 {
			r.table = string(b)[i:]
		}
		return nil, nil
	case name == "systemctl" || name == "systemd-run":
		return nil, nil
	}
	return nil, &resource.ExitError{Name: name, Code: 2, Stderr: "unexpected"}
}

func (r *nftRunner) live() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.table
}

func (r *nftRunner) count(words ...string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if len(c) >= len(words) && slices.Equal(c[:len(words)], words) {
			n++
		}
	}
	return n
}

func firewallAgent(t *testing.T, ctl *controller, ca testCA, window time.Duration, before func(root, state string)) (*testAgent, *nftRunner) {
	t.Helper()
	nft := &nftRunner{}
	ta := startAgentWith(t, ctl, ca, "tok", func(o *Options) {
		o.Runner = nft
		o.ConfirmWindow = window
		for _, d := range []string{"etc/plumb-agent", "etc/systemd/system"} {
			if err := os.MkdirAll(filepath.Join(o.Root, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if before != nil {
			before(o.Root, o.Config.StateDir)
		}
	})
	return ta, nft
}

func fwApply(res wire.Resource) wire.ApplyBody {
	return wire.ApplyBody{PlanID: 1, Resources: []wire.Resource{res}, Planned: unverified(res), MaxRisk: wire.RiskDisruptive}
}

func fwResource(attrs map[string]any) wire.Resource {
	a := map[string]any{"name": "main"}
	for k, v := range attrs {
		a[k] = v
	}
	return wire.Resource{ID: "firewall:main", Type: "firewall", Block: "fw", Attrs: a}
}

func TestSafeApplyConfirmed(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	ta, nft := firewallAgent(t, ctl, ca, 5*time.Second, nil)
	ctl.send(t, "c1", wire.CommandApply, fwApply(fwResource(map[string]any{"tcp_ports": "80"})))
	rep := ctl.result(t, "c1")
	if len(rep.Items) != 1 || rep.Items[0].Status != wire.StatusApplied || rep.Items[0].RolledBack {
		t.Fatalf("result: %+v", rep)
	}
	confirms := ctl.reportsOf(wire.ReportConfirm)
	if len(confirms) != 1 || confirms[0].CommandID != "c1" || len(confirms[0].Items) != 1 ||
		confirms[0].Items[0].Resource != "firewall:main" || confirms[0].Items[0].Status != wire.StatusRunning {
		t.Fatalf("confirm reports: %+v", confirms)
	}
	if !strings.Contains(nft.live(), "tcp dport { 80 } accept") {
		t.Fatalf("live: %q", nft.live())
	}
	boot, err := os.ReadFile(filepath.Join(ta.root, resource.FirewallBootFile))
	if err != nil || !strings.Contains(string(boot), "tcp dport { 80 } accept") {
		t.Fatalf("boot file: %v %q", err, boot)
	}
	if _, err := os.Stat(filepath.Join(ta.cfg.StateDir, safeApplyMarker)); !os.IsNotExist(err) {
		t.Error("marker left")
	}
	if nft.count("systemd-run") != 1 || nft.count("systemctl", "stop", resource.FirewallRollback+".timer") < 2 {
		t.Errorf("timer not armed and stopped: %v", nft.calls)
	}
}

func TestSafeApplyRefusedRollsBack(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	ctl.confirmCode = http.StatusConflict
	ta, nft := firewallAgent(t, ctl, ca, 5*time.Second, nil)
	start := time.Now()
	ctl.send(t, "c1", wire.CommandApply, fwApply(fwResource(nil)))
	rep := ctl.result(t, "c1")
	it := rep.Items[0]
	if it.Status != wire.StatusFailed || !it.RolledBack || !strings.Contains(it.Output, "confirmed state is back") {
		t.Fatalf("item: %+v", it)
	}
	if time.Since(start) > 4*time.Second {
		t.Error("a 409 must end the window at once")
	}
	if nft.live() != "" {
		t.Fatalf("live table after rollback: %q", nft.live())
	}
	// The boot file of a first apply deletes the table.
	boot, _ := os.ReadFile(filepath.Join(ta.root, resource.FirewallBootFile))
	if strings.Contains(string(boot), "table inet plumb {") {
		t.Errorf("boot file holds rules: %q", boot)
	}
	if len(ctl.reportsOf(wire.ReportConfirm)) != 1 {
		t.Error("409 was retried")
	}
}

func TestSafeApplyTimeoutRollsBack(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	ctl.confirmCode = http.StatusServiceUnavailable
	_, nft := firewallAgent(t, ctl, ca, 2500*time.Millisecond, nil)
	// A confirmed state first: the rollback must bring it back.
	ctl.mu.Lock()
	ctl.confirmCode = 0
	ctl.mu.Unlock()
	ctl.send(t, "c0", wire.CommandApply, fwApply(fwResource(map[string]any{"tcp_ports": "80"})))
	if it := ctl.result(t, "c0").Items[0]; it.Status != wire.StatusApplied {
		t.Fatalf("first apply: %+v", it)
	}
	before := nft.live()
	ctl.mu.Lock()
	ctl.confirmCode = http.StatusServiceUnavailable
	ctl.mu.Unlock()
	ctl.send(t, "c1", wire.CommandApply, fwApply(fwResource(map[string]any{"tcp_ports": "443"})))
	var rep wire.Report
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range ctl.reportsOf(wire.ReportResult) {
			if r.CommandID == "c1" {
				rep = r
			}
		}
		if rep.CommandID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(rep.Items) != 1 || rep.Items[0].Status != wire.StatusFailed || !rep.Items[0].RolledBack {
		t.Fatalf("result: %+v", rep)
	}
	tries := 0
	for _, r := range ctl.reportsOf(wire.ReportConfirm) {
		if r.CommandID == "c1" {
			tries++
		}
	}
	if tries < 2 {
		t.Errorf("a busy controller must be tried again, got %d tries", tries)
	}
	if nft.live() != before {
		t.Fatalf("not rolled back:\n%s\nwant\n%s", nft.live(), before)
	}
}

func TestSafeApplyMarkerAtStart(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	_, nft := firewallAgent(t, ctl, ca, 5*time.Second, func(root, state string) {
		// The agent died inside the window: the marker and a confirmed
		// boot file exist.
		if err := os.MkdirAll(state, 0o700); err != nil {
			t.Fatal(err)
		}
		m, _ := json.Marshal(safeMarker{Version: 1, Resource: "firewall:main", Type: "firewall", Command: "c9", Deadline: time.Now()})
		write(t, filepath.Join(state, safeApplyMarker), string(m))
		write(t, filepath.Join(root, resource.FirewallBootFile), "table inet plumb\ndelete table inet plumb\ntable inet plumb {\n\tchain input {\n\t}\n}\n")
	})
	rep := ctl.result(t, "c9")
	if rep.Error == "" || len(rep.Items) != 1 || rep.Items[0].Status != wire.StatusFailed || !rep.Items[0].RolledBack {
		t.Fatalf("result after restart: %+v", rep)
	}
	if !strings.Contains(nft.live(), "chain input") {
		t.Fatalf("boot file not loaded: %q", nft.live())
	}
	if nft.count("nft", "-f") != 1 {
		t.Errorf("restore calls: %v", nft.calls)
	}
}

// The confirm report goes over a new connection each time (D-300).
func TestConfirmNewConnection(t *testing.T) {
	ca := newCA(t)
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.leaf}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	c, err := newClient(Config{URL: srv.URL, CA: ca.pem}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.report(ctx, "cred", wire.Report{Type: wire.ReportDrift}); err != nil {
		t.Fatal(err)
	}
	if err := c.report(ctx, "cred", wire.Report{Type: wire.ReportDrift}); err != nil {
		t.Fatal(err)
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("keep-alive: %d connections", n)
	}
	rep := wire.Report{Type: wire.ReportConfirm, CommandID: "c", Items: []wire.Item{{Resource: "firewall:main", Status: wire.StatusRunning}}}
	for i := 0; i < 2; i++ {
		if err := c.confirm(ctx, "cred", rep, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if n := conns.Load(); n != 3 {
		t.Fatalf("confirm reused a connection: %d connections", n)
	}
}

// The agent and the real link agree on the confirm round trip (D-300).
func TestSafeApplyRealLink(t *testing.T) {
	ctx := context.Background()
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
	tok, _, err := ctl.NewEnrollToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	nft := &nftRunner{}
	startAgentWith(t, &controller{srv: srv}, testCA{pem: string(ctl.CA().CertPEM)}, tok, func(o *Options) {
		o.Runner = nft
		for _, d := range []string{"etc/plumb-agent", "etc/systemd/system"} {
			if err := os.MkdirAll(filepath.Join(o.Root, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	})
	id, err := ctl.Send("web1", wire.CommandApply, fwApply(fwResource(nil)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case d := <-deliveries:
			if d.Report.Type != wire.ReportResult || d.Report.CommandID != id {
				continue
			}
			if it := d.Report.Items[0]; it.Status != wire.StatusApplied {
				t.Fatalf("item: %+v", it)
			}
			return
		case <-deadline:
			t.Fatal("no result")
		}
	}
}
