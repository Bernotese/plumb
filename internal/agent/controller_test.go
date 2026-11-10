package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// testCA is a CA that signs a server certificate for 127.0.0.1.
type testCA struct {
	pem  string
	leaf tls.Certificate
}

func newCA(t *testing.T) testCA {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Plumb test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "plumb"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return testCA{
		pem:  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		leaf: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
	}
}

// controller is a fake agent listener.
type controller struct {
	srv *httptest.Server

	mu          sync.Mutex
	hits        int             // all requests
	tokens      map[string]bool // unused valid tokens
	creds       map[string]bool // valid credentials
	enrolled    []string        // tokens that enrolled
	enrollTries int
	rejected    map[string]int // 401 answers per credential
	pollFail    int            // answer this many polls with failCode
	failCode    int            // default 503
	retryAfter  string         // Retry-After of a failed poll or report
	reportFail  int            // answer this many reports with failCode
	pollTimes   []time.Time
	reports     []wire.Report
	commands    chan wire.Command
}

func newController(t *testing.T, ca testCA, tokens ...string) *controller {
	c := &controller{tokens: map[string]bool{}, creds: map[string]bool{}, rejected: map[string]int{}, commands: make(chan wire.Command, 16)}
	for _, tok := range tokens {
		c.tokens[tok] = true
	}
	c.srv = httptest.NewUnstartedServer(http.HandlerFunc(c.serve))
	c.srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.leaf}}
	c.srv.Config.ErrorLog = log.New(io.Discard, "", 0) // TLS failures are expected in TestPinnedCA
	c.srv.StartTLS()
	t.Cleanup(c.srv.Close)
	return c
}

func (c *controller) serve(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.hits++
	c.mu.Unlock()
	if r.URL.Path == wire.PathEnroll {
		var req wire.EnrollRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.enrollTries++
		if !c.tokens[req.Token] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		delete(c.tokens, req.Token) // single use
		c.enrolled = append(c.enrolled, req.Token)
		cred := fmt.Sprintf("cred-%d", len(c.enrolled))
		c.creds[cred] = true
		_ = json.NewEncoder(w).Encode(wire.EnrollResponse{AgentID: "agent-1", Credential: cred})
		return
	}
	cred := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	c.mu.Lock()
	ok := c.creds[cred]
	if !ok {
		c.rejected[cred]++
	}
	c.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	failWith := func() {
		code := c.failCode
		if code == 0 {
			code = http.StatusServiceUnavailable
		}
		if c.retryAfter != "" {
			w.Header().Set("Retry-After", c.retryAfter)
		}
		w.WriteHeader(code)
	}
	switch r.URL.Path {
	case wire.PathPoll:
		c.mu.Lock()
		c.pollTimes = append(c.pollTimes, time.Now())
		fail := c.pollFail > 0
		if fail {
			c.pollFail--
			failWith()
		}
		c.mu.Unlock()
		if fail {
			return
		}
		select {
		case cmd := <-c.commands:
			_ = json.NewEncoder(w).Encode(cmd)
		case <-time.After(50 * time.Millisecond):
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	case wire.PathReport:
		var rep wire.Report
		if err := json.NewDecoder(io.LimitReader(r.Body, wire.MaxReportBytes)).Decode(&rep); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		if c.reportFail > 0 && rep.Type == wire.ReportResult {
			c.reportFail--
			failWith()
			c.mu.Unlock()
			return
		}
		c.reports = append(c.reports, rep)
		c.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// reportsOf returns the reports of one type.
func (c *controller) reportsOf(typ wire.ReportType) []wire.Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []wire.Report
	for _, r := range c.reports {
		if r.Type == typ {
			out = append(out, r)
		}
	}
	return out
}

// result waits for the result report of a command.
func (c *controller) result(t *testing.T, id string) wire.Report {
	t.Helper()
	var rep wire.Report
	waitFor(t, "result of "+id, func() bool {
		for _, r := range c.reportsOf(wire.ReportResult) {
			if r.CommandID == id {
				rep = r
				return true
			}
		}
		return false
	})
	return rep
}

// driftItems returns every drift item reported for a resource.
func (c *controller) driftItems(id string) []wire.Item {
	var out []wire.Item
	for _, r := range c.reportsOf(wire.ReportDrift) {
		for _, it := range r.Items {
			if it.Resource == id {
				out = append(out, it)
			}
		}
	}
	return out
}

func (c *controller) send(t *testing.T, id string, typ wire.CommandType, body any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	c.commands <- wire.Command{ID: id, Type: typ, Body: b}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// runner is a fake command runner for service resources. A unit is
// loaded, active and enabled; restart calls are recorded with a check.
type runner struct {
	mu        sync.Mutex
	calls     [][]string
	onRestart func()
	onShow    func() // runs before "systemctl show" answers
}

func (r *runner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{name}, args...))
	hook, show := r.onRestart, r.onShow
	r.mu.Unlock()
	if name == "systemctl" && len(args) > 0 {
		switch args[0] {
		case "show":
			if show != nil {
				show()
			}
			return []byte("LoadState=loaded\nActiveState=active\nUnitFileState=enabled\n"), nil
		case "restart":
			if hook != nil {
				hook()
			}
		}
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected command %s %v", name, args)
}

func (r *runner) count(words ...string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if len(c) >= len(words) && slices.Equal(c[:len(words)], words) {
			n++
		}
	}
	return n
}

// testAgent is a running agent against a fake controller.
type testAgent struct {
	a      *Agent
	root   string
	cfg    Config
	runner *runner
	cancel context.CancelFunc
	done   chan struct{}
}

func startAgent(t *testing.T, ctl *controller, ca testCA, token string) *testAgent {
	t.Helper()
	root := t.TempDir()
	uid, gid := os.Getuid(), os.Getgid()
	write(t, filepath.Join(root, "etc/passwd"), fmt.Sprintf("root:x:%d:%d:root:/root:/bin/bash\n", uid, gid))
	write(t, filepath.Join(root, "etc/group"), fmt.Sprintf("root:x:%d:\n", gid))
	write(t, filepath.Join(root, "etc/os-release"), "ID=debian\nVERSION_ID=\"13\"\n")
	write(t, filepath.Join(root, "token"), token+"\n")
	if err := os.MkdirAll(filepath.Join(root, "srv"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{URL: ctl.srv.URL, CA: ca.pem, TokenFile: filepath.Join(root, "token"), StateDir: filepath.Join(t.TempDir(), "state"), FullCheck: Duration(time.Hour)}
	r := &runner{}
	a, err := New(Options{
		Config: cfg, Root: root, Runner: r, Version: "test", Hostname: "web1",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Debounce: 100 * time.Millisecond, PollWait: 1,
		BackoffMin: 10 * time.Millisecond, BackoffMax: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ta := &testAgent{a: a, root: root, cfg: a.cfg, runner: r, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(ta.done)
		_ = a.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-ta.done
	})
	return ta
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileRes(block, path string, attrs map[string]any) wire.Resource {
	a := map[string]any{"path": path}
	for k, v := range attrs {
		a[k] = v
	}
	return wire.Resource{ID: wire.ID("file", path), Type: "file", Block: block, Attrs: a}
}
