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
	c := exec.Command("sh", "-c", "umask 022\n"+cmd+"echo \"after=$? umask=$(umask)\"\n")
	c.Env = []string{"PATH=" + path, "HOME=" + t.TempDir()}
	out, _ := c.CombinedOutput()
	s := string(out)
	i := strings.LastIndex(s, "after=")
	if i < 0 {
		t.Fatalf("the person's shell ended with the command:\n%s", s)
	}
	tail := strings.Fields(s[i+len("after="):])
	if len(tail) != 2 || tail[1] != "umask=0022" {
		t.Fatalf("the person's shell changed (umask leak?): %q", s[i:])
	}
	code := 0
	if tail[0] != "0" {
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
		// scriptRan: the failure is inside install.sh (it printed its own
		// plumb-install: line; the token file is its business).
		scriptRan bool
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
		{name: "install.sh fails", body: func(string) (int, string) {
			return 200, "#!/bin/sh\nmain() {\n  echo 'plumb-install: the agent unit did not start' >&2; exit 1\n}\nmain \"$@\"\n"
		}, want: "did not start", scriptRan: true},
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
			text, err := InstallCommand(ca, srv.URL, cmdToken)
			if err != nil {
				t.Fatal(err)
			}
			cmd := strings.ReplaceAll(text, "/etc/plumb-agent", etc)
			code, out := runCommand(t, cmd, path)
			if strings.Contains(out, cmdToken) {
				t.Fatalf("the token is in the output:\n%s", out)
			}
			_, tokErr := os.Stat(filepath.Join(etc, "enroll.token"))
			_, ranErr := os.Stat(filepath.Join(etc, "ran"))
			_, caErr := os.Stat(filepath.Join(etc, "ca.pem"))
			if c.ok {
				if code != 0 || tokErr != nil || ranErr != nil || caErr != nil {
					t.Fatalf("ok: code %d, token file %v, ran %v, ca %v\n%s", code, tokErr, ranErr, caErr, out)
				}
				return
			}
			// L1 of #307: a failed run leaves no file of the command.
			if caErr == nil {
				t.Fatal("a failed run left ca.pem")
			}
			if c.scriptRan {
				if code == 0 || !strings.Contains(out, c.want) {
					t.Fatalf("install.sh failure: code %d\n%s", code, out)
				}
				return
			}
			if _, err := os.Stat(etc); err == nil {
				t.Fatalf("a refused run made %s", etc)
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
	cmd, err := InstallCommand("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n", "https://plumb.lan:8444", cmdToken)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cmd, "(\numask 077\n") || !strings.HasSuffix(cmd, ")\n") {
		t.Fatalf("the command is not one subshell that sets its own umask:\n%s", cmd)
	}
	lines := strings.Split(cmd, "\n")
	for i, l := range lines {
		if strings.Contains(l, "| sh") || strings.Contains(l, "|sh") {
			t.Errorf("line %d pipes into sh: %q", i, l)
		}
		if strings.Contains(l, cmdToken) && (i == 0 || lines[i-1] != "cat > /etc/plumb-agent/enroll.token <<'PLUMB_TOKEN'") {
			t.Errorf("the token is outside its here document on line %d", i)
		}
		if strings.HasPrefix(l, "curl ") && !(strings.Contains(l, "--proto =https") && strings.Contains(l, `--capath "$d"`) &&
			strings.Contains(l, `--cacert "$d/ca.pem"`)) {
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

// L3 of #307: the function refuses input that could break its quoting,
// whoever calls it.
func TestInstallCommandRefusesBadInput(t *testing.T) {
	ca := "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"
	for name, c := range map[string][3]string{
		"quote in URL":     {ca, "https://plumb.lan':8444", cmdToken},
		"space in URL":     {ca, "https://plumb.lan :8444", cmdToken},
		"newline in URL":   {ca, "https://plumb.lan\n:8444", cmdToken},
		"dollar in URL":    {ca, "https://$(id).lan:8444", cmdToken},
		"http":             {ca, "http://plumb.lan:8444", cmdToken},
		"path":             {ca, "https://plumb.lan:8444/x", cmdToken},
		"user":             {ca, "https://u@plumb.lan:8444", cmdToken},
		"token with quote": {ca, "https://plumb.lan:8444", "a'b"},
		"token newline":    {ca, "https://plumb.lan:8444", "a\nPLUMB_TOKEN\nid"},
		"empty token":      {ca, "https://plumb.lan:8444", ""},
		"CA delimiter":     {ca + "PLUMB_CA\nid\n", "https://plumb.lan:8444", cmdToken},
		"not a CA":         {"id\n", "https://plumb.lan:8444", cmdToken},
	} {
		if cmd, err := InstallCommand(c[0], c[1], c[2]); err == nil {
			t.Errorf("%s: accepted:\n%s", name, cmd)
		}
	}
	if _, err := InstallCommand(ca, "https://[2001:db8::1]:8444", cmdToken); err != nil {
		t.Errorf("an IPv6 URL was refused: %v", err)
	}
}
