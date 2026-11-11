package main

// plumb serve with the Proxmox driver (T-053, D-182 … D-184). No test can
// reach a real host: every test sets all PLUMB_PVE_* (clearPVE or setPVE).

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver/proxmox"
	"github.com/Bernotese/plumb/internal/secret"
)

const canary = "CANARY-5ec7e7-never-print"

// clearPVE sets every Proxmox setting to empty, so the settings of the
// dev VM (test host) never reach a test.
func clearPVE(t *testing.T) {
	t.Helper()
	for _, n := range append(pveVars, "PLUMB_PVE_CA") {
		t.Setenv(n, "")
	}
}

// setPVE sets good settings for the fake host at url.
func setPVE(t *testing.T, url, ca string) {
	t.Helper()
	clearPVE(t)
	for k, v := range map[string]string{
		"PLUMB_PVE_URL": url, "PLUMB_PVE_TOKEN_ID": "plumb@pve!plumb", secretVar: canary, "PLUMB_PVE_CA": ca,
		"PLUMB_PVE_NODE": "pve", "PLUMB_PVE_POOL": "plumb", "PLUMB_PVE_DISK": "local-lvm", "PLUMB_PVE_IMAGES": "plumb-images",
		"PLUMB_PVE_SNIPPETS": "plumb-snippets", "PLUMB_PVE_SNIPPET": "plumb-vendor.yaml", "PLUMB_PVE_BRIDGE": "vmbr0",
		"PLUMB_PVE_VMID_RANGE": "9000-9099", "PLUMB_PVE_NAMESERVER": "192.0.2.53", "PLUMB_PVE_SEARCHDOMAIN": "plumb.test",
	} {
		t.Setenv(k, v)
	}
}

