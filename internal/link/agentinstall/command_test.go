package agentinstall

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
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

	"github.com/Bernotese/plumb/internal/wire"
)

// The install command of D-387 fails loudly (host finding F6 of T-192):
// these cases run the real command text in sh with the directory moved
// into a temp dir. test/guest/installcmd_test.go runs the same cases as
// root with the real paths in the CI guest images.

const cmdToken = "tok-T206-one-time-secret-value"

// stubScript is a served install script: it records that it ran and
// that the token file was there. Its last line is the main guard.
func stubScript(etc string) string {
	return "#!/bin/sh\nmain() {\n  test -s " + etc + "/enroll.token && echo ran > " + etc + "/ran\n}\nmain \"$@\"\n"
}

func tlsServer(t *testing.T, body func(etc string) (int, string), etc string) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wire.PathInstallScript {
			http.NotFound(w, r)
			return
		}
		code, b := body(etc)
		w.WriteHeader(code)
		w.Write([]byte(b))
	}))
	t.Cleanup(srv.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	return srv, string(ca)
}

// foreignCA is a CA that did not sign the test server (every
// httptest.NewTLSServer has the same certificate, so a second server is no
// other CA).
func foreignCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "not plumb"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// runCommand runs the command text in a person's shell (sh -c) and
// echoes the exit code afterwards, so the test sees that the person's
// shell lives on.
func runCommand(t *testing.T, cmd, path string) (int, string) {
	t.Helper()
	c := exec.Command("sh", "-c", cmd+"echo \"after=$?\"\n")
	c.Env = []string{"PATH=" + path, "HOME=" + t.TempDir()}
	out, _ := c.CombinedOutput()
	s := string(out)
	i := strings.LastIndex(s, "after=")
	if i < 0 {
		t.Fatalf("the person's shell ended with the command:\n%s", s)
	}
	code := 0
	if strings.TrimSpace(s[i+len("after="):]) != "0" {
		code = 1
	}
	return code, s[:i]
}

// toolDir links the tools the command needs, without curl.
func toolDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, x := range []string{"sh", "mkdir", "cat", "mktemp", "tail", "rm"} {
		p, err := exec.LookPath(x)
		if err != nil {
			t.Skipf("%s not installed", x)
		}
		if err := os.Symlink(p, filepath.Join(d, x)); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestInstallCommandFailsLoudly(t *testing.T) {
	need(t, "sh", "curl", "mktemp", "tail")
	sys := os.Getenv("PATH")
	otherCA := foreignCA(t)
	for _, c := range []struct {
		name    string
		body    func(etc string) (int, string)
		noCurl  bool
		wrongCA bool
		ok      bool
		want    string
	}{
		{name: "ok", body: func(etc string) (int, string) { return 200, stubScript(etc) }, ok: true},
		{name: "no curl", body: func(etc string) (int, string) { return 200, stubScript(etc) }, noCurl: true, want: "curl is missing"},
		{name: "wrong CA", body: func(etc string) (int, string) { return 200, stubScript(etc) }, wrongCA: true, want: "download of the install script failed"},
		{name: "not found", body: func(string) (int, string) { return 404, "no" }, want: "download of the install script failed"},
		{name: "cut", body: func(etc string) (int, string) {
			s := stubScript(etc)
			return 200, s[:strings.LastIndex(s, "main \"$@\"")]
		}, want: "empty or incomplete"},
		{name: "cut inside the last line", body: func(etc string) (int, string) {
			s := stubScript(etc)
			return 200, s[:len(s)-4]
		}, want: "empty or incomplete"},
		{name: "empty", body: func(string) (int, string) { return 200, "" }, want: "empty or incomplete"},
	} {
		t.Run(c.name, func(t *testing.T) {
			etc := filepath.Join(t.TempDir(), "plumb-agent")
			srv, ca := tlsServer(t, c.body, etc)
			if c.wrongCA {
				ca = otherCA
			}
			path := sys
			if c.noCurl {
				path = toolDir(t)
			}
			cmd := strings.ReplaceAll(InstallCommand(ca, srv.URL, cmdToken), "/etc/plumb-agent", etc)
			code, out := runCommand(t, cmd, path)
			if strings.Contains(out, cmdToken) {
				t.Fatalf("the token is in the output:\n%s", out)
			}
			_, tokErr := os.Stat(filepath.Join(etc, "enroll.token"))
			_, ranErr := os.Stat(filepath.Join(etc, "ran"))
			if c.ok {
				if code != 0 || tokErr != nil || ranErr != nil {
					t.Fatalf("ok: code %d, token file %v, ran %v\n%s", code, tokErr, ranErr, out)
				}
				return
			}
			if code == 0 {
				t.Fatalf("exit 0 without an install:\n%s", out)
			}
			var mine []string
			for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
				if strings.HasPrefix(l, "plumb-install:") {
					mine = append(mine, l)
				}
			}
			if len(mine) != 1 || !strings.Contains(mine[0], c.want) || !strings.Contains(mine[0], "Nothing was installed") {
				t.Fatalf("want one plumb-install line with %q, got:\n%s", c.want, out)
			}
			if tokErr == nil {
				t.Fatal("a failed try wrote the token file")
			}
			if ranErr == nil {
				t.Fatal("the script ran")
			}
		})
	}
}

// The command text: no "curl … | sh", the token only inside its here
// document, the CA pin and https only.
func TestInstallCommandText(t *testing.T) {
	cmd := InstallCommand("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n", "https://plumb.lan:8444", cmdToken)
	lines := strings.Split(cmd, "\n")
	for i, l := range lines {
		if strings.Contains(l, "| sh") || strings.Contains(l, "|sh") {
			t.Errorf("line %d pipes into sh: %q", i, l)
		}
		if strings.Contains(l, cmdToken) && (i == 0 || lines[i-1] != "cat > /etc/plumb-agent/enroll.token <<'PLUMB_TOKEN'") {
			t.Errorf("the token is outside its here document on line %d", i)
		}
		if strings.HasPrefix(l, "curl ") && !(strings.Contains(l, "--proto =https") && strings.Contains(l, "--capath /etc/plumb-agent") &&
			strings.Contains(l, "--cacert /etc/plumb-agent/ca.pem")) {
			t.Errorf("curl without the pin: %q", l)
		}
	}
	if strings.Count(cmd, cmdToken) != 1 {
		t.Fatalf("the token appears %d times", strings.Count(cmd, cmdToken))
	}
	// The served script really ends with the guard the command checks.
	if s := strings.TrimRight(string(Script()), "\n"); !strings.HasSuffix(s, "\nmain \"$@\"") {
		t.Fatal("install.sh does not end with main \"$@\"")
	}
}
