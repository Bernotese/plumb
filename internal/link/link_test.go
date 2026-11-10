package link

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// clock is a settable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fixture struct {
	t       *testing.T
	link    *Link
	st      *store.Store
	events  *event.Log
	clock   *clock
	srv     http.Handler
	mu      sync.Mutex
	got     []Delivery
	failNow bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fixture{t: t, st: st, events: event.New(st), clock: &clock{t: time.Now().UTC().Truncate(time.Second)}}
	st.Now = f.clock.now
	f.link, err = New(Config{
		Dir: filepath.Join(t.TempDir(), "link"), Hosts: []string{"127.0.0.1", "plumb.lan"},
		Store: st, Events: f.events, AgentBinary: []byte("agent-binary"), Now: f.clock.now,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnReport: func(_ context.Context, d Delivery) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.failNow {
				f.failNow = false
				return errors.New("db busy")
			}
			f.got = append(f.got, d)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.srv = f.link.Handler()
	return f
}

// do sends one request to the handler and returns status and body.
func (f *fixture) do(method, path, cred string, body any) (int, []byte) {
	f.t.Helper()
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
	req.RemoteAddr = "10.0.0.7:5000"
	if cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (f *fixture) token(target string) string {
	f.t.Helper()
	tok, exp, err := f.link.NewEnrollToken(context.Background(), target)
	if err != nil {
		f.t.Fatal(err)
	}
	if len(tok) < 43 || !exp.Equal(f.clock.now().Add(15*time.Minute)) {
		f.t.Fatalf("token %q expires %v", tok, exp)
	}
	return tok
}

func (f *fixture) enroll(tok string) (int, wire.EnrollResponse, []byte) {
	f.t.Helper()
	code, body := f.do("POST", wire.PathEnroll, "", wire.EnrollRequest{Token: tok, Hostname: "web1", Version: "0.1"})
	var resp wire.EnrollResponse
	json.Unmarshal(body, &resp)
	return code, resp, body
}

func (f *fixture) agent(target string) string {
	f.t.Helper()
	code, resp, body := f.enroll(f.token(target))
	if code != 200 || resp.Credential == "" {
		f.t.Fatalf("enroll %s: %d %s", target, code, body)
	}
	return resp.Credential
}

func TestCAFiles(t *testing.T) {
	f := newFixture(t)
	dir := f.link.cfg.Dir
	for name, mode := range map[string]os.FileMode{caKeyFile: 0o600, serverKeyFile: 0o600, caCertFile: 0o644, serverCertFile: 0o644} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != mode {
			t.Errorf("%s: mode %v, %v; want %v", name, info.Mode().Perm(), err, mode)
		}
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", info.Mode().Perm())
	}
	// A restart keeps the CA and the certificate.
	l2, err := New(Config{Dir: dir, Hosts: []string{"plumb.lan", "127.0.0.1"}, Store: f.st, Now: f.clock.now})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(l2.ca.CertPEM, f.link.ca.CertPEM) || !bytes.Equal(l2.cert.Certificate[0], f.link.cert.Certificate[0]) {
		t.Error("restart made a new CA or certificate")
	}
	// New host names give a new certificate from the same CA.
	l3, err := New(Config{Dir: dir, Hosts: []string{"other.lan"}, Store: f.st, Now: f.clock.now})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(l3.ca.CertPEM, f.link.ca.CertPEM) || bytes.Equal(l3.cert.Certificate[0], f.link.cert.Certificate[0]) {
		t.Error("host change: want same CA, new certificate")
	}
	// A key file that others can read is refused.
	os.Chmod(filepath.Join(dir, caKeyFile), 0o644)
	if _, err := New(Config{Dir: dir, Store: f.st}); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Errorf("readable key: %v", err)
	}
}

// The listener uses its own TLS port; agents trust only the Plumb CA.
func TestTLSListener(t *testing.T) {
	f := newFixture(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.link.ServeListener(ctx, ln) }()
	url := "https://" + ln.Addr().String() + wire.PathEnroll

	pinned := &http.Client{Transport: &http.Transport{TLSClientConfig: f.link.ClientTLSConfig()}}
	tok := f.token("web1")
	raw, _ := json.Marshal(wire.EnrollRequest{Token: tok})
	resp, err := pinned.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("enroll over TLS: %d", resp.StatusCode)
	}
	// A client with the system roots does not trust the listener.
	if _, err := http.Post(url, "application/json", bytes.NewReader(raw)); err == nil {
		t.Error("system roots trusted the Plumb certificate")
	}
	old := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: f.link.ca.Pool(), MaxVersion: tls.VersionTLS12}}}
	if _, err := old.Post(url, "application/json", bytes.NewReader(raw)); err == nil {
		t.Error("TLS 1.2 accepted")
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Serve: %v", err)
	}
}

