package mcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The environment of this test binary (T-186): a foreign CA in the system
// trust (SSL_CERT_FILE; Go reads it at the first use of the system pool,
// so TestMain sets it). plumb mcp must trust only its pinned CA (D-364).
var foreignCA *testCA

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newCA(name string) *testCA {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	cert, _ := x509.ParseCertificate(der)
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// serverCert is a cert for 127.0.0.1 signed by ca.
func (ca *testCA) serverCert() tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "plumb"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestMain puts a foreign CA into the system trust of this test binary
// before any test runs, and removes its temp dir at the end.
func TestMain(m *testing.M) {
	foreignCA = newCA("foreign")
	dir, err := os.MkdirTemp("", "mcp-qa-")
	if err != nil {
		panic(err)
	}
	f := filepath.Join(dir, "roots.pem")
	if err := os.WriteFile(f, foreignCA.pem, 0o600); err != nil {
		panic(err)
	}
	os.Setenv("SSL_CERT_FILE", f)
	os.Setenv("SSL_CERT_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func tlsServer(t *testing.T, ca *testCA) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(okJSON(`{"items":[]}`))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.serverCert()}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// The pin: a controller cert from the pinned CA works; one from a CA that
// the system trusts, but that is not the pin, is refused.
func TestQAOnlyThePinnedCA(t *testing.T) {
	pinned := newCA("plumb")
	good := tlsServer(t, pinned)
	c, err := NewClient(ClientConfig{URL: good.URL, Token: testToken, CAPEM: pinned.pem})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(context.Background(), http.MethodGet, "/api/v1/targets", nil, nil); err != nil {
		t.Fatalf("pinned CA: %v", err)
	}
	evil := tlsServer(t, foreignCA)
	c, err = NewClient(ClientConfig{URL: evil.URL, Token: testToken, CAPEM: pinned.pem})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(context.Background(), http.MethodGet, "/api/v1/targets", nil, nil); err == nil {
		t.Fatal("a cert of a system-trusted CA that is not the pin was accepted")
	}
}

// No proxy (D-364): the token would pass through a proxy of the
// environment. Go reads the proxy variables once per process, before this
// package's init, so the test checks the transport itself.
func TestQANoProxy(t *testing.T) {
	ca := newCA("plumb")
	for _, u := range []string{"https://plumb.lan:8443", "http://127.0.0.1:8443"} {
		c, err := NewClient(ClientConfig{URL: u, Token: testToken, CAPEM: ca.pem})
		if err != nil {
			t.Fatal(err)
		}
		tr, ok := c.http.Transport.(*http.Transport)
		if !ok || tr.Proxy != nil {
			t.Fatalf("%s: the client may use a proxy", u)
		}
	}
}

// An endless answer: the client reads at most MaxResult+1 bytes and
// returns at once (no memory growth, no wait for the 30 s timeout).
func TestQAEndlessBody(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 64<<10)
		for i := range chunk {
			chunk[i] = 'a'
		}
		for {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	c, err := NewClient(ClientConfig{URL: f.srv.URL, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Answer, 1)
	go func() {
		a, _ := c.Do(context.Background(), http.MethodGet, "/api/v1/targets", nil, nil)
		done <- a
	}()
	select {
	case a := <-done:
		if !a.Truncated || len(a.Body) != MaxResult {
			t.Fatalf("truncated %v, %d bytes", a.Truncated, len(a.Body))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the client kept reading an endless answer")
	}
}
