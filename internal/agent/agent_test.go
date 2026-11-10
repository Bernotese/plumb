package agent

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestEnroll(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")

	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
	hello := ctl.reportsOf(wire.ReportHello)[0].Hello
	if hello == nil || hello.Hostname != "web1" || hello.OS != "debian 13" || hello.Version != "test" {
		t.Fatalf("hello = %+v", hello)
	}
	credFile := filepath.Join(ta.cfg.StateDir, credentialFile)
	if b, _ := os.ReadFile(credFile); strings.TrimSpace(string(b)) != "cred-1" {
		t.Fatalf("credential file = %q", b)
	}
	if m := mode(t, credFile); m != 0o600 {
		t.Fatalf("credential mode = %v", m)
	}
	if m := mode(t, ta.cfg.StateDir); m != 0o700 {
		t.Fatalf("state dir mode = %v", m)
	}
	if _, err := os.Stat(filepath.Join(ta.root, "etc/cloud/cloud-init.disabled")); err != nil {
		t.Fatalf("cloud-init not disabled: %v", err)
	}
}

func TestReenrollAfter401(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	waitFor(t, "first enrollment", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 1 })

	// The controller drops the credential (for example after a restore).
	ctl.mu.Lock()
	delete(ctl.creds, "cred-1")
	tries := ctl.enrollTries
	ctl.mu.Unlock()

	// The old token is used up: the agent retries with backoff and reads
	// the token source each time.
	waitFor(t, "retries with the old token", func() bool {
		ctl.mu.Lock()
		defer ctl.mu.Unlock()
		return ctl.enrollTries >= tries+3
	})
	// A re-enroll Plan sets a new token.
	ctl.mu.Lock()
	ctl.tokens["tok-2"] = true
	ctl.mu.Unlock()
	write(t, ta.cfg.TokenFile, "tok-2\n")

	waitFor(t, "second enrollment", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 2 })
	ctl.mu.Lock()
	enrolled, rejected := append([]string(nil), ctl.enrolled...), ctl.rejected["cred-1"]
	ctl.mu.Unlock()
	if strings.Join(enrolled, ",") != "tok-1,tok-2" {
		t.Fatalf("enrolled with %v", enrolled)
	}
	// The agent never loops on the old credential: one rejected call (the
	// poll in flight), at most one more (a report racing with it).
	if rejected < 1 || rejected > 2 {
		t.Fatalf("calls with the dropped credential = %d", rejected)
	}
	if b, _ := os.ReadFile(filepath.Join(ta.cfg.StateDir, credentialFile)); strings.TrimSpace(string(b)) != "cred-2" {
		t.Fatalf("credential file = %q", b)
	}
}

func TestPinnedCA(t *testing.T) {
	pinned, other := newCA(t), newCA(t)
	ctl := newController(t, other, "tok-1") // server certificate from another CA
	ta := startAgent(t, ctl, pinned, "tok-1")
	time.Sleep(300 * time.Millisecond)
	ctl.mu.Lock()
	hits := ctl.hits
	ctl.mu.Unlock()
	if hits != 0 {
		t.Fatalf("the agent sent %d requests over an untrusted TLS link", hits)
	}
	select {
	case <-ta.done:
		t.Fatal("the agent stopped; it must keep retrying")
	default:
	}
}

