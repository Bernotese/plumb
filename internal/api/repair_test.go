package api

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/link/linktest"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-190: the repair core (D-372, D-383, D-385, ARCHITECTURE §19) with a
// fake transport and the real link.

// offAgents makes Online false while off is set: the agent is cut off.
type offAgents struct {
	plan.Agents
	off atomic.Bool
}

func (a *offAgents) Online(t string) bool { return !a.off.Load() && a.Agents.Online(t) }

// fakeTransport is the guest end of a repair session.
type fakeTransport struct {
	e        *env
	mu       sync.Mutex
	ready    error
	mode     string // ok, exit1, refuse, hang, noisy, silent
	sends    []plan.RepairSend
	bundles  [][]byte
	newAgent *linktest.Agent
}

func (f *fakeTransport) Ready(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func (f *fakeTransport) Send(ctx context.Context, s plan.RepairSend) (int, bool, error) {
	f.mu.Lock()
	mode := f.mode
	f.sends = append(f.sends, s)
	f.mu.Unlock()
	if mode == "refuse" { // a changed host key: refused before the bundle
		return 0, false, errors.New("ssh: the host key is not pinned")
	}
	var buf bytes.Buffer
	if err := s.Bundle(&buf); err != nil {
		return 0, false, err
	}
	f.mu.Lock()
	f.bundles = append(f.bundles, buf.Bytes())
	f.mu.Unlock()
	switch mode {
	case "exit1":
		fmt.Fprint(s.Output, "plumb-install: systemctl failed\n")
		return 1, true, nil
	case "hang":
		<-ctx.Done()
		return 0, true, ctx.Err()
	case "noisy":
		chunk := bytes.Repeat([]byte("ab\x1b[31m\x00\r\u202e\xff"), 1024)
		for n := 0; n < 1<<20; n += len(chunk) {
			s.Output.Write(chunk)
		}
		return 3, true, nil
	case "silent": // exit 0, but the new agent never comes
		return 0, true, nil
	case "other": // exit 0, and the guest enrolls with another token
		other, _, err := f.e.link.NewEnrollToken(f.e.ctx, s.Target)
		if err != nil {
			return 0, true, err
		}
		a := linktest.New(f.e.link.Handler(), func() (string, bool) { return other, true })
		caps := wire.Capabilities{Commands: wire.AgentCommands(), Resources: resource.Capabilities()}
		a.SetHello(wire.Hello{Capabilities: &caps})
		go a.Run(f.e.ctx)
		return 0, true, nil
	}
	// ok: the install script restarts the agent, which enrolls with the
	// one-shot token of the bundle.
	tok := bundleToken(f.e.t, buf.Bytes())
	a := linktest.New(f.e.link.Handler(), func() (string, bool) { return tok, true })
	caps := wire.Capabilities{Commands: wire.AgentCommands(), Resources: resource.Capabilities()}
	a.SetHello(wire.Hello{Capabilities: &caps})
	f.mu.Lock()
	f.newAgent = a
	f.mu.Unlock()
	go a.Run(f.e.ctx)
	fmt.Fprint(s.Output, "plumb-install: the agent is installed and restarted\n")
	return 0, true, nil
}

func bundleMembers(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	var names []string
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		out[h.Name] = body
		names = append(names, h.Name)
	}
	if !slices.Equal(names, wire.RepairMembers) {
		t.Fatalf("bundle members %v", names)
	}
	return out
}

func bundleToken(t *testing.T, b []byte) string {
	var e wire.RepairEnroll
	if err := json.Unmarshal(bundleMembers(t, b)["enroll.json"], &e); err != nil {
		t.Fatal(err)
	}
	return e.Token
}

// countingTokens counts the drops of repair tokens.
type countingTokens struct {
	plan.RepairTokens
	drops atomic.Int32
}

func (c *countingTokens) DropRepairToken(ctx context.Context, target string, t link.RepairToken) error {
	c.drops.Add(1)
	return c.RepairTokens.DropRepairToken(ctx, target, t)
}

type repairEnv struct {
	*env
	tokens *countingTokens
	logs   *lockedBuf
	tr     *fakeTransport
	agents *offAgents
	offset atomic.Int64
	adm    *client
	old    *linktest.Agent
}

