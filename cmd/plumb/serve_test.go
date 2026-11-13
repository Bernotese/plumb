package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/secret"
)

// D-039: --dev never touches a real host; without --dev, no plain HTTP
// and no fake driver.
func TestServeFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string // "" = valid
	}{
		{[]string{"--dev"}, ""},
		{[]string{"--dev", "--driver", "fake"}, ""},
		{[]string{"--dev", "--driver", "proxmox"}, "fake driver only"},
		{[]string{"--plain-http"}, "plain HTTP is refused"},
		{[]string{"--driver", "fake"}, "for --dev only"},
		{[]string{"--driver", "vmware"}, "unknown driver"},
		{[]string{}, ""},
	} {
		f, err := parseServe(tc.args, &bytes.Buffer{})
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%v: %v", tc.args, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%v: got %v, want %q", tc.args, err, tc.want)
		}
		if err == nil && f.dev && (!f.plainHTTP || f.driver != "fake") {
			t.Errorf("%v: dev mode = %+v", tc.args, f)
		}
		if err == nil && !f.dev && (f.plainHTTP || f.driver != "proxmox") {
			t.Errorf("%v: production mode = %+v", tc.args, f)
		}
	}
	// Without --dev and without the Proxmox settings the controller stops
	// with exit 78 and names them (D-182); it never falls back to the fake.
	clearPVE(t)
	var out, errOut bytes.Buffer
	keyed := t.TempDir()
	os.Mkdir(filepath.Join(keyed, "key"), 0o700)
	if err := secret.WriteKeyFile(defaultKeyFile(keyed), secret.NewKey()); err != nil {
		t.Fatal(err)
	}
	if code := serve(context.Background(), []string{"--data", keyed}, &out, &errOut); code != 78 || !strings.Contains(errOut.String(), "the Proxmox settings are missing") {
		t.Errorf("serve without --dev: %d %q", code, errOut.String())
	}
	if code := serve(context.Background(), []string{"--dev", "--driver", "proxmox"}, &out, &errOut); code != 2 {
		t.Errorf("serve --dev --driver proxmox: %d", code)
	}
}

// Review T-011 m3: dev mode stays on loopback by default and never opens
// a production data directory.
func TestDevSafety(t *testing.T) {
	f, _ := parseServe([]string{"--dev"}, &bytes.Buffer{})
	if f.listen != "127.0.0.1:8443" || f.agentListen != "127.0.0.1:8444" {
		t.Errorf("dev listens on %s and %s", f.listen, f.agentListen)
	}
	f, _ = parseServe(nil, &bytes.Buffer{})
	if f.listen != ":8443" || f.agentListen != ":8444" {
		t.Errorf("production listens on %s and %s", f.listen, f.agentListen)
	}
	if !loopback("127.0.0.1:1") || !loopback("[::1]:1") || !loopback("localhost:1") || loopback(":8443") || loopback("192.0.2.5:1") {
		t.Error("loopback")
	}
	prod := t.TempDir()
	if err := checkMode(prod, false); err != nil {
		t.Fatal(err)
	}
	if err := checkMode(prod, true); err == nil || !strings.Contains(err.Error(), "production data directory") {
		t.Errorf("dev on a production directory: %v", err)
	}
	dev := t.TempDir()
	checkMode(dev, true)
	if err := checkMode(dev, false); err == nil {
		t.Error("production on a dev directory")
	}
	old := t.TempDir()
	os.WriteFile(filepath.Join(old, "plumb.db"), []byte("x"), 0o600)
	if err := checkMode(old, true); err == nil {
		t.Error("dev on an unmarked database")
	}
}

// D-080: --dev-demo needs --dev, and the dev mode marker still refuses a
// production data directory.
func TestDevDemoGuards(t *testing.T) {
	if _, err := parseServe([]string{"--dev-demo"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "--dev-demo needs --dev") {
		t.Errorf("--dev-demo alone: %v", err)
	}
	var out, errOut bytes.Buffer
	if code := serve(context.Background(), []string{"--dev-demo"}, &out, &errOut); code != 2 {
		t.Errorf("serve --dev-demo: %d", code)
	}
	prod := t.TempDir()
	if err := checkMode(prod, false); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	code := serve(context.Background(), []string{"--dev", "--dev-demo", "--data", prod, "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0"}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "production data directory") {
		t.Errorf("dev demo on a production directory: %d %q", code, errOut.String())
	}
	if strings.Contains(out.String(), "dev login") {
		t.Error("a refused start printed a login link")
	}
}
