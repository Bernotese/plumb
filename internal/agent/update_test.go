package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// sysRunner is a fake systemctl for the update and the guard.
type sysRunner struct {
	mu       sync.Mutex
	calls    []string
	restarts int              // NRestarts of the agent unit
	fail     map[string]error // by the joined arguments, e.g. "enable --now plumb-agent-guard.timer"
}

func (r *sysRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	call := strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if name != "systemctl" {
		return nil, errors.New("unexpected " + name)
	}
	if err := r.fail[call]; err != nil {
		return nil, err
	}
	if args[0] == "show" {
		return []byte(strconv.Itoa(r.restarts) + "\n"), nil
	}
	return nil, nil
}

func (r *sysRunner) called(call string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.calls, call)
}

// fakeBin is a shell script that stands for an agent binary: its
// self-test prints the version, or runs body.
func fakeBin(version, body string) []byte {
	if body == "" {
		body = `echo '{"version":"` + version + `"}'`
	}
	return []byte("#!/bin/sh\n# " + version + "\n" + `if [ "$1" = self-test ]; then ` + body + "; exit; fi\nexit 0\n")
}

func shaOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type updEnv struct {
	u      *updater
	r      *sysRunner
	bin    string
	units  string
	config string
	old    []byte
}

func newUpdEnv(t *testing.T) *updEnv {
	t.Helper()
	dir := t.TempDir()
	e := &updEnv{r: &sysRunner{fail: map[string]error{}}, bin: filepath.Join(dir, "bin", "plumb-agent"),
		units: filepath.Join(dir, "units"), config: filepath.Join(dir, "agent.json"), old: fakeBin("v1", "")}
	for _, d := range []string{filepath.Dir(e.bin), e.units} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(e.bin, e.old, 0o755); err != nil {
		t.Fatal(err)
	}
	st := stateDir{dir: filepath.Join(dir, "state")}
	if err := st.ensure(); err != nil {
		t.Fatal(err)
	}
	e.u = &updater{state: st, bin: binPaths{bin: e.bin}, unitDir: e.units, config: e.config, run: e.r,
		log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return e
}

func serveBytes(b []byte) fetchFunc {
	return func(_ context.Context, w io.Writer) error { _, err := w.Write(b); return err }
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func present(p string) bool { _, err := os.Lstat(p); return err == nil }

// unchangedState: the old binary, no .prev, no marker, no guard, no temp.
func (e *updEnv) unchangedState(t *testing.T) {
	t.Helper()
	if read(t, e.bin) != string(e.old) {
		t.Error("the binary changed")
	}
	for _, p := range []string{e.u.bin.prev(), e.u.bin.temp(), e.u.state.path(updateMarkerFile),
		filepath.Join(e.units, guardService), filepath.Join(e.units, guardTimer)} {
		if present(p) {
			t.Errorf("%s exists", p)
		}
	}
}

func TestUpdateSwap(t *testing.T) {
	e := newUpdEnv(t)
	nb := fakeBin("v2", "")
	changed, err := e.u.update(context.Background(), serveBytes(nb), shaOf(nb), "v1")
	if err != nil || !changed {
		t.Fatalf("update: %v %v", changed, err)
	}
	if read(t, e.bin) != string(nb) || read(t, e.u.bin.prev()) != string(e.old) {
		t.Error("binary or .prev wrong")
	}
	if fi, _ := os.Stat(e.bin); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", fi.Mode())
	}
	if present(e.u.bin.temp()) || present(e.u.bin.prevTemp()) {
		t.Error("temp file left")
	}
	m, ok, err := e.u.marker()
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	want := updateMarker{V: 1, State: updatePending, FromVersion: "v1", FromSHA: shaOf(e.old), ToVersion: "v2", ToSHA: shaOf(nb)}
	if m.V != want.V || m.State != want.State || m.FromVersion != "v1" || m.FromSHA != want.FromSHA || m.ToVersion != "v2" || m.ToSHA != want.ToSHA {
		t.Errorf("marker %+v", m)
	}
	svc := read(t, filepath.Join(e.units, guardService))
	if !strings.Contains(svc, "ExecStart="+e.bin+".prev guard -config "+e.config+"\n") {
		t.Errorf("guard unit:\n%s", svc)
	}
	if tm := read(t, filepath.Join(e.units, guardTimer)); !strings.Contains(tm, "OnUnitActiveSec=") || !strings.Contains(tm, "WantedBy=timers.target") {
		t.Errorf("guard timer:\n%s", tm)
	}
	if !e.r.called("daemon-reload") || !e.r.called("enable --now "+guardTimer) {
		t.Errorf("calls %v", e.r.calls)
	}
	// A second update while the first is pending is refused.
	nb3 := fakeBin("v3", "")
	if _, err := e.u.update(context.Background(), serveBytes(nb3), shaOf(nb3), "v2"); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Errorf("second update: %v", err)
	}
}

func TestUpdateSameBinaryIsUnchanged(t *testing.T) {
	e := newUpdEnv(t)
	fetched := false
	changed, err := e.u.update(context.Background(), func(context.Context, io.Writer) error { fetched = true; return nil }, shaOf(e.old), "v1")
	if err != nil || changed || fetched {
		t.Fatalf("changed %v err %v fetched %v", changed, err, fetched)
	}
	e.unchangedState(t)
}

