package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// D-431 (T-243): no secret value in the agent log. agentSecretKinds is
// the one list of the secret kinds that the agent holds; the scan fails
// for a kind of this list that the run did not make a value of, so a new
// kind needs a value in logScanRun before the test passes again. The
// controller side has its own list (internal/api/logscan_test.go).
var agentSecretKinds = []string{
	"enrollment token",        // the token file of the first start
	"agent credential",        // the credential of every enrollment
	"one-shot repair token",   // D-390, a repair re-enrolls with it
	"desired sensitive value", // a sensitive attribute of the desired list
	"failed sensitive value",  // the same in a command line whose tool fails
	"actual sensitive value",  // a hand edit of a sensitive attribute (drift)
}

// logScanNeedleMin: every value is random text of this length or more,
// so a hit in the log is never by chance (the T-213 rule).
const logScanNeedleMin = 16

func logScanValue() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "lscan-" + hex.EncodeToString(b)
}

// logScanRun runs a real agent against a real link on root, with the
// runner, and returns the agent log at debug level and the values of
// every kind. path is the guest path of the test file (it must not
// exist). The guest test (guest_logscan_test.go) runs it on the real
// root of a container and of a VM.
func logScanRun(t *testing.T, root, path string, r resource.Runner) (string, map[string][]string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	values := map[string][]string{}
	add := func(kind, v string) {
		if v == "" {
			t.Fatalf("empty value of %s", kind)
		}
		values[kind] = append(values[kind], v)
	}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "link.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	logs := &syncBuf{}
	debug := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	deliveries := make(chan link.Delivery, 64)
	ctl, err := link.New(link.Config{Dir: filepath.Join(t.TempDir(), "link"), Store: st, Logger: debug,
		OnReport: func(_ context.Context, d link.Delivery) error {
			select {
			case deliveries <- d:
			default:
			}
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(ctl.Handler())
	srv.TLS = ctl.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	tok, _, err := ctl.NewEnrollToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	add("enrollment token", tok)
	write(t, filepath.Join(dir, "token"), tok+"\n")
	state := filepath.Join(dir, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	oneShot := filepath.Join(dir, "enroll.token")
	start := func() func() {
		a, err := New(Options{Config: Config{URL: srv.URL, CA: string(ctl.CA().CertPEM), TokenFile: filepath.Join(dir, "token"),
			StateDir: state, FullCheck: Duration(time.Hour)}, Root: root, Runner: r, Version: "test", Hostname: "web1",
			Log: debug, RepairTokenFile: oneShot, CloudInitFlag: filepath.Join(dir, "cloud-init.disabled"),
			Debounce: 100 * time.Millisecond, PollWait: 1, BackoffMin: 10 * time.Millisecond, BackoffMax: 50 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		runCtx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); _ = a.Run(runCtx) }()
		return func() { stop(); <-done }
	}
	credential := func(old string) string {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			b, err := os.ReadFile(filepath.Join(state, credentialFile))
			var f struct{ Credential string }
			if err == nil && json.Unmarshal(b, &f) == nil && f.Credential != "" && f.Credential != old {
				return f.Credential
			}
		}
		t.Fatal("no new credential in the state dir")
		return ""
	}
	wait := func(what string, ok func(wire.Report) bool) {
		t.Helper()
		deadline := time.After(90 * time.Second)
		for {
			select {
			case d := <-deliveries:
				if ok(d.Report) {
					return
				}
			case <-deadline:
				t.Fatalf("no %s report", what)
			}
		}
	}

	stop := start()
	cred := credential("")
	add("agent credential", cred)

	// A sensitive file and a package whose tool fails with the
	// sensitive version on its command line.
	content, version := logScanValue(), logScanValue()
	add("desired sensitive value", content)
	add("failed sensitive value", version)
	file := wire.Resource{ID: wire.ID("file", path), Type: "file", Block: "scan",
		Attrs: map[string]any{"path": path, "content": content + "\n", "mode": "0600"}, Sensitive: []string{"content"}}
	pkg := wire.Resource{ID: "package:less", Type: "package", Block: "scan",
		Attrs: map[string]any{"name": "less", "strictness": "exact", "version": version}, Sensitive: []string{"version"}}
	id, err := ctl.Send("web1", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{file, pkg},
		Planned: unverified(file, pkg), MaxRisk: wire.RiskDisruptive})
	if err != nil {
		t.Fatal(err)
	}
	wait("apply result", func(rep wire.Report) bool { return rep.Type == wire.ReportResult && rep.CommandID == id })
	t.Cleanup(func() { os.Remove(filepath.Join(root, path)) })

	// A hand edit of the sensitive file: the drift report and its log.
	actual := logScanValue()
	add("actual sensitive value", actual)
	if err := os.WriteFile(filepath.Join(root, path), []byte(actual+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wait("drift", func(rep wire.Report) bool {
		if rep.Type != wire.ReportDrift {
			return false
		}
		for _, it := range rep.Items {
			if it.Resource == file.ID {
				return true
			}
		}
		return false
	})
	stop()

	// A repair: the one-shot token gives a new credential.
	rt, err := ctl.NewRepairToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	add("one-shot repair token", rt.Token)
	if err := os.WriteFile(oneShot, []byte(rt.Token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop = start()
	add("agent credential", credential(cred))
	stop()
	return logs.String(), values
}

// agentLogForms are the forms of a value in a text or JSON log: the
// value, its Go-quoted and its JSON-escaped form, and each line of a
// multi-line value (as logForms in internal/api; review M1 of #363).
func agentLogForms(v string) []string {
	forms := func(s string) []string {
		q := strconv.Quote(s)
		j, _ := json.Marshal(s)
		return []string{s, q[1 : len(q)-1], string(j[1 : len(j)-1])}
	}
	out := forms(v)
	for _, line := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if line != v {
			out = append(out, forms(line)...)
		}
	}
	return out
}

// logScanCheck fails for a kind without a value, a value shorter than
// logScanNeedleMin and every value in the log.
func logScanCheck(t interface {
	Helper()
	Errorf(string, ...any)
}, log string, values map[string][]string) {
	t.Helper()
	for _, kind := range agentSecretKinds {
		if len(values[kind]) == 0 {
			t.Errorf("the run made no value of the secret kind %q", kind)
		}
	}
	for kind, vs := range values {
		known := false
		for _, k := range agentSecretKinds {
			known = known || k == kind
		}
		if !known {
			t.Errorf("the value kind %q is not in agentSecretKinds", kind)
		}
		for _, v := range vs {
			if len(v) < logScanNeedleMin {
				t.Errorf("a value of %s has %d bytes; the scan needs %d or more", kind, len(v), logScanNeedleMin)
			}
			for _, f := range agentLogForms(v) {
				if len(f) >= logScanNeedleMin && strings.Contains(log, f) {
					t.Errorf("the agent log holds a value of the secret kind %q", kind)
					break
				}
			}
		}
	}
}

func TestAgentLogHoldsNoSecret(t *testing.T) {
	root := t.TempDir()
	uid, gid := os.Getuid(), os.Getgid()
	write(t, filepath.Join(root, "etc/passwd"), fmt.Sprintf("root:x:%d:%d:root:/root:/bin/bash\n", uid, gid))
	write(t, filepath.Join(root, "etc/group"), fmt.Sprintf("root:x:%d:\n", gid))
	write(t, filepath.Join(root, "etc/os-release"), "ID=debian\nVERSION_ID=\"13\"\n")
	if err := os.MkdirAll(filepath.Join(root, "srv"), 0o755); err != nil {
		t.Fatal(err)
	}
	log, values := logScanRun(t, root, "/srv/scan.env", &runner{})
	// The run reached both enrollments (the log is short: the agent logs
	// little, and every line counts).
	for _, line := range []string{"msg=enrolled", `msg="enrolled with the one-shot token"`} {
		if !strings.Contains(log, line) {
			t.Fatalf("the agent log has no %s line:\n%s", line, log)
		}
	}
	logScanCheck(t, log, values)
}

type logScanProbe struct{ errs []string }

func (*logScanProbe) Helper()                     {}
func (p *logScanProbe) Errorf(f string, a ...any) { p.errs = append(p.errs, fmt.Sprintf(f, a...)) }

// The check finds a planted value, a missing kind and an unknown kind.
func TestAgentLogScanFindsPlant(t *testing.T) {
	values := map[string][]string{}
	for _, k := range agentSecretKinds {
		values[k] = []string{logScanValue()}
	}
	p := &logScanProbe{}
	logScanCheck(p, "msg=ok", values)
	if len(p.errs) != 0 {
		t.Fatalf("clean log: %v", p.errs)
	}
	for _, k := range agentSecretKinds {
		p := &logScanProbe{}
		logScanCheck(p, "msg=x err="+values[k][0], values)
		if len(p.errs) != 1 || !strings.Contains(p.errs[0], k) {
			t.Errorf("planted %s: %v", k, p.errs)
		}
	}
	// A multi-line value with a quote, in a text log (quoted) and a JSON
	// log (escaped), also one line of it.
	multi := logScanValue() + "\n\"" + logScanValue() + "\"\n" + logScanValue()
	mv := map[string][]string{}
	for _, k := range agentSecretKinds {
		mv[k] = []string{logScanValue()}
	}
	mv[agentSecretKinds[3]] = []string{multi}
	for _, h := range []func(io.Writer) slog.Handler{
		func(w io.Writer) slog.Handler { return slog.NewTextHandler(w, nil) },
		func(w io.Writer) slog.Handler { return slog.NewJSONHandler(w, nil) },
	} {
		for _, v := range []string{multi, "x " + strings.Split(multi, "\n")[1]} {
			var buf strings.Builder
			slog.New(h(&buf)).Info("m", "v", v)
			if strings.Contains(buf.String(), multi) {
				t.Fatal("the handler did not escape the value")
			}
			p := &logScanProbe{}
			logScanCheck(p, buf.String(), mv)
			if len(p.errs) != 1 || !strings.Contains(p.errs[0], agentSecretKinds[3]) {
				t.Errorf("escaped plant %q: %v", buf.String(), p.errs)
			}
		}
	}
	delete(values, agentSecretKinds[0])
	values["new kind"] = []string{logScanValue()}
	logScanCheck(p, "", values)
	if len(p.errs) != 2 {
		t.Fatalf("missing and unknown kind: %v", p.errs)
	}
}