// fakeHost answers the four calls of the start-up check.
type fakeHost struct {
	srv      *httptest.Server
	ca       string
	requests atomic.Int32
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	h := &fakeHost{}
	h.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.requests.Add(1)
		if r.Header.Get("Authorization") != "PVEAPIToken=plumb@pve!plumb="+canary {
			w.WriteHeader(401)
			io.WriteString(w, `{"data":null}`)
			return
		}
		var data any
		switch strings.TrimPrefix(r.URL.Path, "/api2/json") {
		case "/version":
			data = map[string]any{"version": "9.2.26"}
		case "/pools":
			data = []any{map[string]any{"poolid": "plumb", "members": []any{}}}
		case "/nodes/pve/storage":
			data = []any{
				map[string]any{"storage": "local-lvm", "content": "images,rootdir", "enabled": 1, "active": 1},
				map[string]any{"storage": "plumb-images", "content": "import", "enabled": 1, "active": 1},
				map[string]any{"storage": "plumb-snippets", "content": "snippets", "enabled": 1, "active": 1},
			}
		case "/access/permissions":
			data = map[string]any{"/sdn/zones/localnetwork/vmbr0": map[string]int{"SDN.Use": 0}} // as on the test host
		default:
			w.WriteHeader(500)
			io.WriteString(w, `{"data":null,"message":"no such call in the fake"}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(h.srv.Close)
	h.ca = filepath.Join(t.TempDir(), "pve-root-ca.pem")
	os.WriteFile(h.ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: h.srv.Certificate().Raw}), 0o644)
	return h
}

// keyFile makes a master key outside the data directory.
func keyFile(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "key")
	os.Mkdir(dir, 0o700)
	p := filepath.Join(dir, "master.key")
	if err := secret.WriteKeyFile(p, secret.NewKey()); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPVESettings(t *testing.T) {
	good := map[string]string{
		"PLUMB_PVE_URL": "https://pve.example:8006", "PLUMB_PVE_TOKEN_ID": "plumb@pve!plumb", secretVar: canary,
		"PLUMB_PVE_NODE": "pve", "PLUMB_PVE_POOL": "plumb", "PLUMB_PVE_DISK": "local-lvm", "PLUMB_PVE_IMAGES": "plumb-images",
		"PLUMB_PVE_SNIPPETS": "plumb-snippets", "PLUMB_PVE_SNIPPET": "plumb-vendor.yaml", "PLUMB_PVE_BRIDGE": "vmbr0",
		"PLUMB_PVE_VMID_RANGE": "9000-9099", "PLUMB_PVE_NAMESERVER": "192.0.2.53,2001:db8::53", "PLUMB_PVE_SEARCHDOMAIN": "plumb.test",
	}
	run := func(change map[string]string) (string, error) {
		env := map[string]string{}
		for k, v := range good {
			env[k] = v
		}
		for k, v := range change {
			env[k] = v
		}
		_, pc, err := pveSettings(func(k string) string { return env[k] })
		if err == nil && (pc.VMIDMin != 9000 || pc.VMIDMax != 9099 || len(pc.Nameservers) != 2 || pc.Bridge != "vmbr0") {
			t.Errorf("parsed %+v", pc)
		}
		if err != nil {
			return err.Error(), err
		}
		return "", nil
	}
	if msg, err := run(nil); err != nil {
		t.Fatalf("good settings: %s", msg)
	}
	// Each variable missing: named in the message.
	for _, n := range pveVars {
		msg, err := run(map[string]string{n: ""})
		var ce *errConfig
		if !strings.Contains(msg, "the Proxmox settings are missing: "+n) || !errorsAs(err, &ce) {
			t.Errorf("%s missing: %q", n, msg)
		}
	}
	for name, tc := range map[string]struct {
		change map[string]string
		want   string
	}{
		"http URL":        {map[string]string{"PLUMB_PVE_URL": "http://pve:8006"}, "PLUMB_PVE_URL must be https://host[:port]"},
		"URL with a path": {map[string]string{"PLUMB_PVE_URL": "https://pve:8006/api2/json"}, "PLUMB_PVE_URL must be"},
		"URL with user":   {map[string]string{"PLUMB_PVE_URL": "https://root@pve:8006"}, "PLUMB_PVE_URL must be"},
		"token id space":  {map[string]string{"PLUMB_PVE_TOKEN_ID": "plumb@pve!plu mb"}, "PLUMB_PVE_TOKEN_ID must be"},
		"secret with =":   {map[string]string{secretVar: canary + "=x"}, "PLUMB_PVE_SECRET must be one word"},
		// Review S1: a token id pasted with its secret never echoes the value.
		"token id with secret": {map[string]string{"PLUMB_PVE_TOKEN_ID": "plumb@pve!plumb=" + canary}, "PLUMB_PVE_TOKEN_ID must be"},
		"range reversed":       {map[string]string{"PLUMB_PVE_VMID_RANGE": "9099-9000"}, "PLUMB_PVE_VMID_RANGE must be"},
		"range below 100":      {map[string]string{"PLUMB_PVE_VMID_RANGE": "10-99"}, "PLUMB_PVE_VMID_RANGE must be"},
		"range text":           {map[string]string{"PLUMB_PVE_VMID_RANGE": "all"}, "PLUMB_PVE_VMID_RANGE must be"},
		"bridge comma":         {map[string]string{"PLUMB_PVE_BRIDGE": "vmbr0,tag=5"}, "PLUMB_PVE_BRIDGE must be"},
		"pool with /":          {map[string]string{"PLUMB_PVE_POOL": "a/b"}, "PLUMB_PVE_POOL must be"},
		"four DNS":             {map[string]string{"PLUMB_PVE_NAMESERVER": "192.0.2.1,192.0.2.2,192.0.2.3,192.0.2.4"}, "PLUMB_PVE_NAMESERVER must be"},
		"DNS name":             {map[string]string{"PLUMB_PVE_NAMESERVER": "dns.example"}, "PLUMB_PVE_NAMESERVER must be"},
		"domain space":         {map[string]string{"PLUMB_PVE_SEARCHDOMAIN": "a b"}, "PLUMB_PVE_SEARCHDOMAIN must be"},
		"control chars, long": {map[string]string{"PLUMB_PVE_NODE": "\x1b[31m" + strings.Repeat("n", 300)},
			`PLUMB_PVE_NODE must be a Proxmox name (letters, digits, . _ -), not "\x1b[31m`},
		"two bad, one missing": {map[string]string{"PLUMB_PVE_POOL": "a b", "PLUMB_PVE_BRIDGE": "x,y", "PLUMB_PVE_DISK": ""},
			"missing: PLUMB_PVE_DISK"},
	} {
		msg, _ := run(tc.change)
		if !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %q", name, msg)
		}
		if strings.Contains(msg, canary) {
			t.Errorf("%s: the secret is in %q", name, msg)
		}
		if len(msg) > 600 {
			t.Errorf("%s: message not cut (%d bytes)", name, len(msg))
		}
	}
	if msg, _ := run(map[string]string{"PLUMB_PVE_POOL": "a b", "PLUMB_PVE_BRIDGE": "x,y", "PLUMB_PVE_DISK": ""}); !strings.Contains(msg, "PLUMB_PVE_POOL must be") || !strings.Contains(msg, "PLUMB_PVE_BRIDGE must be") {
		t.Errorf("not every wrong variable is named: %q", msg)
	}
	// No CA is fine (system roots).
	if msg, err := run(map[string]string{"PLUMB_PVE_CA": ""}); err != nil {
		t.Errorf("no CA: %s", msg)
	}
}

