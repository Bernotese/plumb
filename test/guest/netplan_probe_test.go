//go:build guest

package guest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/agent"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// TestNetplanProbe runs on the CI runner, with the agent across the VM NIC.
// Docker/base images are deliberately excluded: they are not cloud images.
func TestNetplanProbe(t *testing.T) {
	vm := os.Getenv("PLUMB_NETPLAN_VM")
	if vm == "" {
		t.Skip("requires the LXD cloud VM harness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "lxc", append([]string{"exec", vm, "--"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("guest command %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	t.Logf("image facts:\n%s", run("sh", "-c", `cat /etc/os-release; command -v netplan || true; if test -f /etc/netplan/50-cloud-init.yaml; then cat /etc/netplan/50-cloud-init.yaml; else echo '50-cloud-init.yaml absent'; fi`))
	t.Logf("active renderer:\n%s", run("sh", "-c", `systemctl is-active systemd-networkd NetworkManager || true; networkctl status --no-pager || true`))
	if strings.TrimSpace(run("sh", "-c", "command -v netplan || true")) == "" {
		t.Log("NETPLAN_RESULT present=false; apply/link measurement unavailable (package not installed)")
		return
	}
	t.Logf("merged netplan (renderer and match):\n%s", run("netplan", "get"))
	// The default gateway is the runner's LXD bridge address, not loopback.
	var routes []struct {
		Gateway string `json:"gateway"`
	}
	if err := json.Unmarshal([]byte(run("ip", "-j", "-4", "route", "show", "default")), &routes); err != nil || len(routes) != 1 || net.ParseIP(routes[0].Gateway) == nil {
		t.Fatalf("need one IPv4 default gateway: %v %v", routes, err)
	}
	host := routes[0].Gateway
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "link.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reports := make(chan link.Delivery, 128)
	var hellos atomic.Int64
	ctl, err := link.New(link.Config{Dir: filepath.Join(t.TempDir(), "link"), Store: st, Hosts: []string{host},
		OnReport: func(_ context.Context, d link.Delivery) error {
			if d.Report.Type == wire.ReportHello {
				hellos.Add(1)
			}
			reports <- d
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(ctl.Handler())
	srv.Listener.Close()
	srv.Listener, err = net.Listen("tcp4", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	var connections atomic.Int64
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			connections.Add(1)
		}
	}
	srv.TLS = ctl.TLSConfig()
	srv.StartTLS()
	defer srv.Close()
	token, _, err := ctl.NewEnrollToken(ctx, "netplan-probe")
	if err != nil {
		t.Fatal(err)
	}
	cfg := agent.Config{URL: srv.URL, CA: string(ctl.CA().CertPEM)}
	input, err := json.Marshal(struct {
		Config agent.Config
		Token  string
	}{cfg, token})
	if err != nil {
		t.Fatal(err)
	}
	childCtx, stopChild := context.WithCancel(ctx)
	child := exec.CommandContext(childCtx, "lxc", "exec", vm, "--", "env", "PLUMB_NETPLAN_HELPER=1", "/root/netplan-probe.test", "-test.run=^TestNetplanProbeAgent$", "-test.timeout=45s")
	child.Stdin = strings.NewReader(string(input))
	// Never print the helper's input (it contains the enrollment token).
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		stopChild()
		t.Fatal(err)
	}
	defer func() { stopChild(); _ = child.Wait() }()
	waitReport := func(typ wire.ReportType, id string) {
		t.Helper()
		for {
			select {
			case d := <-reports:
				if d.Report.Type == typ && d.Report.CommandID == id {
					return
				}
			case <-ctx.Done():
				t.Fatalf("no %s report before probe deadline", typ)
			}
		}
	}
	waitReport(wire.ReportHello, "")
	observe := func() {
		t.Helper()
		id, err := ctl.Send("netplan-probe", wire.CommandObserve, wire.ObserveBody{Resources: []wire.Resource{}})
		if err != nil {
			t.Fatal(err)
		}
		waitReport(wire.ReportResult, id)
	}
	observe() // Warm up both long-poll and report HTTP connections.
	before := connections.Load()
	helloBefore := hellos.Load()
	start := time.Now()
	type result struct {
		output []byte
		err    error
	}
	applied := make(chan result, 1)
	go func() {
		out, err := exec.CommandContext(ctx, "lxc", "exec", vm, "--", "netplan", "apply").CombinedOutput()
		applied <- result{out, err}
	}()
	var maxRoundTrip time.Duration
	var r result
	finished := false
	for !finished || time.Since(start) < 3*time.Second {
		round := time.Now()
		observe()
		if elapsed := time.Since(round); elapsed > maxRoundTrip {
			maxRoundTrip = elapsed
		}
		select {
		case r = <-applied:
			finished = true
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	if r.err != nil {
		t.Fatalf("netplan apply: %v: %s", r.err, r.output)
	}
	t.Logf("NETPLAN_RESULT present=true apply_and_probe=%s max_observe_roundtrip=%s new_tcp_connections=%d reconnect_hellos=%d\n%s", time.Since(start), maxRoundTrip, connections.Load()-before, hellos.Load()-helloBefore, r.output)

}

// TestNetplanProbeAgent is the same test binary, executed only inside the VM.
func TestNetplanProbeAgent(t *testing.T) {
	if os.Getenv("PLUMB_NETPLAN_HELPER") != "1" {
		t.Skip("VM helper only")
	}
	var input struct {
		Config agent.Config
		Token  string
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input.Config.TokenFile = filepath.Join(dir, "token")
	input.Config.StateDir = filepath.Join(dir, "state")
	if err := os.WriteFile(input.Config.TokenFile, []byte(input.Token), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(input.Config.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := agent.New(agent.Options{Config: input.Config, Root: "/", Hostname: "netplan-probe", CloudInitFlag: filepath.Join(dir, "cloud-init.disabled"), PollWait: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if err := a.Run(ctx); err != nil && ctx.Err() == nil {
		t.Fatal(fmt.Errorf("probe agent: %w", err))
	}
}