func TestObserveCommand(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	write(t, filepath.Join(ta.root, "srv/ok"), "ok\n")

	svc := wire.Resource{ID: "service:nginx", Type: "service", Block: "web",
		Attrs: map[string]any{"name": "nginx", "restart_on": []any{"file:/srv/conf"}}}
	ctl.send(t, "c1", wire.CommandObserve, wire.ObserveBody{Resources: []wire.Resource{
		fileRes("web", "/srv/conf", map[string]any{"content": "x"}),
		fileRes("web", "/srv/ok", map[string]any{"content": "ok\n"}),
		svc,
		{ID: "command:x", Type: "command", Block: "web", Attrs: map[string]any{}},
	}})
	rep := ctl.result(t, "c1")
	if rep.Error != "" || len(rep.Items) != 4 {
		t.Fatalf("result = %+v", rep)
	}
	got := []wire.Status{rep.Items[0].Status, rep.Items[1].Status, rep.Items[2].Status, rep.Items[3].Status}
	want := []wire.Status{wire.StatusDiffers, wire.StatusUnchanged, wire.StatusDiffers, wire.StatusFailed}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("statuses = %v, want %v", got, want)
		}
	}
	// The Plan shows the restart that the changed config causes.
	if c := rep.Items[2].Changes; len(c) != 1 || c[0].Attr != wire.AttrRestart || c[0].Risk != wire.RiskDisruptive {
		t.Fatalf("service changes = %+v", c)
	}
	if ta.runner.count("systemctl", "restart") != 0 {
		t.Fatal("observe restarted a service")
	}
}

func TestApplyOrderRiskAndRestart(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	write(t, filepath.Join(ta.root, "srv/old.txt"), "old")
	restartSawConfig := make(chan bool, 4)
	ta.runner.onRestart = func() {
		_, err := os.Stat(filepath.Join(ta.root, "srv/www/index.html"))
		restartSawConfig <- err == nil
	}

	list := []wire.Resource{
		// Listed order inside a Block: the directory before its file.
		{ID: "directory:/srv/www", Type: "directory", Block: "web", Attrs: map[string]any{"path": "/srv/www"}},
		fileRes("web", "/srv/www/index.html", map[string]any{"content": "<h1>hi</h1>\n"}),
		// Another Block: waits for its restart_on resource, restarts once.
		{ID: "service:nginx", Type: "service", Block: "svc", Attrs: map[string]any{"name": "nginx",
			"restart_on": []any{"directory:/srv/www", "file:/srv/www/index.html"}}},
		// A delete is destructive, above max_risk: refused; the rest of the Block is skipped.
		fileRes("cleanup", "/srv/old.txt", map[string]any{"state": "absent"}),
		fileRes("cleanup", "/srv/after.txt", map[string]any{"content": "x"}),
	}
	ctl.send(t, "a1", wire.CommandApply, wire.ApplyBody{PlanID: 7, Resources: list, Planned: unverified(list...), MaxRisk: wire.RiskDisruptive})
	rep := ctl.result(t, "a1")
	var st []string
	for i, it := range rep.Items {
		if it.Resource != list[i].ID {
			t.Fatalf("item %d is %s, want %s", i, it.Resource, list[i].ID)
		}
		st = append(st, string(it.Status))
	}
	if strings.Join(st, ",") != "applied,applied,applied,refused,skipped" {
		t.Fatalf("statuses = %v (%+v)", st, rep.Items)
	}
	if ta.runner.count("systemctl", "restart") != 1 || !<-restartSawConfig {
		t.Fatal("service must restart once, after its config was written")
	}
	if _, err := os.Stat(filepath.Join(ta.root, "srv/old.txt")); err != nil {
		t.Fatal("the refused delete removed the file")
	}
	if !strings.Contains(rep.Items[3].Output, "above the approved disruptive") {
		t.Fatalf("refused output = %q", rep.Items[3].Output)
	}
	if n := len(ctl.reportsOf(wire.ReportProgress)); n != len(list) {
		t.Fatalf("progress reports = %d", n)
	}
}