// Check every environment-to-driver field, including settings not used by Check.
func TestPVESettingsMapping(t *testing.T) {
	h := newFakeHost(t)
	setPVE(t, h.srv.URL, h.ca)
	cc, pc, err := pveSettings(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"PLUMB_PVE_URL": cc.URL, "PLUMB_PVE_TOKEN_ID": cc.TokenID, secretVar: cc.Secret, "PLUMB_PVE_CA": cc.CAFile,
		"PLUMB_PVE_NODE": pc.Node, "PLUMB_PVE_POOL": pc.Pool, "PLUMB_PVE_DISK": pc.DiskStorage,
		"PLUMB_PVE_IMAGES": pc.ImageStorage, "PLUMB_PVE_SNIPPETS": pc.SnippetStorage, "PLUMB_PVE_SNIPPET": pc.Snippet,
		"PLUMB_PVE_BRIDGE": pc.Bridge, "PLUMB_PVE_SEARCHDOMAIN": pc.SearchDomain,
		"PLUMB_PVE_NAMESERVER": strings.Join(pc.Nameservers, ","),
	} {
		if got != os.Getenv(name) {
			t.Errorf("%s was not mapped", name)
		}
	}
	if pc.VMIDMin != 9000 || pc.VMIDMax != 9099 {
		t.Error("VMID range was not mapped")
	}
}

func errorsAs(err error, ce **errConfig) bool {
	e, ok := err.(*errConfig)
	if ok {
		*ce = e
	}
	return ok
}

// startServe runs serve until "Plumb listens on" or its end.
func startServe(t *testing.T, args ...string) (code chan int, out *lockedBuffer, errOut *lockedBuffer, cancel func()) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	out, errOut = &lockedBuffer{}, &lockedBuffer{}
	code = make(chan int, 1)
	go func() {
		c := serve(ctx, args, pw, errOut)
		pw.Close()
		code <- c
	}()
	listening := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		once := false
		for sc.Scan() {
			out.Write([]byte(sc.Text() + "\n"))
			if !once && strings.HasPrefix(sc.Text(), "Plumb listens on ") {
				once = true
				close(listening)
			}
		}
		if !once {
			close(listening)
		}
	}()
	select {
	case <-listening:
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not start or stop")
	}
	return code, out, errOut, stop
}

// A good host: serve starts with the Proxmox driver, logs the host line,
// and the secret is in no output.
func TestServeWithProxmox(t *testing.T) {
	h := newFakeHost(t)
	setPVE(t, h.srv.URL, h.ca)
	data := t.TempDir()
	masterKey := keyFile(t)
	code, out, errOut, cancel := startServe(t, "--data", data, "--key-file", masterKey,
		"--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0", "--hosts", "127.0.0.1")
	if !strings.Contains(out.String(), "Plumb listens on https://") {
		t.Fatalf("no start:\nout %s\nerr %s", out, errOut)
	}
	cancel()
	if c := <-code; c != 0 {
		t.Errorf("exit %d: %s", c, errOut)
	}
	logs := errOut.String()
	for _, want := range []string{"Proxmox host checked", "url=" + h.srv.URL, "node=pve", "version=9.2.26", "pool=plumb", "vmid_range=9000-9099"} {
		if !strings.Contains(logs, want) {
			t.Errorf("start line without %q:\n%s", want, logs)
		}
	}
	if strings.Count(logs, "Proxmox host checked") != 1 || strings.Count(out.String(), "Password:") != 1 {
		t.Errorf("expected one host line and one admin password: %s %s", out, logs)
	}
	if strings.Contains(out.String()+logs, canary) {
		t.Error("the secret is in the output")
	}
	if os.Getenv(secretVar) != "" {
		t.Error("serve left the secret in its environment")
	}
	if _, err := os.Stat(filepath.Join(data, "link", "ca.crt")); err != nil {
		t.Errorf("no Plumb CA: %v", err)
	}
	// A restart keeps the admin account and does not print another password.
	setPVE(t, h.srv.URL, h.ca)
	code, out, errOut, cancel = startServe(t, "--data", data, "--key-file", masterKey,
		"--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0", "--hosts", "127.0.0.1")
	cancel()
	if c := <-code; c != 0 || strings.Contains(out.String(), "Password:") {
		t.Errorf("restart exit %d: %s %s", c, out, errOut)
	}
}