func newRepairEnv(t *testing.T) *repairEnv {
	r := &repairEnv{tr: &fakeTransport{mode: "ok"}, logs: &lockedBuf{}}
	envLog = slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	defer func() { envLog = quiet }()
	r.env = newEnv(t, false, func(c *plan.Config) {
		r.tokens = &countingTokens{RepairTokens: c.Agents.(plan.RepairTokens)} // the link
		tokens := r.tokens
		r.agents = &offAgents{Agents: c.Agents}
		c.Agents = r.agents
		c.Now = func() time.Time { return time.Now().Add(time.Duration(r.offset.Load())) }
		c.Repair = plan.RepairConfig{Transport: r.tr, Tokens: tokens, AgentBinary: []byte("agent-binary"),
			CA: []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), AgentURL: "https://192.0.2.10:8444",
			HelloWait: 3 * time.Second, SessionTimeout: 300 * time.Millisecond}
	})
	r.tr.e = r.env
	r.adm = r.admin()
	r.seedAgent(r.adm, slice3Caps())
	r.old = r.agents0()
	return r
}

// lockedBuf is a log sink for several goroutines.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// noToken greps the Run, the Plan, every event, every API answer and the
// controller log for the bundle token (review T1 of #295): 0 hits.
func (r *repairEnv) noToken(planID, runID int64) {
	r.t.Helper()
	if len(r.tr.bundles) == 0 {
		return
	}
	tok := bundleToken(r.t, r.tr.bundles[len(r.tr.bundles)-1])
	for _, path := range []string{"/api/v1/runs/" + itoa(runID), "/api/v1/plans/" + itoa(planID)} {
		if code, body := r.adm.do("GET", path, nil); code != 200 || strings.Contains(string(body), tok) {
			r.t.Fatalf("%s: %d, token in it: %v", path, code, strings.Contains(string(body), tok))
		}
	}
	evs, err := r.st.EventsAfter(r.ctx, 0, 100000)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, e := range evs {
		if strings.Contains(string(e.Data), tok) || strings.Contains(e.Subject, tok) {
			r.t.Fatalf("event %s holds the token", e.Type)
		}
	}
	r.mu.Lock()
	for _, b := range r.seen {
		if bytes.Contains(b, []byte(tok)) {
			r.mu.Unlock()
			r.t.Fatal("an API answer holds the token")
		}
	}
	r.mu.Unlock()
	if strings.Contains(r.logs.String(), tok) {
		r.t.Fatal("the controller log holds the token")
	}
	if !strings.Contains(r.logs.String(), "level=") {
		r.t.Fatal("the log capture is empty")
	}
}

func (r *repairEnv) agents0() *linktest.Agent { return r.env.agents["web1"] }

func (r *repairEnv) eventTypes() []string {
	r.t.Helper()
	evs, err := r.st.EventsAfter(r.ctx, 0, 10000)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

// enrollCode posts an enrollment with tok to the agent listener.
func (r *repairEnv) enrollCode(tok string) int {
	b, _ := json.Marshal(wire.EnrollRequest{Token: tok, Hostname: "late", Version: "test"})
	req := httptest.NewRequest("POST", wire.PathEnroll, bytes.NewReader(b))
	rec := httptest.NewRecorder()
	r.link.Handler().ServeHTTP(rec, req)
	return rec.Code
}

func (r *repairEnv) stopGuest(target string) {
	r.t.Helper()
	if err := r.drv.StopOutside(r.guestID(target)); err != nil {
		r.t.Fatal(err)
	}
}

// cutOff makes the old agent silent for 11 minutes.
func (r *repairEnv) cutOff() {
	r.old.Pause(true)
	r.agents.off.Store(true)
	r.offset.Store(int64(11 * time.Minute))
}

func (r *repairEnv) propose(c *client, body any, want int) (planOut, string) {
	r.t.Helper()
	code, b := c.do("POST", "/api/v1/targets/web1/repair", body)
	if code != want {
		r.t.Fatalf("repair: %d %s, want %d", code, b, want)
	}
	var p planOut
	json.Unmarshal(b, &p)
	return p, string(b)
}

// oldWorks reports whether the old credential still gets 204 for a hello.
func (r *repairEnv) oldCode() int {
	return r.old.Report(r.ctx, wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "test"}})
}

