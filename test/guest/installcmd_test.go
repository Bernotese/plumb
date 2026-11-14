//go:build guest

package guest_test

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
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link/agentinstall"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-206 (host finding F6 of T-192): the install command of an adopted
// guest fails loudly. It runs as root with the real paths in the CI guest
// images (Debian 13, Ubuntu 26.04), which have no curl, like a stock
// Debian 13 LXC on Proxmox: first without curl, then with it for a wrong
// CA, a cut and an empty script, and a good one. The served "install
// script" is a stub: the real install.sh is tested in agentinstall.
func TestGuestInstallCommandFailsLoudly(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root (writes /etc/plumb-agent)")
	}
	const etc = "/etc/plumb-agent"
	const token = "tok-T206-guest-one-time-value"
	stub := "#!/bin/sh\nmain() {\n  test -s " + etc + "/enroll.token && echo ran > " + etc + "/ran\n}\nmain \"$@\"\n"
	var body string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wire.PathInstallScript {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	goodCA := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	t.Cleanup(func() { os.RemoveAll(etc) })

	run := func(t *testing.T, ca string) (int, string) {
		t.Helper()
		os.RemoveAll(etc)
		c := exec.Command("sh", "-c", agentinstall.InstallCommand(ca, srv.URL, token)+"echo \"after=$?\"\n")
		out, _ := c.CombinedOutput()
		s := string(out)
		i := strings.LastIndex(s, "after=")
		if i < 0 {
			t.Fatalf("the person's shell ended with the command:\n%s", s)
		}
		if strings.Contains(s, token) {
			t.Fatalf("the token is in the output:\n%s", s)
		}
		code := 0
		if strings.TrimSpace(s[i+6:]) != "0" {
			code = 1
		}
		return code, s[:i]
	}
	refused := func(t *testing.T, code int, out, want string) {
		t.Helper()
		var mine []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.HasPrefix(l, "plumb-install:") {
				mine = append(mine, l)
			}
		}
		if code == 0 || len(mine) != 1 || !strings.Contains(mine[0], want) {
			t.Fatalf("want exit != 0 and one plumb-install line with %q; code %d:\n%s", want, code, out)
		}
		if _, err := os.Stat(etc + "/enroll.token"); err == nil {
			t.Fatal("a failed try wrote the token file")
		}
		if _, err := os.Stat(etc + "/ran"); err == nil {
			t.Fatal("the script ran")
		}
	}

	t.Run("no curl", func(t *testing.T) {
		if _, err := exec.LookPath("curl"); err == nil {
			t.Fatal("this guest image has curl; the case needs an image without it")
		}
		body = stub
		code, out := run(t, goodCA)
		refused(t, code, out, "curl is missing")
	})

	// The image has no apt lists (Dockerfile); this test may run before
	// the one that refreshes them.
	if out, err := exec.Command("apt-get", "update", "-q").CombinedOutput(); err != nil {
		t.Fatalf("apt-get update: %v\n%s", err, out)
	}
	out, err := exec.Command("apt-get", "install", "-y", "-q", "--no-install-recommends", "curl").CombinedOutput()
	if err != nil {
		t.Fatalf("install curl: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("apt-get", "-y", "-q", "purge", "curl").Run() })

	t.Run("wrong CA", func(t *testing.T) {
		body = stub
		code, out := run(t, foreignCAPEM(t))
		refused(t, code, out, "download of the install script failed")
	})
	t.Run("cut", func(t *testing.T) {
		body = stub[:strings.LastIndex(stub, "main \"$@\"")]
		code, out := run(t, goodCA)
		refused(t, code, out, "empty or incomplete")
	})
	t.Run("empty", func(t *testing.T) {
		body = ""
		code, out := run(t, goodCA)
		refused(t, code, out, "empty or incomplete")
	})
	t.Run("good", func(t *testing.T) {
		body = stub
		code, out := run(t, goodCA)
		if code != 0 {
			t.Fatalf("good: code %d\n%s", code, out)
		}
		if _, err := os.Stat(etc + "/ran"); err != nil {
			t.Fatalf("the script did not run: %v\n%s", err, out)
		}
		fi, err := os.Stat(etc + "/enroll.token")
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("token file: %v %v", fi, err)
		}
	})
}

func foreignCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(9), Subject: pkix.Name{CommonName: "not plumb"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
