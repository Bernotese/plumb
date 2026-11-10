package agent

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// B1: a result report that the controller does not accept is sent again.
func TestResultIsResent(t *testing.T) {
	for _, code := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			ca := newCA(t)
			ctl := newController(t, ca, "tok-1")
			startAgent(t, ctl, ca, "tok-1")
			waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 1 })
			ctl.mu.Lock()
			ctl.reportFail, ctl.failCode = 2, code
			ctl.mu.Unlock()
			ctl.send(t, "o1", wire.CommandObserve, wire.ObserveBody{})
			ctl.result(t, "o1")
			ctl.mu.Lock()
			left := ctl.reportFail
			ctl.mu.Unlock()
			if left != 0 {
				t.Fatalf("failed result reports left = %d", left)
			}
			n := 0
			for _, r := range ctl.reportsOf(wire.ReportResult) {
				if r.CommandID == "o1" {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("accepted results for o1 = %d", n)
			}
		})
	}
}

// B1: after a 401 the link has forgotten the command; the result is dropped.
func TestResultDroppedAfter401(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1", "tok-2")
	ta := startAgent(t, ctl, ca, "tok-1")
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 1 })
	ctl.mu.Lock()
	ctl.reportFail, ctl.failCode = 1, http.StatusUnauthorized
	ctl.mu.Unlock()
	write(t, ta.cfg.TokenFile, "tok-2\n")
	ctl.send(t, "o1", wire.CommandObserve, wire.ObserveBody{})
	waitFor(t, "re-enrollment", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 2 })
	time.Sleep(200 * time.Millisecond)
	for _, r := range ctl.reportsOf(wire.ReportResult) {
		if r.CommandID == "o1" {
			t.Fatal("result was sent with the new credential")
		}
	}
}

// M2: a drift check that is running when an apply arrives must not see the
// half-applied state. The check blocks inside "systemctl show"; the apply
// waits for it, so the check never observes the new file.
func TestCheckDoesNotOverlapApply(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	write(t, filepath.Join(ta.root, "srv/x"), "a")
	release := make(chan struct{})
	blocked := make(chan struct{}, 1)
	ta.runner.mu.Lock()
	ta.runner.onShow = func() {
		select {
		case blocked <- struct{}{}:
			<-release
		default:
		}
	}
	ta.runner.mu.Unlock()

	svc := wire.Resource{ID: "service:nginx", Type: "service", Block: "s", Attrs: map[string]any{"name": "nginx"}}
	x := fileRes("x", "/srv/x", map[string]any{"content": "a"})
	ctl.send(t, "w1", wire.CommandWatch, wire.WatchBody{Resources: []wire.Resource{svc, x}})
	ctl.result(t, "w1")
	<-blocked // the full check is inside the service observe

	ctl.send(t, "a1", wire.CommandApply, wire.ApplyBody{MaxRisk: wire.RiskReversible,
		Resources: []wire.Resource{fileRes("x", "/srv/x", map[string]any{"content": "b"})}})
	time.Sleep(300 * time.Millisecond)
	if b, _ := os.ReadFile(filepath.Join(ta.root, "srv/x")); string(b) != "a" {
		t.Fatal("the apply ran while a drift check observed")
	}
	close(release)
	if rep := ctl.result(t, "a1"); rep.Items[0].Status != wire.StatusApplied {
		t.Fatalf("apply = %+v", rep)
	}
	time.Sleep(5 * ta.a.opt.Debounce)
	if items := ctl.driftItems(x.ID); len(items) != 0 {
		t.Fatalf("the agent's own change was reported as drift: %+v", items)
	}
}

// m2: watches of an old list are removed.
func TestWatchesFollowTheList(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	for _, d := range []string{"srv/a", "srv/b"} {
		if err := os.MkdirAll(filepath.Join(ta.root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctl.send(t, "w1", wire.CommandWatch, wire.WatchBody{Resources: []wire.Resource{fileRes("a", "/srv/a/x", nil), fileRes("b", "/srv/b/y", nil)}})
	ctl.result(t, "w1")
	waitFor(t, "two watches", func() bool { return ta.a.watcher.watched() == 2 })
	ctl.send(t, "w2", wire.CommandWatch, wire.WatchBody{Resources: []wire.Resource{fileRes("b", "/srv/b/y", nil)}})
	ctl.result(t, "w2")
	waitFor(t, "one watch", func() bool { return ta.a.watcher.watched() == 1 })
	ta.a.watcher.mu.Lock()
	_, ok := ta.a.watcher.dirs[filepath.Join(ta.root, "srv/b")]
	ta.a.watcher.mu.Unlock()
	if !ok {
		t.Fatal("the remaining watch is not on srv/b")
	}
}

// m3: a file that changes faster than the debounce is still checked.
func TestDebounceHasAMaximum(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	x := fileRes("x", "/srv/x", map[string]any{"content": "a"})
	write(t, filepath.Join(ta.root, "srv/x"), "a")
	ctl.send(t, "w1", wire.CommandWatch, wire.WatchBody{Resources: []wire.Resource{x}})
	ctl.result(t, "w1")
	time.Sleep(3 * ta.a.opt.Debounce)

	stop := time.Now().Add(3 * time.Second)
	reported := false
	for i := 0; time.Now().Before(stop); i++ {
		write(t, filepath.Join(ta.root, "srv/x"), strings.Repeat("b", i%5+1))
		time.Sleep(ta.a.opt.Debounce / 2) // faster than the debounce
		if len(ctl.driftItems(x.ID)) > 0 {
			reported = true
			break
		}
	}
	if !reported {
		t.Fatal("no drift report while the file kept changing")
	}
}

// m4: the agent does not change an existing state dir; a bad one is refused.
func TestStateDirRules(t *testing.T) {
	dir := t.TempDir() // mode 0755 from the test helper is fine to test with
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := stateDir{dir: dir}
	if err := s.saveCredential("x"); err == nil || !strings.Contains(err.Error(), "mode 0700") {
		t.Fatalf("open state dir: err = %v", err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o755 {
		t.Fatal("the agent changed an existing directory")
	}
	ca := newCA(t)
	for _, bad := range []string{"/", "relative/dir", "/../"} {
		c := Config{URL: "https://10.0.0.1:8444", CA: ca.pem, StateDir: bad}
		if err := c.check(); err == nil {
			t.Errorf("state_dir %q: want error", bad)
		}
	}
	fresh := stateDir{dir: filepath.Join(t.TempDir(), "new", "state")}
	if err := fresh.saveCredential("x"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(fresh.dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("new state dir mode = %v", fi.Mode())
	}
}

func TestRetryAfterParse(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	tests := map[string]time.Duration{
		"":                              0,
		"5":                             5 * time.Second,
		"-3":                            0,
		"99999":                         maxRetryAfter,
		"Tue, 10 Nov 2026 12:00:30 GMT": 30 * time.Second,
		"soon":                          0,
	}
	for in, want := range tests {
		if got := retryAfter(in, now); got != want {
			t.Errorf("retryAfter(%q) = %s, want %s", in, got, want)
		}
	}
}
