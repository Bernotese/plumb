package ctpack

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tlsCA is a CA with a key, for TLS tests of the first-boot script.
type tlsCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTlsCA(t testing.TB, name string) tlsCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return tlsCA{cert: c, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// server serves "agent-binary" at /agent/v1/binary over TLS with a leaf
// certificate of ca for 127.0.0.1.
func (ca tlsCA) server(t testing.TB) *httptest.Server {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent/v1/binary" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("agent-binary"))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // refused handshakes are the point
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// fetchWith runs the first-boot script with only one fetch tool (curl,
// wget or python3) on PATH, the Plumb CA pin, and /etc/plumb-agent and
// /usr/local/bin moved to temp dirs. It reports whether the binary was
// installed.
func fetchWith(t *testing.T, tool string, pin []byte, url string) (bool, string) {
	t.Helper()
	real, err := exec.LookPath(tool)
	if err != nil {
		t.Skipf("no %s", tool)
	}
	root := t.TempDir()
	etc, bin, path := filepath.Join(root, "etc"), filepath.Join(root, "bin"), filepath.Join(root, "path")
	for _, d := range []string{etc, bin, path} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(etc, "ca.pem"), pin, 0o644)
	os.WriteFile(filepath.Join(etc, "agent.json"), []byte("{}\n"), 0o644) // like the real directory
	for _, c := range []string{"mktemp", "rm", "mv", "install"} {
		p, err := exec.LookPath(c)
		if err != nil {
			t.Fatal(err)
		}
		os.Symlink(p, filepath.Join(path, c))
	}
	os.Symlink(real, filepath.Join(path, tool))
	for name, body := range map[string]string{
		"systemctl": `[ "$1" = is-active ] && exit 3; exit 0`,
		"sleep":     `exit 0`, // the 60 retries run at once
	} {
		os.WriteFile(filepath.Join(path, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755)
	}
	script := FirstBootScript(url)
	script = strings.ReplaceAll(script, "/etc/plumb-agent", etc)
	script = strings.ReplaceAll(script, "/usr/local/bin", bin)
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = []string{"PATH=" + path, "HOME=" + root}
	out, _ := cmd.CombinedOutput()
	b, err := os.ReadFile(filepath.Join(bin, "plumb-agent"))
	return err == nil && string(b) == "agent-binary", string(out)
}

// The pinned CA works with each tool, and another CA is refused.
func TestFirstBootScriptTLS(t *testing.T) {
	plumbCA, other := newTlsCA(t, "plumb"), newTlsCA(t, "other")
	srv := plumbCA.server(t)
	for _, tool := range []string{"curl", "wget", "python3"} {
		t.Run(tool, func(t *testing.T) {
			if ok, out := fetchWith(t, tool, plumbCA.pem, srv.URL); !ok {
				t.Fatalf("pinned CA: no binary\n%s", out)
			}
			if ok, _ := fetchWith(t, tool, other.pem, srv.URL); ok {
				t.Fatal("another CA was accepted")
			}
		})
	}
}