func TestEnrollTokenRules(t *testing.T) {
	f := newFixture(t)
	invalid := func(name, tok string) {
		t.Helper()
		code, _, body := f.enroll(tok)
		if code != 401 || string(body) != `{"error":"invalid enrollment token"}`+"\n" {
			t.Errorf("%s: %d %s; want the one invalid-token answer", name, code, body)
		}
	}
	invalid("unknown", "nope")
	invalid("empty", "")

	tok := f.token("web1")
	if code, _, body := f.enroll(tok); code != 200 {
		t.Fatalf("first use: %d %s", code, body)
	}
	invalid("reuse", tok)

	expired := f.token("web2")
	f.clock.add(15*time.Minute + time.Second)
	invalid("expired", expired)

	// A new token replaces the old one.
	first := f.token("web3")
	second := f.token("web3")
	invalid("replaced", first)
	code, resp, _ := f.enroll(second)
	if code != 200 {
		t.Fatalf("new token: %d", code)
	}
	// The token decides the target; the agent cannot choose it.
	if a, err := f.st.AgentByID(context.Background(), resp.AgentID); err != nil || a.Target != "web3" {
		t.Errorf("agent = %+v, %v", a, err)
	}
	// Only hashes are stored.
	var stored int
	f.st.Now = f.clock.now
	for _, s := range []string{tok, second, resp.Credential} {
		if strings.Contains(dumpDB(t, f.st), s) {
			stored++
		}
	}
	if stored != 0 {
		t.Errorf("%d secrets found in clear text in the database", stored)
	}
	// Events hold no secret.
	ch := f.events.Subscribe(context.Background(), 0).C
	var types []string
	for range 7 { // 4 token events, 3 enrollments… read what is there
		select {
		case e := <-ch:
			types = append(types, e.Type)
			for _, s := range []string{tok, second, resp.Credential} {
				if strings.Contains(string(e.Data), s) {
					t.Errorf("event %s holds a secret", e.Type)
				}
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	if !strings.Contains(strings.Join(types, ","), "agent.enrolled") {
		t.Errorf("events = %v", types)
	}
}

// dumpDB returns all text of the tables that hold secrets' hashes.
func dumpDB(t *testing.T, st *store.Store) string {
	t.Helper()
	var b strings.Builder
	for _, target := range []string{"web1", "web2", "web3"} {
		if a, err := st.AgentByTarget(context.Background(), target); err == nil {
			b.WriteString(a.ID + a.CredentialHash + a.Hostname)
		}
	}
	return b.String()
}

func TestReenrollmentInvalidatesCredential(t *testing.T) {
	f := newFixture(t)
	cred := f.agent("web1")
	if code, _ := f.do("GET", wire.PathPoll, cred, nil); code != 204 {
		t.Fatalf("poll: %d", code)
	}
	newTok := f.token("web1")
	if code, _ := f.do("GET", wire.PathPoll, cred, nil); code != 401 {
		t.Errorf("old credential after a new token: %d, want 401", code)
	}
	if f.link.Online("web1") {
		t.Error("target still online after re-enrollment started")
	}
	code, resp, _ := f.enroll(newTok)
	if code != 200 {
		t.Fatal(code)
	}
	if code, _ := f.do("GET", wire.PathPoll, resp.Credential, nil); code != 204 {
		t.Errorf("new credential: %d", code)
	}
	if code, _ := f.do("GET", wire.PathPoll, cred, nil); code != 401 {
		t.Errorf("old credential after re-enrollment: %d", code)
	}
}

func TestCredentialRules(t *testing.T) {
	f := newFixture(t)
	a := f.agent("web1")
	b := f.agent("web2")
	id, sec, _ := strings.Cut(a, ".")
	_, secB, _ := strings.Cut(b, ".")
	for name, cred := range map[string]string{
		"none":            "",
		"garbage":         "xyz",
		"wrong secret":    id + ".AAAA",
		"secret of other": id + "." + secB,
		"unknown id":      "nobody." + sec,
		"too long":        id + "." + strings.Repeat("a", 300),
		"empty secret":    id + ".",
	} {
		if code, _ := f.do("GET", wire.PathPoll, cred, nil); code != 401 {
			t.Errorf("%s: %d, want 401", name, code)
		}
		if code, _ := f.do("POST", wire.PathReport, cred, wire.Report{Type: wire.ReportDrift}); code != 401 {
			t.Errorf("%s report: %d, want 401", name, code)
		}
	}

	// A command for web2 never reaches the agent of web1.
	cmdB, _ := f.link.Send("web2", wire.CommandObserve, wire.ObserveBody{})
	if code, _ := f.do("GET", wire.PathPoll, a, nil); code != 204 {
		t.Errorf("web1 got a command of web2: %d", code)
	}
	// web1 cannot answer web2's command, even with its id.
	res := wire.Report{Type: wire.ReportResult, CommandID: cmdB}
	if code, _ := f.do("POST", wire.PathReport, a, res); code != 409 {
		t.Errorf("result for another target's command: %d, want 409", code)
	}
	code, body := f.do("GET", wire.PathPoll, b, nil)
	var cmd wire.Command
	json.Unmarshal(body, &cmd)
	if code != 200 || cmd.ID != cmdB {
		t.Fatalf("web2 poll: %d %s", code, body)
	}
	if code, _ := f.do("POST", wire.PathReport, b, res); code != 204 {
		t.Errorf("own result: %d", code)
	}
	if len(f.got) != 1 || f.got[0].Target != "web2" {
		t.Errorf("deliveries = %+v", f.got)
	}
}

func TestLongPollAndOnline(t *testing.T) {
	f := newFixture(t)
	cred := f.agent("web1")
	if f.link.Online("web1") {
		t.Error("online before the first poll")
	}
	start := time.Now()
	if code, _ := f.do("GET", wire.PathPoll+"?wait=1", cred, nil); code != 204 {
		t.Fatalf("empty poll: %d", code)
	}
	if d := time.Since(start); d < 900*time.Millisecond {
		t.Errorf("long poll returned after %v", d)
	}
	if !f.link.Online("web1") {
		t.Error("offline after a poll")
	}
	f.clock.add(89 * time.Second)
	if !f.link.Online("web1") {
		t.Error("offline after 89 s")
	}
	f.clock.add(2 * time.Second)
	if f.link.Online("web1") {
		t.Error("online after 91 s")
	}

	// A command sent during the wait ends the poll at once.
	go func() {
		time.Sleep(100 * time.Millisecond)
		f.link.Send("web1", wire.CommandWatch, wire.WatchBody{})
	}()
	start = time.Now()
	code, body := f.do("GET", wire.PathPoll+"?wait=30", cred, nil)
	var cmd wire.Command
	json.Unmarshal(body, &cmd)
	if code != 200 || cmd.Type != wire.CommandWatch || time.Since(start) > 5*time.Second {
		t.Errorf("poll = %d %s after %v", code, body, time.Since(start))
	}
	// Commands come in order, each once.
	first, _ := f.link.Send("web1", wire.CommandObserve, wire.ObserveBody{})
	second, _ := f.link.Send("web1", wire.CommandObserve, wire.ObserveBody{})
	for _, want := range []string{first, second} {
		_, body := f.do("GET", wire.PathPoll, cred, nil)
		json.Unmarshal(body, &cmd)
		if cmd.ID != want {
			t.Errorf("got command %s, want %s", cmd.ID, want)
		}
	}
	if code, _ := f.do("GET", wire.PathPoll+"?wait=-1", cred, nil); code != 400 {
		t.Errorf("bad wait: %d", code)
	}
	if _, err := f.link.Send("web1", "reboot", nil); err == nil {
		t.Error("unknown command type: want an error")
	}
}

func TestReports(t *testing.T) {
	f := newFixture(t)
	cred := f.agent("web1")
	// The controller marks content as sensitive in what it sends.
	cmdID, _ := f.link.Send("web1", wire.CommandApply, wire.ApplyBody{PlanID: 1, MaxRisk: wire.RiskReversible, Resources: []wire.Resource{
		{ID: "file:/etc/app.conf", Type: "file", Block: "app", Attrs: map[string]any{"path": "/etc/app.conf"}, Sensitive: []string{"content"}},
	}})
	f.do("GET", wire.PathPoll, cred, nil)

	t.Run("oversized", func(t *testing.T) {
		big := []byte(`{"type":"drift","items":[{"resource":"file:/x","status":"differs","output":"` + strings.Repeat("a", wire.MaxReportBytes) + `"}]}`)
		if code, _ := f.do("POST", wire.PathReport, cred, big); code != 413 {
			t.Errorf("oversized report: %d, want 413", code)
		}
	})
	for name, raw := range map[string]string{
		"not JSON":            `{`,
		"unknown field":       `{"type":"drift","items":[{"resource":"file:/x","status":"differs"}],"x":1}`,
		"two values":          `{"type":"hello","hello":{}} {}`,
		"unknown type":        `{"type":"shell"}`,
		"bad status":          `{"type":"drift","items":[{"resource":"file:/x","status":"pending"}]}`,
		"bad resource":        `{"type":"drift","items":[{"resource":"nokey","status":"differs"}]}`,
		"bad risk":            `{"type":"drift","items":[{"resource":"file:/x","status":"differs","changes":[{"attr":"mode","risk":"none"}]}]}`,
		"empty attr":          `{"type":"drift","items":[{"resource":"file:/x","status":"differs","changes":[{"attr":"","risk":"read"}]}]}`,
		"drift without items": `{"type":"drift"}`,
		"result without id":   `{"type":"result"}`,
		"hello with items":    `{"type":"hello","hello":{},"items":[{"resource":"file:/x","status":"unchanged"}]}`,
		"output too long":     `{"type":"drift","items":[{"resource":"file:/x","status":"differs","output":"` + strings.Repeat("a", maxOutput+1) + `"}]}`,
	} {
		if code, _ := f.do("POST", wire.PathReport, cred, []byte(raw)); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	if len(f.got) != 0 {
		t.Fatalf("invalid reports reached the handler: %+v", f.got)
	}

	// Values of sensitive attributes never leave the link.
	leak := wire.Report{Type: wire.ReportResult, CommandID: cmdID, Items: []wire.Item{{
		Resource: "file:/etc/app.conf", Status: wire.StatusApplied, Changes: []wire.Change{
			{Attr: "content", From: "old-secret", To: "new-secret", Risk: wire.RiskReversible},
			{Attr: "mode", From: "0600", To: "0640", Risk: wire.RiskReversible, Sensitive: true},
			{Attr: "owner", From: "a", To: "b", Risk: wire.RiskReversible},
		}}}}
	f.failNow = true
	if code, _ := f.do("POST", wire.PathReport, cred, leak); code != 503 {
		t.Fatalf("handler error: %d, want 503", code)
	}
	if code, _ := f.do("POST", wire.PathReport, cred, leak); code != 204 {
		t.Fatalf("retry after 503: %d", code)
	}
	got := f.got[0].Report.Items[0].Changes
	raw, _ := json.Marshal(f.got[0])
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "0640") {
		t.Errorf("a sensitive value reached the handler: %s", raw)
	}
	if !got[0].Sensitive || !got[1].Sensitive || got[2].To != "b" {
		t.Errorf("changes = %+v", got)
	}
	// The result is consumed.
	if code, _ := f.do("POST", wire.PathReport, cred, leak); code != 409 {
		t.Errorf("second result for one command: %d, want 409", code)
	}
	// Hello updates the agent info.
	hello := wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "0.2", Hostname: "web1.lan", OS: "ubuntu 26.04"}}
	if code, _ := f.do("POST", wire.PathReport, cred, hello); code != 204 {
		t.Fatalf("hello: %d", code)
	}
	if a, _ := f.st.AgentByTarget(context.Background(), "web1"); a.Version != "0.2" || a.Hostname != "web1.lan" {
		t.Errorf("agent after hello = %+v", a)
	}
}

func TestBinary(t *testing.T) {
	f := newFixture(t)
	code, body := f.do("GET", wire.PathBinary, "", nil)
	if code != 200 || string(body) != "agent-binary" {
		t.Fatalf("binary: %d %q", code, body)
	}
	// Burst of 3 per client, then 429 until the bucket refills.
	limited := false
	for range 5 {
		if code, _ := f.do("GET", wire.PathBinary, "", nil); code == 429 {
			limited = true
		}
	}
	if !limited {
		t.Error("no rate limit on the binary")
	}
	f.clock.add(time.Minute)
	if code, _ := f.do("GET", wire.PathBinary, "", nil); code != 200 {
		t.Errorf("after refill: %d", code)
	}
	f.link.cfg.AgentBinary = nil
	f.clock.add(time.Minute)
	if code, _ := f.do("GET", wire.PathBinary, "", nil); code != 404 {
		t.Errorf("no binary: %d", code)
	}
}