func TestUpdateFailuresChangeNothing(t *testing.T) {
	good := fakeBin("v2", "")
	tests := []struct {
		name  string
		bin   []byte
		want  string // sha in the command; default the sha of bin
		fetch fetchFunc
		fail  string // runner call that fails
		err   string
	}{
		{name: "hash", bin: good, want: strings.Repeat("0", 64), err: "the command wants"},
		{name: "bad sha", bin: good, want: "ABC", err: "64 lower-case hex"},
		{name: "download", fetch: func(_ context.Context, w io.Writer) error { w.Write([]byte("part")); return errors.New("reset") }, err: "reset"},
		{name: "self-test exit", bin: fakeBin("v2", "echo broken >&2; exit 3"), err: "broken"},
		{name: "self-test garbage", bin: fakeBin("v2", "echo hello"), err: "no valid report"},
		{name: "self-test no version", bin: fakeBin("v2", `echo '{"version":""}'`), err: "no valid report"},
		{name: "not executable", bin: []byte("\x7fELF garbage"), err: "self-test"},
		{name: "guard", bin: good, fail: "enable --now " + guardTimer, err: "update guard"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newUpdEnv(t)
			if tc.fail != "" {
				e.r.fail[tc.fail] = errors.New("unit failed")
			}
			fetch, want := tc.fetch, tc.want
			if fetch == nil {
				fetch = serveBytes(tc.bin)
			}
			if want == "" {
				want = shaOf(tc.bin)
			}
			changed, err := e.u.update(context.Background(), fetch, want, "v1")
			if changed || err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("changed %v err %v, want %q", changed, err, tc.err)
			}
			if tc.name != "guard" {
				e.unchangedState(t)
				return
			}
			// The guard failed after .prev was kept: the binary, the marker
			// and the units are back.
			if read(t, e.bin) != string(e.old) || present(e.u.state.path(updateMarkerFile)) ||
				present(filepath.Join(e.units, guardTimer)) || present(e.u.bin.temp()) {
				t.Error("not undone")
			}
		})
	}
}

func TestUpdateSelfTestTimeout(t *testing.T) {
	old := selfTestTimeout
	selfTestTimeout = 200 * time.Millisecond
	t.Cleanup(func() { selfTestTimeout = old })
	e := newUpdEnv(t)
	nb := fakeBin("v2", "exec sleep 5")
	start := time.Now()
	_, err := e.u.update(context.Background(), serveBytes(nb), shaOf(nb), "v1")
	if err == nil || !strings.Contains(err.Error(), "no answer within") || time.Since(start) > 4*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
	e.unchangedState(t)
}

func TestUpdateSizeLimit(t *testing.T) {
	var sink strings.Builder
	lw := &limitWriter{w: &sink, n: 4}
	if _, err := lw.Write([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	if _, err := lw.Write([]byte("e")); err == nil {
		t.Fatal("no error above the limit")
	}
}

func TestSelfTestCommand(t *testing.T) {
	ca := newCA(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "agent.json")
	state := filepath.Join(dir, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(Config{URL: "https://127.0.0.1:8444", CA: ca.pem, StateDir: state})
	if err := os.WriteFile(cfg, b, 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := SelfTest(cfg, "v9", &out); err != nil || strings.TrimSpace(out.String()) != `{"version":"v9"}` {
		t.Fatalf("%v %q", err, out.String())
	}
	if err := SelfTest(filepath.Join(dir, "none.json"), "v9", io.Discard); err == nil {
		t.Error("missing config passed")
	}
	os.Remove(state)
	if err := SelfTest(cfg, "v9", io.Discard); err == nil {
		t.Error("missing state dir passed")
	}
}

func TestReverted(t *testing.T) {
	e := newUpdEnv(t)
	if e.u.reverted() != nil {
		t.Fatal("revert without marker")
	}
	m := updateMarker{State: updateReverted, FromVersion: "v1", ToVersion: "v2", ToSHA: "aa", Reason: wire.RevertNoHello, At: "2026-11-12T10:00:00Z"}
	if err := e.u.saveMarker(m); err != nil {
		t.Fatal(err)
	}
	want := wire.UpdateRevert{From: "v1", To: "v2", ToSHA256: "aa", Reason: wire.RevertNoHello, At: "2026-11-12T10:00:00Z"}
	if got := e.u.reverted(); got == nil || *got != want {
		t.Fatalf("reverted %+v", got)
	}
	// It stays after a hello (confirm), and the next update command clears
	// it, also when that update fails.
	if err := e.u.confirm(context.Background(), shaOf(e.old)); err != nil || e.u.reverted() == nil {
		t.Fatalf("confirm dropped the revert: %v", err)
	}
	if _, err := e.u.update(context.Background(), serveBytes(nil), strings.Repeat("0", 64), "v1"); err == nil {
		t.Fatal("update passed")
	}
	if e.u.reverted() != nil {
		t.Error("revert still reported after the next update command")
	}
}

func TestConfirm(t *testing.T) {
	e := newUpdEnv(t)
	nb := fakeBin("v2", "")
	if _, err := e.u.update(context.Background(), serveBytes(nb), shaOf(nb), "v1"); err != nil {
		t.Fatal(err)
	}
	// Another binary (the old one again) does not confirm.
	if err := e.u.confirm(context.Background(), shaOf(e.old)); err != nil {
		t.Fatal(err)
	}
	if !present(e.u.state.path(updateMarkerFile)) || !present(filepath.Join(e.units, guardTimer)) {
		t.Fatal("a foreign binary confirmed")
	}
	if err := e.u.confirm(context.Background(), shaOf(nb)); err != nil {
		t.Fatal(err)
	}
	if present(e.u.state.path(updateMarkerFile)) || present(filepath.Join(e.units, guardTimer)) || present(filepath.Join(e.units, guardService)) {
		t.Error("marker or guard left after confirm")
	}
	if !e.r.called("disable --now " + guardTimer) {
		t.Errorf("calls %v", e.r.calls)
	}
	if read(t, e.u.bin.prev()) != string(e.old) {
		t.Error(".prev not kept")
	}
}
