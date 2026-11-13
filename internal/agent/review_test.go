package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

	xb := fileRes("x", "/srv/x", map[string]any{"content": "b"})
	ctl.send(t, "a1", wire.CommandApply, wire.ApplyBody{MaxRisk: wire.RiskReversible,
		Resources: []wire.Resource{xb}, Planned: unverified(xb)})
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
		c := Config{URL: "https://192.0.2.1:8444", CA: ca.pem, StateDir: bad}
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

func TestCleanStaleTemps(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s := stateDir{dir: dir}
	tmp := s.path(".desired.json-crash")
	if err := os.WriteFile(tmp, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	credTmp := s.path(".credential-crash")
	if err := os.WriteFile(credTmp, []byte("bearer"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{desiredFile, ".desired.jsonx"} {
		if err := os.WriteFile(s.path(name), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dirEntry := s.path(".desired.json-dir")
	if err := os.Mkdir(dirEntry, 0o700); err != nil {
		t.Fatal(err)
	}
	link := s.path(".desired.json-link")
	if err := os.Symlink("outside", link); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanStaleTemps(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{tmp, credTmp} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("temp file remains at %s: %v", path, err)
		}
	}
	for _, path := range []string{s.path(desiredFile), s.path(".desired.jsonx"), dirEntry} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("entry removed at %s: %v", path, err)
		}
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink removed: %v", err)
	}
}

// PR #64 F1: a cleanup error only warns; the start goes on.
func TestNewContinuesWhenTempCleanupFails(t *testing.T) {
	ca := newCA(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".desired.json-crash"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := removeFile
	removeFile = func(string) error { return errors.New("forced remove failure") }
	t.Cleanup(func() { removeFile = orig })
	var logs bytes.Buffer
	a, err := New(Options{Config: Config{URL: "https://127.0.0.1:8444", CA: ca.pem, StateDir: dir}, Root: t.TempDir(), Runner: &runner{}, Log: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatalf("New stopped on cleanup error: %v", err)
	}
	if !strings.Contains(logs.String(), "could not clean stale agent state temp files") || !strings.Contains(logs.String(), "forced remove failure") {
		t.Fatalf("cleanup warning missing: %s", logs.String())
	}
	removeFile = orig
	if err := a.state.saveDesired(nil); err != nil {
		t.Fatalf("write in a valid state dir: %v", err)
	}
}

// T-043 / D-161: an unsafe state dir stops New; the agent does not fix it.
func TestNewRefusesUnsafeStateDir(t *testing.T) {
	ca := newCA(t)
	newAgent := func(dir string) error {
		_, err := New(Options{Config: Config{URL: "https://127.0.0.1:8444", CA: ca.pem, StateDir: dir}, Root: t.TempDir(), Runner: &runner{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
		return err
	}
	mkdir := func(t *testing.T, mode os.FileMode) string {
		dir := filepath.Join(t.TempDir(), "state")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	for _, mode := range []os.FileMode{0o755, 0o750, 0o701, 0o500} {
		t.Run(fmt.Sprintf("mode %04o", mode), func(t *testing.T) {
			dir := mkdir(t, mode)
			err := newAgent(dir)
			if err == nil {
				t.Fatal("New accepted the state dir")
			}
			for _, want := range []string{dir, fmt.Sprintf("has mode %04o", mode), "mode 0700", "chmod 0700 " + dir} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
			if fi, _ := os.Stat(dir); fi.Mode().Perm() != mode {
				t.Errorf("the agent changed the mode to %v", fi.Mode().Perm())
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		target := mkdir(t, 0o700)
		link := filepath.Join(t.TempDir(), "state")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		err := newAgent(link)
		if err == nil || !strings.Contains(err.Error(), link+" is a symlink") || !strings.Contains(err.Error(), "(fix: set state_dir to a new path)") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("not a directory", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "state")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := newAgent(file); err == nil || !strings.Contains(err.Error(), file+" is not a directory") || !strings.Contains(err.Error(), "(fix: set state_dir to a new path)") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("other owner", func(t *testing.T) {
		dir := mkdir(t, 0o700)
		other := os.Geteuid() + 1
		agentUID = func() int { return other }
		t.Cleanup(func() { agentUID = os.Geteuid })
		want := fmt.Sprintf("%s is not owned by the agent user", dir)
		fix := fmt.Sprintf("chown %d %s", other, dir)
		if err := newAgent(dir); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), fix) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("missing dir is created 0700", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "a", "state")
		if err := newAgent(dir); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Lstat(dir); err != nil || fi.Mode().Perm() != 0o700 || !fi.IsDir() {
			t.Fatalf("new state dir: %v %v", fi, err)
		}
	})
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

// N1: after a controller restart the link answers the pending result with
// 409. The agent drops it, keeps the link and polls again.
func TestResultRefusedForGood(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	startAgent(t, ctl, ca, "tok-1")
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 1 })
	ctl.mu.Lock()
	ctl.resultCode = http.StatusConflict
	ctl.mu.Unlock()
	ctl.send(t, "o1", wire.CommandObserve, wire.ObserveBody{})
	waitFor(t, "o1 taken", func() bool { return len(ctl.commands) == 0 })
	time.Sleep(200 * time.Millisecond)
	ctl.mu.Lock()
	ctl.resultCode = 0
	ctl.mu.Unlock()
	ctl.send(t, "o2", wire.CommandObserve, wire.ObserveBody{})
	ctl.result(t, "o2")
	if n := len(ctl.reportsOf(wire.ReportHello)); n > 2 {
		t.Fatalf("hello reports = %d, want at most one extra", n)
	}
}

// N1: an error after a working hello must not loop at BackoffMin.
func TestHelloRateIsBounded(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	startAgent(t, ctl, ca, "tok-1")
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 1 })
	ctl.mu.Lock()
	ctl.pollFail, ctl.failCode = 1000, http.StatusInternalServerError // every poll fails, hellos work
	ctl.mu.Unlock()
	time.Sleep(time.Second)
	// BackoffMax is 50 ms with jitter: about 20-40 rounds per second. A
	// backoff reset after every hello gives 100 or more.
	if n := len(ctl.reportsOf(wire.ReportHello)); n > 45 {
		t.Fatalf("%d hellos in 1 s: the backoff is reset too early", n)
	}
}

// T-022: a drift report that the controller refuses for good (400) does
// not keep the agent from polling after a reconnect.
func TestDriftRefusedForGood(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	startAgent(t, ctl, ca, "tok-1")
	conf := fileRes("app", "/srv/conf", map[string]any{"content": "x"})
	ctl.send(t, "w1", wire.CommandWatch, wire.WatchBody{Resources: []wire.Resource{conf}})
	ctl.result(t, "w1")
	waitFor(t, "open drift", func() bool { return len(ctl.driftItems(conf.ID)) == 1 })

	ctl.mu.Lock()
	ctl.driftCode = http.StatusBadRequest
	ctl.pollFail, ctl.failCode = 1, http.StatusInternalServerError // reconnect: hello + flush of all drift
	ctl.mu.Unlock()
	waitFor(t, "refused flush", func() bool { ctl.mu.Lock(); defer ctl.mu.Unlock(); return ctl.driftTries > 0 })
	ctl.send(t, "o1", wire.CommandObserve, wire.ObserveBody{})
	ctl.result(t, "o1")
	ctl.mu.Lock()
	tries := ctl.driftTries
	ctl.mu.Unlock()
	if n := len(ctl.reportsOf(wire.ReportHello)); n > 3 || tries > 2 {
		t.Fatalf("hellos %d, refused flushes %d: the agent retried a permanent refusal", n, tries)
	}
}

func overLinkLimits(rep wire.Report) bool {
	if len(rep.Items) > 10000 {
		return true
	}
	for _, it := range rep.Items {
		if len(it.Output) > 64<<10 {
			return true
		}
	}
	return false
}

func holds(rep wire.Report, res string) bool {
	for _, it := range rep.Items {
		if it.Resource == res {
			return true
		}
	}
	return false
}

// addDrift puts open drift items into the agent, as a check would.
func addDrift(ta *testAgent, items ...wire.Item) {
	ta.a.mu.Lock()
	defer ta.a.mu.Unlock()
	for _, it := range items {
		ta.a.open[it.Resource] = it
		ta.a.dirty[it.Resource] = true
	}
}

func driftItem(res, output string) wire.Item {
	return wire.Item{Resource: res, Status: wire.StatusDiffers, Output: output,
		Changes: []wire.Change{{Attr: "content", From: "a", To: "b", Risk: wire.RiskReversible}}}
}

func (ta *testAgent) dirtyCount() int {
	ta.a.mu.Lock()
	defer ta.a.mu.Unlock()
	return len(ta.a.dirty)
}

// T-022 review: a too long Output is cut, not the whole report dropped.
func TestDriftOutputIsTruncated(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 1 })
	ctl.mu.Lock()
	ctl.linkLimits = true
	ctl.mu.Unlock()
	addDrift(ta, driftItem("file:/big", strings.Repeat("é", 50<<10)), driftItem("file:/small", "ok"))
	if err := ta.a.flushDrift(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	big, small := ctl.driftItems("file:/big"), ctl.driftItems("file:/small")
	if len(big) != 1 || len(small) != 1 {
		t.Fatalf("delivered: big %d, small %d", len(big), len(small))
	}
	if out := big[0].Output; len(out) > 64<<10 || !strings.HasSuffix(out, truncateMarker) || !utf8.ValidString(out) {
		t.Errorf("output: %d bytes, valid %v", len(out), utf8.ValidString(out))
	}
	if n := ta.dirtyCount(); n != 0 {
		t.Errorf("%d items stay unsent", n)
	}
}

// T-022 review: more than 10 000 items go out in chunks.
func TestDriftIsChunked(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 1 })
	ctl.mu.Lock()
	ctl.linkLimits = true
	ctl.mu.Unlock()
	var items []wire.Item
	for i := 0; i < 10005; i++ {
		items = append(items, driftItem(fmt.Sprintf("file:/f%05d", i), ""))
	}
	addDrift(ta, items...)
	if err := ta.a.flushDrift(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	reports := ctl.reportsOf(wire.ReportDrift)
	total := 0
	for _, r := range reports {
		if len(r.Items) > 10000 {
			t.Errorf("a report with %d items", len(r.Items))
		}
		total += len(r.Items)
	}
	if len(reports) < 2 || total != 10005 || ta.dirtyCount() != 0 {
		t.Errorf("%d reports, %d items, %d unsent", len(reports), total, ta.dirtyCount())
	}
}

// T-022 review: one item that the controller refuses does not hide the
// other drift of the agent.
func TestDriftDropsOnlyTheRefusedItem(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 1 })
	ctl.mu.Lock()
	ctl.rejectRes = "file:/bad"
	ctl.mu.Unlock()
	var items []wire.Item
	for _, r := range []string{"file:/a", "file:/b", "file:/bad", "file:/c", "file:/d", "file:/e", "file:/f", "file:/g"} {
		items = append(items, driftItem(r, ""))
	}
	addDrift(ta, items...)
	if err := ta.a.flushDrift(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		got := len(ctl.driftItems(it.Resource))
		if it.Resource == "file:/bad" && got != 0 || it.Resource != "file:/bad" && got != 1 {
			t.Errorf("%s delivered %d times", it.Resource, got)
		}
	}
	if n := ta.dirtyCount(); n != 0 {
		t.Errorf("%d items stay unsent; the refused one must be dropped", n)
	}
}
