package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/link/linktest"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// fastPasswords keep argon2id cheap in tests.
var fastPasswords = PasswordParams{Time: 1, Memory: 64, Threads: 1}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// envLog is the logger of the next newEnv (link, plan and API). A test
// that scans the log sets it first; no test of this package is parallel.
var envLog = quiet

// env is a whole controller with the API in front.
type env struct {
	t      *testing.T
	ctx    context.Context
	st     *store.Store
	events *event.Log
	drv    *fake.Driver
	link   *link.Link
	eng    *plan.Engine
	iss    *issue.Service
	sec    *secret.Store
	key    secret.Key
	srv    *Server
	dir    string // data directory: plumb.db and link/
	h      http.Handler
	agents map[string]*linktest.Agent

	password string   // admin password at bootstrap
	bearers  []string // every token created
	mu       sync.Mutex
	seen     [][]byte // every response body, for the secret scan
	// buildAgents (image builds): the tuning of new build agents.
	buildTune func(*linktest.Agent)
	buildLoop bool
}

func newEnv(t *testing.T, dev bool, opts ...func(*plan.Config)) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	log := envLog
	st, err := store.Open(ctx, filepath.Join(dir, "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, ctx: ctx, dir: dir, st: st, events: event.New(st), drv: fake.New(), agents: map[string]*linktest.Agent{}}
	var eng *plan.Engine
	var iss *issue.Service
	e.key = secret.NewKey()
	if e.sec, err = secret.Open(ctx, st, e.key); err != nil {
		t.Fatal(err)
	}
	e.link, err = link.New(link.Config{Dir: filepath.Join(dir, "link"), Store: st, Events: e.events, Logger: log,
		OnReport:     func(ctx context.Context, d link.Delivery) error { return eng.HandleReport(ctx, d) },
		Sensitive:    func(ctx context.Context, tg, id string) ([]string, bool, error) { return eng.Sensitive(ctx, tg, id) },
		SecretValues: func(ctx context.Context, tg string) ([]string, error) { return eng.SecretValues(ctx, tg) }})
	if err != nil {
		t.Fatal(err)
	}
	cfg := plan.Config{Store: st, Events: e.events, Agents: e.link, Driver: e.drv, Secrets: e.sec, Logger: log,
		OnDrift:        func(ctx context.Context, tg string, rep wire.Report) error { return iss.Intake(ctx, tg, rep) },
		OnPlanDone:     func(ctx context.Context, id int64, ok bool) { iss.PlanDone(ctx, id, ok) },
		ObserveTimeout: 5 * time.Second, EnrollTimeout: 5 * time.Second, ApplyTimeout: 5 * time.Second}
	for _, o := range opts {
		o(&cfg)
	}
	eng, err = plan.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	iss = issue.New(st, e.events, eng)
	e.sec.SetUsage(eng.SecretUsage)
	e.eng, e.iss = eng, iss
	e.password, _, err = BootstrapAdmin(ctx, st, fastPasswords)
	if err != nil {
		t.Fatal(err)
	}
	e.srv = New(Config{Store: st, Events: e.events, Plans: eng, Issues: iss, Secrets: e.sec, Dev: dev, PasswordParams: fastPasswords,
		KeepAlive: 50 * time.Millisecond, Logger: log})
	e.h = e.srv.Handler()
	t.Cleanup(func() {
		cancel()
		eng.Close()
		st.Close()
	})
	return e
}

// agent starts the fake agent of a target.
func (e *env) agent(target string) *linktest.Agent {
	tok := func() (string, bool) {
		b, err := e.st.GetBinding(context.Background(), state.KindTarget, target)
		if err != nil {
			return "", false
		}
		boot, ok := e.drv.Bootstrap(driver.GuestID(b.Ref))
		return boot.Token, ok
	}
	a := linktest.New(e.link.Handler(), tok)
	e.agents[target] = a
	go a.Run(e.ctx)
	return a
}

// client is one caller of the API: a bearer token, a session cookie, or nothing.
type client struct {
	e      *env
	bearer string
	cookie *http.Cookie
	origin string // "" = the right one
}

func (e *env) anon() *client { return &client{e: e} }

func (e *env) token(tier string, risk wire.Risk) *client {
	e.t.Helper()
	b, _, err := CreateToken(e.ctx, e.st, "test-"+tier+string(risk), tier, risk, "user:admin")
	if err != nil {
		e.t.Fatal(err)
	}
	_, sec, _ := strings.Cut(b, ".")
	e.bearers = append(e.bearers, sec)
	return &client{e: e, bearer: b}
}

// admin logs in and changes the bootstrap password.
func (e *env) admin() *client {
	e.t.Helper()
	c := e.login("admin", e.password)
	if c == nil {
		e.t.Fatal("admin login failed")
	}
	code, body := c.do("PUT", "/api/v1/session/password", map[string]string{"current_password": e.password, "new_password": "a-good-long-password"})
	if code != 204 {
		e.t.Fatalf("change password: %d %s", code, body)
	}
	e.password = "a-good-long-password"
	return c
}

func (e *env) login(user, password string) *client {
	c := &client{e: e}
	rec := c.raw("POST", "/api/v1/session", map[string]string{"username": user, "password": password})
	if rec.Code != 200 {
		return nil
	}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == e.srv.cookieName() {
			c.cookie = ck
		}
	}
	return c
}