func (r *repairEnv) step(runID int64) (string, stepView) {
	r.t.Helper()
	status, steps := runSteps(r.t, r.env, r.adm, runID)
	for _, s := range steps {
		if s.Resource == plan.RepairStepID {
			return status, s
		}
	}
	r.t.Fatalf("no repair step in %+v", steps)
	return "", stepView{}
}

// TestRepairHumanOnly: a plan token proposes, an apply token gets 403
// human-required, a person approves; the Plan has origin repair, risk
// disruptive and no revision (ARCHITECTURE §19).
func TestRepairHumanOnly(t *testing.T) {
	r := newRepairEnv(t)
	r.cutOff()
	p, raw := r.propose(r.token(TierPlan, ""), map[string]any{"reason": "agent dead"}, 201)
	var v struct {
		Origin, Risk string
		HumanOnly    []string `json:"human_only"`
		Targets      []struct {
			Target    string
			Resources []any
		}
	}
	json.Unmarshal([]byte(raw), &v)
	if v.Origin != plan.OriginRepair || v.Risk != "disruptive" || !slices.Equal(v.HumanOnly, []string{plan.HumanOnlyRepair}) ||
		len(v.Targets) != 1 || v.Targets[0].Target != "web1" || len(v.Targets[0].Resources) != 0 {
		t.Fatalf("plan %s", raw)
	}
	code, body := r.token(TierApply, wire.RiskDisruptive).do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil)
	if code != 403 || !strings.Contains(string(body), "human-required") {
		t.Fatalf("apply token approve: %d %s", code, body)
	}
	if len(r.tr.sends) != 0 {
		t.Fatal("a refused approval reached the transport")
	}
	run := approve(t, r.adm, p.ID)
	status, s := r.step(run)
	if status != "succeeded" || s.Status != string(wire.StatusApplied) {
		t.Fatalf("run %s, step %+v", status, s)
	}
	if got := r.getPlan(r.adm, p.ID); got.Revision != nil {
		t.Fatalf("a repair commits no revision: %v", *got.Revision)
	}
}

// TestRepairSuccessSwapsCredential: exit 0 and a hello of the new agent
// pass the step; then the old credential is refused (D-385).
func TestRepairSuccessSwapsCredential(t *testing.T) {
	r := newRepairEnv(t)
	r.cutOff()
	p, _ := r.propose(r.adm, nil, 201)
	run := approve(t, r.adm, p.ID)
	status, s := r.step(run)
	if status != "succeeded" || s.Status != string(wire.StatusApplied) || !strings.Contains(s.Output, "exit code 0") {
		t.Fatalf("run %s, step %+v", status, s)
	}
	if code := r.oldCode(); code != 401 {
		t.Fatalf("old credential after the repair: %d, want 401", code)
	}
	r.noToken(p.ID, run)
	if code := r.tr.newAgent.SendHello(r.ctx); code != 204 {
		t.Fatalf("new credential: %d", code)
	}
	m := bundleMembers(t, r.tr.bundles[0])
	if string(m["plumb-agent"]) != "agent-binary" {
		t.Fatal("the bundle does not hold the served binary")
	}
	types := r.eventTypes()
	if !slices.Contains(types, "repair.started") || !slices.Contains(types, "repair.finished") {
		t.Fatalf("events %v", types)
	}
}

// TestRepairFailedKeepsCredential (review R1 of #290): a session that
// sends nothing, an exit 1, a timeout and a missing hello each fail the
// Run, keep the old credential working and leave the token unusable.
func TestRepairFailedKeepsCredential(t *testing.T) {
	for _, mode := range []string{"refuse", "exit1", "hang", "silent"} {
		t.Run(mode, func(t *testing.T) {
			r := newRepairEnv(t)
			r.tr.mode = mode
			r.cutOff()
			p, _ := r.propose(r.adm, nil, 201)
			run := approve(t, r.adm, p.ID)
			status, s := r.step(run)
			if status != "failed" || s.Status != string(wire.StatusFailed) {
				t.Fatalf("run %s, step %+v", status, s)
			}
			if code := r.oldCode(); code != 204 {
				t.Fatalf("old credential after a failed repair: %d, want 204", code)
			}
			r.noToken(p.ID, run)
			if mode == "refuse" {
				if len(r.tr.bundles) != 0 || !strings.Contains(s.Output, "nothing was sent") {
					t.Fatalf("refuse: bundle built or output %q", s.Output)
				}
				return
			}
			// The token in the bundle is dropped: a late enrollment fails.
			tok := bundleToken(t, r.tr.bundles[0])
			if code := r.enrollCode(tok); code != 401 {
				t.Fatalf("the token of a failed repair: enroll %d, want 401", code)
			}
			if code := r.oldCode(); code != 204 {
				t.Fatalf("old credential at the end: %d", code)
			}
		})
	}
}

