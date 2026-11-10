package issue_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/agent"
	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

type flowRunner func(context.Context, string, ...string) ([]byte, error)

func (f flowRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

func TestRealAgentCreateDriftRevertAndUnplannedChange(t *testing.T) {
	var secrets *secret.Store
	e := newEnv(t, func(cfg *plan.Config) {
		var err error
		secrets, err = secret.Open(context.Background(), cfg.Store, secret.NewKey())
		if err != nil {
			t.Fatal(err)
		}
		cfg.Secrets = secrets
	})
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	check(os.MkdirAll(filepath.Join(root, "etc"), 0o755))
	write := func(path, text string) {
		t.Helper()
		check(os.WriteFile(filepath.Join(root, path), []byte(text), 0o644))
	}
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, path))
		check(err)
		return string(b)
	}
	write("etc/passwd", fmt.Sprintf("root:x:%d:%d:root:/root:/bin/bash\n", os.Getuid(), os.Getgid()))
	write("etc/group", fmt.Sprintf("root:x:%d:\n", os.Getgid()))
	block := `{"params":{"type":"object","properties":{"text":{"type":"string","default":"hi"}}},"resources":[{"file":{"path":"/etc/motd","content":"{{ .text }}"}},{"file":{"path":"/etc/second","content":"stable"}}]}`
	p := e.propose(plan.Proposal{Put: []state.Doc{doc(state.KindImage, "ubuntu", image), doc(state.KindBlock, "motd", block), target("web1", "hi", 10)}})
	run, err := e.eng.Approve(e.ctx, p.ID, "user:admin", nil)
	check(err)
	var token string
	eventually(t, "fake guest bootstrap", func() bool {
		binding, err := e.st.GetBinding(e.ctx, state.KindTarget, "web1")
		if err != nil {
			return false
		}
		boot, ok := e.drv.Bootstrap(driver.GuestID(binding.Ref))
		token = boot.Token
		return ok && token != ""
	})
	write("token", token)
	commands := make(chan wire.Command, 100)
	handler := e.link.Handler()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wire.PathPoll {
			handler.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if rec.Code == http.StatusOK && rec.Body.Len() > 0 {
			var cmd wire.Command
			if err := json.Unmarshal(rec.Body.Bytes(), &cmd); err != nil {
				t.Error(err)
			} else {
				commands <- cmd
			}
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	srv.TLS = e.link.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	var restarts atomic.Int32
	runner := flowRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "systemctl" {
			return nil, fmt.Errorf("unexpected command %s", name)
		}
		switch strings.Join(args, " ") {
		case "show --property=LoadState,ActiveState,UnitFileState -- app.service":
			return []byte("LoadState=loaded\nActiveState=active\nUnitFileState=enabled\n"), nil
		case "daemon-reload":
			return nil, nil
		case "restart -- app.service":
			b, err := os.ReadFile(filepath.Join(root, "etc/secret"))
			if err != nil || string(b) != "prefix=flow-secret-canary" {
				return nil, fmt.Errorf("restart before secret apply")
			}
			restarts.Add(1)
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected systemctl args %v", args)
		}
	})
	a, err := agent.New(agent.Options{Runner: runner, Config: agent.Config{URL: srv.URL, CA: string(e.link.CA().CertPEM),
		TokenFile: filepath.Join(root, "token"), StateDir: filepath.Join(root, "state"), FullCheck: agent.Duration(time.Hour)},
		Root: root, Hostname: "web1", Version: "flow-test", Log: quiet, PollWait: 1,
		Debounce: 20 * time.Millisecond, BackoffMin: 10 * time.Millisecond, BackoffMax: 50 * time.Millisecond})
	check(err)
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
	e.eng.Wait()
	run, err = e.st.GetRun(e.ctx, run.ID)
	check(err)
	if run.Status != "succeeded" || read("etc/motd") != "hi" || read("etc/second") != "stable" {
		t.Fatalf("initial real-agent apply: %+v", run)
	}
	write("etc/motd", "outside")
	iss := e.openIssue("web1", "file:/etc/motd")
	revert, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	check(err)
	if run := e.approve(revert.ID); run.Status != "succeeded" || read("etc/motd") != "hi" {
		t.Fatalf("real-agent revert: %+v", run)
	}
	resolved, err := e.st.GetIssue(e.ctx, iss.ID)
	check(err)
	if resolved.Status != store.IssueResolved || resolved.Resolution != issue.ActionRevert {
		t.Fatalf("revert did not close the Issue: %+v", resolved)
	}
	p = e.propose(plan.Proposal{Put: []state.Doc{target("web1", "planned", 10)}})
	write("etc/second", "unplanned drift") // D-075: same risk, but absent from the approved changes.
	run = e.approve(p.ID)
	if run.Status != "failed" || read("etc/second") != "unplanned drift" || read("etc/motd") != "planned" {
		t.Fatalf("unplanned file was overwritten or planned file not applied: %+v", run)
	}
	steps, err := e.st.Steps(e.ctx, run.ID)
	check(err)
	if len(steps) != 2 || steps[1].Resource != "file:/etc/second" || steps[1].Status != "refused" {
		t.Fatalf("unplanned change was not refused: %+v", steps)
	}
	if iss := e.openIssue("web1", "file:/etc/second"); iss.Status != store.IssueOpen {
		t.Fatalf("unplanned drift has no open Issue: %+v", iss)
	}
	// A new binding on an existing guest must be left out of planning observe.
	const canary = "flow-secret-canary"
	_, err = secrets.Put(e.ctx, "password", canary, "user:admin")
	check(err)
	secretBlock := doc(state.KindBlock, "secret-file", `{"params":{"type":"object","required":["password"],"properties":{"password":{"type":"string","writeOnly":true}}},"resources":[{"file":{"path":"/etc/secret","content":"prefix={{ .password }}"}},{"service":{"name":"app","restart_on":["file:/etc/secret"]}}]}`)
	secretTarget := target("web1", "planned", 10)
	secretTarget.Spec = json.RawMessage(strings.Replace(string(secretTarget.Spec), `"blocks":[`, `"blocks":[{"use":"secret-file","params":{"password":{"secret":"password"}}},`, 1))
	rule := doc(state.KindRule, "ignore-secret", `{"action":"ignore","target":"web1","resource":"file:/etc/secret","attrs":["content"]}`)
	p = e.propose(plan.Proposal{Put: []state.Doc{secretBlock, secretTarget, rule}})
	found, restartPlanned := false, false
	for _, rc := range changes(t, p)["web1"].Resources {
		if rc.Resource == "service:app" {
			for _, c := range rc.Changes {
				if c.Attr == wire.AttrRestart && c.Risk == wire.RiskDisruptive {
					restartPlanned = true
				}
			}
		}
		if rc.Resource != "file:/etc/secret" {
			continue
		}
		found = rc.Unverified && rc.Risk == wire.RiskDisruptive
		for _, c := range rc.Changes {
			if c.Attr == "content" && (!c.Sensitive || !c.Ignored || c.From != nil || c.To != nil) {
				t.Fatalf("secret change: %+v", c)
			}
		}
	}
	if !found || !restartPlanned {
		t.Fatal("missing unverified binding or planned service restart")
	}
	// The real agent receives and applies the clear value only after approval.
	run = e.approve(p.ID)
	if run.Status != "succeeded" || read("etc/secret") != "prefix="+canary {
		t.Fatalf("unverified secret apply: %+v", run)
	}
	if got := restarts.Load(); got != 1 {
		t.Fatalf("new binding restarts = %d, want 1", got)
	}
	steps, err = e.st.Steps(e.ctx, run.ID)
	check(err)
	raw, err := json.Marshal(steps)
	check(err)
	if strings.Contains(string(raw), canary) {
		t.Fatal("run steps leaked the secret")
	}
	sawApply, sawObserve := false, false
	for len(commands) > 0 {
		cmd := <-commands
		var body wire.ApplyBody
		check(json.Unmarshal(cmd.Body, &body))
		if cmd.Type == wire.CommandObserve {
			sawObserve = true
		}
		for _, r := range body.Resources {
			if r.ID != "file:/etc/secret" {
				continue
			}
			if cmd.Type == wire.CommandObserve {
				t.Fatal("planning observe delivered a new secret binding")
			}
			if cmd.Type == wire.CommandApply && r.Attrs["content"] == "prefix="+canary {
				sawApply = true
			}
		}
	}
	if !sawObserve || !sawApply {
		t.Fatal("missing planning observe or clear secret in apply body")
	}
	// Replan observed drift. The agent must also apply a masked, ignored change.
	write("etc/secret", "outside")
	secretTarget.Spec = json.RawMessage(strings.Replace(string(secretTarget.Spec), `"text":"planned"`, `"text":"planned-again"`, 1))
	p = e.propose(plan.Proposal{Put: []state.Doc{secretTarget}})
	run = e.approve(p.ID)
	if run.Status != "succeeded" || read("etc/secret") != "prefix="+canary {
		t.Fatalf("secret apply: %+v", run)
	}

	// D-139 also masks a secret embedded in a non-sensitive resource value.
	write("etc/motd", "outside="+canary)
	secretTarget.Spec = json.RawMessage(strings.Replace(string(secretTarget.Spec), `"text":"planned-again"`, `"text":"final"`, 1))
	p = e.propose(plan.Proposal{Put: []state.Doc{secretTarget}})
	if strings.Contains(string(p.Changes), canary) {
		t.Fatal("planning report leaked an embedded secret")
	}
	masked := false
	for _, rc := range changes(t, p)["web1"].Resources {
		if rc.Resource == "file:/etc/motd" {
			for _, c := range rc.Changes {
				if c.Attr == "content" {
					masked = c.From != "outside="+canary && c.From != nil
				}
			}
		}
	}
	if !masked {
		t.Fatal("missing masked non-sensitive observation")
	}

}

func TestConcurrentApprovalWithoutTargets(t *testing.T) {
	e := newEnv(t)
	p := e.propose(plan.Proposal{Put: []state.Doc{doc(state.KindBlock, "unused", motd)}})
	if len(changes(t, p)) != 0 {
		t.Fatal("fixture touches a target; the busy guard would hide the DB guard")
	}
	start, results := make(chan struct{}), make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := e.eng.Approve(e.ctx, p.ID, "user:admin", nil); results <- err }()
	}
	close(start)
	wins := 0
	for range 2 {
		if err := <-results; err == nil {
			wins++
		} else if !errors.Is(err, store.ErrPlanNotPending) {
			t.Errorf("target-free loser must reach the DB guard: %v", err)
		}
	}
	e.eng.Wait()
	head, err := e.st.Head(e.ctx)
	if err != nil || wins != 1 || head.ID != 1 || head.PlanID != p.ID {
		t.Fatalf("expected one committed approval: wins=%d head=%+v err=%v", wins, head, err)
	}
	if _, err := e.st.GetRun(e.ctx, 2); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("duplicate approval created another Run: %v", err)
	}
}