// A failed check: exit 78, the text, nothing in --data, no password, no
// listener, no secret.
func TestServeProxmoxFailures(t *testing.T) {
	h := newFakeHost(t)
	other := otherCA(t) // did not sign h's certificate
	for name, tc := range map[string]struct {
		set  func()
		want string
		code int
	}{
		"missing settings": {func() { clearPVE(t) }, "the Proxmox settings are missing: PLUMB_PVE_URL", 78},
		"unknown issuer":   {func() { setPVE(t, h.srv.URL, other) }, "the CA in " + other + " (PLUMB_PVE_CA) did not sign it", 78},
		"wrong name": {func() {
			setPVE(t, strings.Replace(h.srv.URL, "127.0.0.1", "localhost.", 1), h.ca)
		}, "does not name localhost.", 78},
		"bad token":   {func() { setPVE(t, h.srv.URL, h.ca); t.Setenv(secretVar, "wrong") }, "Proxmox refused the API token plumb@pve!plumb", 78},
		"bad CA file": {func() { setPVE(t, h.srv.URL, filepath.Join(t.TempDir(), "none.pem")) }, "cannot read the Proxmox CA file", 78},
	} {
		t.Run(name, func(t *testing.T) {
			tc.set()
			data := filepath.Join(t.TempDir(), "data")
			var out, errOut bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c := serve(ctx, []string{"--data", data, "--key-file", keyFile(t), "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0"}, &out, &errOut)
			all := out.String() + errOut.String()
			if c != tc.code || !strings.Contains(all, tc.want) {
				t.Errorf("exit %d, want %d with %q:\n%s", c, tc.code, tc.want, all)
			}
			if _, err := os.Stat(data); !os.IsNotExist(err) {
				t.Errorf("the data directory was made: %v", err)
			}
			if strings.Contains(all, "Password") || strings.Contains(all, "listens on") {
				t.Errorf("a failed start went on:\n%s", all)
			}
			if strings.Contains(all, canary) {
				t.Error("the secret is in the output")
			}
		})
	}
}

// --dev sends no request to PLUMB_PVE_URL, even with the settings set (D-039).
func TestDevIgnoresProxmox(t *testing.T) {
	h := newFakeHost(t)
	setPVE(t, h.srv.URL, h.ca)
	code, out, errOut, cancel := startServe(t, "--dev", "--data", t.TempDir(), "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0")
	cancel()
	<-code
	if !strings.Contains(out.String(), "Plumb listens on") {
		t.Fatalf("dev did not start: %s %s", out, errOut)
	}
	if n := h.requests.Load(); n != 0 {
		t.Errorf("--dev sent %d requests to PLUMB_PVE_URL", n)
	}
	if code, _, _ := serveCode(t, "--dev", "--driver", "proxmox"); code != 2 {
		t.Errorf("--dev --driver proxmox: %d", code)
	}
}

func serveCode(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	c := serve(context.Background(), args, &out, &errOut)
	return c, out.String(), errOut.String()
}

// otherCA writes a new self-signed CA that signed nothing.
func otherCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "another CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "other-ca.pem")
	os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	return p
}