// TestRepairOutputBounded: a forced command that prints 1 MiB with
// escapes leaves at most 4 KiB, escaped, in the Run step.
func TestRepairOutputBounded(t *testing.T) {
	r := newRepairEnv(t)
	r.tr.mode = "noisy"
	r.cutOff()
	p, _ := r.propose(r.adm, nil, 201)
	_, s := r.step(approve(t, r.adm, p.ID))
	if len(s.Output) > plan.MaxRepairOutput || len(s.Output) < 1024 {
		t.Fatalf("output %d bytes", len(s.Output))
	}
	for _, bad := range []string{"\x1b", "\x00", "\r", "\u202e", "\xff"} {
		if strings.Contains(s.Output, bad) {
			t.Fatalf("output holds %q", bad)
		}
	}
	if !strings.Contains(s.Output, "exit code 3") || !strings.Contains(s.Output, `\u{1b}`) || !strings.HasSuffix(s.Output, "...") {
		t.Fatalf("output %q", s.Output[:200])
	}
}

// TestRepairRefusals: 409 repair-not-needed (online, offline too short,
// guest stopped), repair-not-ready (transport), and a re-check at approval.
func TestRepairRefusals(t *testing.T) {
	r := newRepairEnv(t)
	c := r.adm
	if _, b := r.propose(c, nil, 409); !strings.Contains(b, "repair-not-needed") || !strings.Contains(b, "online") {
		t.Fatalf("online: %s", b)
	}
	r.old.Pause(true)
	r.agents.off.Store(true)
	if _, b := r.propose(c, nil, 409); !strings.Contains(b, "repair-not-needed") || !strings.Contains(b, "offline for") {
		t.Fatalf("offline too short: %s", b)
	}
	r.offset.Store(int64(11 * time.Minute))
	r.tr.ready = fmt.Errorf("no repair line: %w", plan.ErrRepairNotReady)
	if _, b := r.propose(c, nil, 409); !strings.Contains(b, "repair-not-ready") || !strings.Contains(b, "no repair line") {
		t.Fatalf("not ready: %s", b)
	}
	r.tr.ready = nil
	if _, b := r.propose(c, map[string]any{"accept_host_key": "SHA256:short"}, 400); !strings.Contains(b, "accept_host_key") {
		t.Fatalf("bad fingerprint: %s", b)
	}
	if _, b := r.propose(c, map[string]any{"command": "id"}, 400); b == "" {
		t.Fatal("an unknown field passed")
	}
	p, _ := r.propose(c, nil, 201)
	// The agent comes back before the approval: 409, nothing sent.
	r.agents.off.Store(false)
	code, body := c.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil)
	if code != 409 || !strings.Contains(string(body), "repair-not-needed") {
		t.Fatalf("approve of an online agent: %d %s", code, body)
	}
	// The guest stops: not needed either.
	r.agents.off.Store(true)
	r.stopGuest("web1")
	if _, b := r.propose(c, nil, 409); !strings.Contains(b, "does not run") {
		t.Fatalf("stopped guest: %s", b)
	}
	if len(r.tr.sends) != 0 {
		t.Fatal("a refusal reached the transport")
	}
	if code, body := c.do("POST", "/api/v1/targets/nope/repair", nil); code != 404 {
		t.Fatalf("unknown target: %d %s", code, body)
	}
}

// TestRepairAcceptHostKey: the accepted fingerprint is a visible change
// of the Plan and reaches the transport only through the approved Plan.
func TestRepairAcceptHostKey(t *testing.T) {
	r := newRepairEnv(t)
	r.cutOff()
	fp := "SHA256:" + strings.Repeat("A", 43)
	p, raw := r.propose(r.adm, map[string]any{"accept_host_key": fp}, 201)
	if !strings.Contains(raw, plan.RepairHostKeyID) || !strings.Contains(raw, fp) {
		t.Fatalf("plan does not show the key: %s", raw)
	}
	approve(t, r.adm, p.ID)
	r.eng.Wait()
	if len(r.tr.sends) != 1 || r.tr.sends[0].AcceptHostKey != fp {
		t.Fatalf("sends %+v", r.tr.sends)
	}
}