func TestOwnApplyIsNoDrift(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })

	motd := fileRes("motd", "/srv/motd", map[string]any{"content": "hi\n"})
	ctl.send(t, "a1", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{motd}, Planned: unverified(motd), MaxRisk: wire.RiskReversible})
	if rep := ctl.result(t, "a1"); rep.Items[0].Status != wire.StatusApplied {
		t.Fatalf("apply = %+v", rep)
	}
	time.Sleep(5 * ta.a.opt.Debounce)
	if n := len(ctl.reportsOf(wire.ReportDrift)); n != 0 {
		t.Fatalf("own apply produced %d drift reports", n)
	}

	// A change from outside: exactly one drift report.
	write(t, filepath.Join(ta.root, "srv/motd"), "edited\n")
	waitFor(t, "drift report", func() bool { return len(ctl.driftItems(motd.ID)) == 1 })
	time.Sleep(5 * ta.a.opt.Debounce)
	items := ctl.driftItems(motd.ID)
	if len(items) != 1 || items[0].Status != wire.StatusDiffers || items[0].Changes[0].From != "edited\n" {
		t.Fatalf("drift items = %+v", items)
	}

	// The drift goes away by hand: one report that says so.
	write(t, filepath.Join(ta.root, "srv/motd"), "hi\n")
	waitFor(t, "drift gone", func() bool { return len(ctl.driftItems(motd.ID)) == 2 })
	if it := ctl.driftItems(motd.ID)[1]; it.Status != wire.StatusUnchanged {
		t.Fatalf("resolved item = %+v", it)
	}
}