// "No answer" stays exit 1 (systemd starts serve again); the rest is 78.
func TestStartErrorExitCodes(t *testing.T) {
	cc := proxmox.ClientConfig{URL: "https://pve.example:8006", TokenID: "plumb@pve!plumb", Secret: canary}
	var ce *errConfig
	ue := startError(&proxmox.UnreachableError{URL: cc.URL, Err: errors.New("connection refused")}, cc)
	if errors.As(ue, &ce) || !strings.Contains(ue.Error(), "cannot reach Proxmox at https://pve.example:8006: connection refused") {
		t.Errorf("unreachable: %v", ue)
	}
	for _, err := range []error{
		&proxmox.VersionError{Version: "8.4.1", Readable: true},
		&proxmox.HostProblems{Node: "pve", Problems: []proxmox.HostProblem{{Kind: proxmox.ProblemPool, ID: "plumb"},
			{Kind: proxmox.ProblemStorageContent, ID: "local", Content: "import,iso", Rule: `exactly "import"`}}},
		&proxmox.APIError{Status: 401},
	} {
		got := startError(err, cc)
		if !errors.As(got, &ce) || strings.Contains(got.Error(), canary) {
			t.Errorf("%T: %v", err, got)
		}
	}
	got := startError(&proxmox.HostProblems{Node: "pve", Problems: []proxmox.HostProblem{
		{Kind: proxmox.ProblemPool, ID: "plumb"}, {Kind: proxmox.ProblemNode, ID: "pve"},
		{Kind: proxmox.ProblemStorageMissing, ID: "plumb-images"},
		{Kind: proxmox.ProblemStorageContent, ID: "local", Content: "import,iso", Rule: `exactly "import"`},
		{Kind: proxmox.ProblemBridge, ID: "vmbr0"}}}, cc).Error()
	for _, want := range []string{"pool plumb does not exist, or the token has no Pool.Audit right on /pool/plumb",
		"node pve does not exist on this host", "storage plumb-images is missing on node pve, is not active, or the token has no right on it",
		`storage local has the content "import,iso"; it must have exactly "import" (D-081)`, "the token has no SDN.Use right on bridge vmbr0"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

// Preserve the certificate cause and the actionable error text.
func TestStartErrorCertificateTime(t *testing.T) {
	cc := proxmox.ClientConfig{URL: "https://127.0.0.1:8006", Secret: canary}
	for _, start := range []time.Time{time.Now().Add(-2 * time.Hour), time.Now().Add(time.Hour)} {
		cert := &x509.Certificate{NotBefore: start, NotAfter: start.Add(time.Hour)}
		err := startError(&url.Error{Op: "Get", URL: cc.URL, Err: x509.CertificateInvalidError{
			Cert: cert, Reason: x509.Expired}}, cc)
		var ce *errConfig
		for _, want := range []string{cc.URL, "is not valid at this time", cert.NotBefore.UTC().Format("2006-01-02 15:04 MST"),
			cert.NotAfter.UTC().Format("2006-01-02 15:04 MST"), "check the clock of the LXC and the certificate of the node"} {
			if !errors.As(err, &ce) || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), canary) {
				t.Errorf("want config error containing %q without secret: %v", want, err)
			}
		}
	}
}

// tlsHost is a TLS server with its own certificate, a CA file for it, and a
// count of the TLS connections it got.
func tlsHost(t *testing.T, notBefore, notAfter time.Time) (url, ca string, conns *atomic.Int32) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test node CA"},
		NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: time.Now().Add(48 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "pve"},
		NotBefore: notBefore, NotAfter: notAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request passed TLS: %s", r.URL.Path)
	}))
	conns = &atomic.Int32{}
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	ca = filepath.Join(t.TempDir(), "node-ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644)
	return srv.URL, ca, conns
}

// runFailing runs serve and checks a failed start: the exit code, the text,
// an empty --data and no password.
func runFailing(t *testing.T, code int, want string) string {
	t.Helper()
	data := filepath.Join(t.TempDir(), "data")
	c, out, errOut := serveCode(t, "--data", data, "--key-file", keyFile(t), "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0")
	all := out + errOut
	if c != code || !strings.Contains(all, want) {
		t.Errorf("exit %d, want %d with %q:\n%s", c, code, want, all)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Errorf("the data directory was made: %v", err)
	}
	if strings.Contains(all, "Password") || strings.Contains(all, canary) {
		t.Errorf("password or secret in the output:\n%s", all)
	}
	return all
}

// QA gap: an expired node certificate, end to end: exit 78 with the dates.
func TestServeExpiredCertificate(t *testing.T) {
	url, ca, _ := tlsHost(t, time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
	setPVE(t, url, ca)
	runFailing(t, 78, "is not valid at this time (")
}

// QA gap: a TLS failure is never tried again: one connection only.
func TestServeTLSFailureNotRetried(t *testing.T) {
	url, _, conns := tlsHost(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	setPVE(t, url, otherCA(t))
	pveStartWait, pveStartPoll = 2*time.Second, 10*time.Millisecond
	t.Cleanup(func() { pveStartWait, pveStartPoll = 0, 0 })
	runFailing(t, 78, "did not sign it")
	if n := conns.Load(); n != 1 {
		t.Errorf("TLS connections = %d, want 1 (no retry)", n)
	}
}

// QA gap: no answer at all, end to end: exit 1 (systemd starts it again).
func TestServeNoAnswer(t *testing.T) {
	h := newFakeHost(t)
	url := h.srv.URL
	h.srv.Close()
	setPVE(t, url, h.ca)
	pveStartWait, pveStartPoll = 100*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { pveStartWait, pveStartPoll = 0, 0 })
	runFailing(t, 1, "cannot reach Proxmox at "+url)
}