const host = "plumb.test"

func (c *client) raw(method, path string, body any) *httptest.ResponseRecorder {
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		r = bytes.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, r)
	req.Host = host
	req.RemoteAddr = "10.0.0.9:1234"
	scheme := "https"
	if c.e.srv.cfg.Dev {
		scheme = "http"
	}
	switch c.origin {
	case "":
		req.Header.Set("Origin", scheme+"://"+host)
	case "none":
	default:
		req.Header.Set("Origin", c.origin)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	rec := httptest.NewRecorder()
	c.e.h.ServeHTTP(rec, req)
	c.e.mu.Lock()
	c.e.seen = append(c.e.seen, rec.Body.Bytes()) // bodies only: Set-Cookie of a login holds the session id by design
	c.e.mu.Unlock()
	return rec
}

func (c *client) do(method, path string, body any) (int, []byte) {
	rec := c.raw(method, path, body)
	return rec.Code, rec.Body.Bytes()
}

// problemType returns the problem type of a body, or "".
func problemType(body []byte) string {
	var p struct {
		Type string `json:"type"`
	}
	json.Unmarshal(body, &p)
	return strings.TrimPrefix(p.Type, "urn:plumb:problem:")
}

// docs used by the tests.
func doc(kind, name, spec string) state.Doc {
	return state.Doc{Kind: kind, Name: name, Spec: json.RawMessage(spec)}
}

const (
	imageSpec = `{"type":"vm","url":"https://images.invalid/u.img","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}`
	motdSpec  = `{"params":{"type":"object","properties":{"text":{"type":"string","default":"hi"}}},` +
		`"resources":[{"file":{"path":"/etc/motd","content":"{{ .text }}","mode":"0644"}}]}`
)

func targetDoc(name, text string) state.Doc {
	return doc(state.KindTarget, name, `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},`+
		`"blocks":[{"use":"motd","params":{"text":"`+text+`"}}]}`)
}

// seed creates and applies one target with a running agent, through the API.
func (e *env) seed(c *client, name string) {
	e.t.Helper()
	e.agent(name)
	code, body := c.do("POST", "/api/v1/plans", map[string]any{"reason": "seed",
		"put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), doc(state.KindBlock, "motd", motdSpec), targetDoc(name, "hello")}})
	if code != 201 {
		e.t.Fatalf("seed plan: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code, body := c.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		e.t.Fatalf("seed approve: %d %s", code, body)
	}
	e.eng.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for !e.link.Online(name) {
		if time.Now().After(deadline) {
			e.t.Fatal("seed: agent not online")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