// TestRepairRequestNeverReachesBundle: no request field reaches the
// bundle (ARCHITECTURE §19, "repair is a fixed action set"). Whatever the
// body, the bundle members are the fixed five with the served binary and
// the controller CA and URL; the request text is in none of them. The
// guest never answers (mode silent), so the agent stays offline and each
// body can propose again. (A fuzz needs an env per input; T-195 can add
// one once store.Open accepts a '#' in the data dir path.)
func TestRepairRequestNeverReachesBundle(t *testing.T) {
	r := newRepairEnv(t)
	r.tr.mode = "silent"
	r.cutOff()
	bodies := []string{
		`{"reason":"$(id) ; reboot"}`,
		`{"reason":"../../etc/passwd install.sh plumb-agent"}`,
		`{"reason":"-----BEGIN CERTIFICATE-----evil","accept_host_key":"SHA256:` + strings.Repeat("B", 43) + `"}`,
		`{"reason":"https://evil.example:8444 url token"}`,
		`{"reason":"x","command":"id"}`,
		`{"reason":"x","bundle":{"install.sh":"rm -rf /"}}`,
		`{"reason":"x","url":"https://evil:1","token":"abc"}`,
		`{"reason":"` + strings.Repeat("Z", 3000) + `"}`,
		`not json`,
	}
	for _, body := range bodies {
		before := len(r.tr.bundles)
		code, out := r.adm.do("POST", "/api/v1/targets/web1/repair", []byte(body))
		if code != 201 {
			if code != 400 {
				t.Fatalf("%s: %d %s", body, code, out)
			}
			continue
		}
		var p planOut
		json.Unmarshal(out, &p)
		approve(t, r.adm, p.ID)
		r.eng.Wait()
		if len(r.tr.bundles) != before+1 {
			t.Fatalf("%s: no bundle", body)
		}
		m := bundleMembers(t, r.tr.bundles[before])
		var e wire.RepairEnroll
		if err := json.Unmarshal(m["enroll.json"], &e); err != nil || e.URL != "https://192.0.2.10:8444" {
			t.Fatalf("enroll.json %s", m["enroll.json"])
		}
		if string(m["plumb-agent"]) != "agent-binary" || string(m["ca.pem"]) != "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n" {
			t.Fatal("bundle content changed")
		}
		var req map[string]any
		json.Unmarshal([]byte(body), &req)
		for _, v := range req {
			sv, _ := v.(string)
			if len(sv) < 6 {
				continue
			}
			for name, b := range m {
				if bytes.Contains(b, []byte(sv)) {
					t.Fatalf("the request text %q reached %s", sv, name)
				}
			}
		}
	}
	if len(r.tr.bundles) < 3 {
		t.Fatalf("only %d bodies made a Plan", len(r.tr.bundles))
	}
}

// TestRepairDropsTokenOnEveryEnd (review n1 of #300): every end of the
// Run drops the repair token while it is unused, also a success through
// another enrollment; after the Run the token never enrolls.
func TestRepairDropsTokenOnEveryEnd(t *testing.T) {
	for _, tc := range []struct{ mode, status string }{
		{"ok", "succeeded"}, {"other", "succeeded"}, {"exit1", "failed"}, {"hang", "failed"}, {"silent", "failed"}, {"refuse", "failed"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			r := newRepairEnv(t)
			r.tr.mode = tc.mode
			r.cutOff()
			p, _ := r.propose(r.adm, nil, 201)
			status, _ := r.step(approve(t, r.adm, p.ID))
			if status != tc.status {
				t.Fatalf("run %s, want %s", status, tc.status)
			}
			if n := r.tokens.drops.Load(); n != 1 {
				t.Fatalf("%d drops, want 1", n)
			}
			if len(r.tr.bundles) > 0 {
				if code := r.enrollCode(bundleToken(t, r.tr.bundles[0])); code != 401 {
					t.Fatalf("the repair token enrolls after the Run: %d", code)
				}
			}
		})
	}
}