func TestWatchListOnDiskAndReconnect(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	conf := fileRes("app", "/srv/conf", map[string]any{"content": "x"})
	ctl.send(t, "w1", wire.CommandWatch, wire.WatchBody{Resources: []wire.Resource{conf}})
	if rep := ctl.result(t, "w1"); rep.Error != "" {
		t.Fatalf("watch = %+v", rep)
	}
	waitFor(t, "drift of the missing file", func() bool { return len(ctl.driftItems(conf.ID)) == 1 })

	path := filepath.Join(ta.cfg.StateDir, desiredFile)
	if m := mode(t, path); m != 0o600 {
		t.Fatalf("desired list mode = %v", m)
	}
	var stored []wire.Resource
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &stored); err != nil || len(stored) != 1 || stored[0].ID != conf.ID {
		t.Fatalf("desired list = %s", b)
	}

	// The link breaks and comes back: hello and all open drift again.
	ctl.mu.Lock()
	ctl.pollFail, ctl.failCode = 1, http.StatusInternalServerError
	ctl.mu.Unlock()
	waitFor(t, "drift after reconnect", func() bool { return len(ctl.driftItems(conf.ID)) >= 2 })
	if n := len(ctl.reportsOf(wire.ReportHello)); n < 2 {
		t.Fatalf("hello reports = %d", n)
	}

	// A new agent process reads the list from disk and checks it at start.
	a2, err := New(Options{Config: ta.cfg, Root: ta.root, Runner: ta.runner, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil || len(a2.desired) != 1 || a2.desired[0].ID != conf.ID {
		t.Fatalf("restart: %v %+v", err, a2.desired)
	}
}

func TestCommandErrors(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	startAgent(t, ctl, ca, "tok-1")
	ctl.send(t, "x1", "update", map[string]any{})
	ctl.send(t, "x2", wire.CommandApply, wire.ApplyBody{MaxRisk: "huge"})
	ctl.commands <- wire.Command{ID: "x3", Type: wire.CommandObserve, Body: json.RawMessage(`"no"`)}
	for _, id := range []string{"x1", "x2", "x3"} {
		if rep := ctl.result(t, id); rep.Error == "" {
			t.Errorf("%s: want an error, got %+v", id, rep)
		}
	}
}

func TestPlanOrder(t *testing.T) {
	svc := func(block, name string, after ...any) wire.Resource {
		return wire.Resource{ID: "service:" + name, Type: "service", Block: block, Attrs: map[string]any{"name": name, "restart_on": after}}
	}
	f := func(block, p string) wire.Resource { return fileRes(block, p, nil) }
	tests := []struct {
		name string
		list []wire.Resource
		err  string
	}{
		{"ok across blocks", []wire.Resource{f("a", "/x"), svc("b", "s", "file:/x")}, ""},
		{"ok earlier in the same block", []wire.Resource{f("a", "/x"), svc("a", "s", "file:/x")}, ""},
		{"later in the same block", []wire.Resource{svc("a", "s", "file:/x"), f("a", "/x")}, "cycle"},
		{"cross-block cycle", []wire.Resource{svc("a", "s1", "file:/y"), f("a", "/x"), svc("b", "s2", "file:/x"), f("b", "/y")}, "cycle"},
		{"duplicate id", []wire.Resource{f("a", "/x"), f("b", "/x")}, "twice"},
		{"unknown after id", []wire.Resource{withAfter(f("a", "/x"), "file:/nope")}, "not in the resource list"},
		{"after cycle", []wire.Resource{withAfter(f("a", "/x"), "file:/y"), withAfter(f("b", "/y"), "file:/x")}, "cycle"},
		{"after and restart_on cycle", []wire.Resource{withAfter(f("a", "/x"), "service:s"), svc("b", "s", "file:/x")}, "cycle"},
		{"after a later resource of the same use", []wire.Resource{withAfter(f("a", "/x"), "file:/y"), f("a", "/y")}, "cycle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := plan(tt.list)
			if tt.err == "" && err != nil || tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)) {
				t.Fatalf("plan: err = %v, want %q", err, tt.err)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	ca := newCA(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "agent.json")
	b, _ := json.Marshal(map[string]any{"url": "https://10.0.0.5:8444", "ca": ca.pem, "full_check": "5m"})
	write(t, good, string(b))
	cfg, err := LoadConfig(good)
	if err != nil || cfg.TokenFile != DefaultTokenFile || cfg.StateDir != DefaultStateDir || time.Duration(cfg.FullCheck) != 5*time.Minute {
		t.Fatalf("LoadConfig = %+v, %v", cfg, err)
	}
	for name, v := range map[string]map[string]any{
		"http":  {"url": "http://10.0.0.5:8444", "ca": ca.pem},
		"no ca": {"url": "https://10.0.0.5:8444", "ca": "x"},
	} {
		p := filepath.Join(dir, name+".json")
		b, _ := json.Marshal(v)
		write(t, p, string(b))
		if _, err := LoadConfig(p); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

// 429 and 503 mean "busy": retry with backoff, keep the credential.
func TestBusyIsNotRevoked(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			ca := newCA(t)
			ctl := newController(t, ca, "tok-1")
			ta := startAgent(t, ctl, ca, "tok-1")
			waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 1 })
			ctl.mu.Lock()
			ctl.failCode, ctl.pollFail, ctl.retryAfter = code, 2, "1"
			ctl.mu.Unlock()
			waitFor(t, "all busy answers used", func() bool {
				ctl.mu.Lock()
				defer ctl.mu.Unlock()
				return ctl.pollFail == 0
			})
			ctl.send(t, "o1", wire.CommandObserve, wire.ObserveBody{})
			if rep := ctl.result(t, "o1"); rep.Error != "" {
				t.Fatalf("after recovery: %+v", rep)
			}
			ctl.mu.Lock()
			enrolled := len(ctl.enrolled)
			ctl.mu.Unlock()
			if enrolled != 1 {
				t.Fatalf("enrollments = %d; a busy controller must not cause re-enrollment", enrolled)
			}
			// A busy controller keeps the link: no new hello, no resend of all drift.
			if n := len(ctl.reportsOf(wire.ReportHello)); n != 1 {
				t.Fatalf("hello reports = %d, want 1", n)
			}
			// Retry-After: 1 is honored (backoff alone would retry after ~10 ms).
			ctl.mu.Lock()
			times := append([]time.Time(nil), ctl.pollTimes...)
			ctl.mu.Unlock()
			waits := 0
			for i := 1; i < len(times); i++ {
				if times[i].Sub(times[i-1]) >= 950*time.Millisecond {
					waits++
				}
			}
			if waits < 2 {
				t.Fatalf("only %d poll gaps of >= 1 s after two busy answers", waits)
			}
			if b, _ := os.ReadFile(filepath.Join(ta.cfg.StateDir, credentialFile)); strings.TrimSpace(string(b)) != "cred-1" {
				t.Fatalf("credential changed: %q", b)
			}
		})
	}
}
