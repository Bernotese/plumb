package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMCP(t *testing.T) {
	dir := t.TempDir()
	tokFile := filepath.Join(dir, "token")
	os.WriteFile(tokFile, []byte("tok_file.secret\n"), 0o600)
	loose := filepath.Join(dir, "loose")
	os.WriteFile(loose, []byte("tok_loose.secret"), 0o644)
	ca := filepath.Join(dir, "ca.crt")
	os.WriteFile(ca, []byte("PEM"), 0o644)
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	cfg, err := parseMCP([]string{"--token-file", tokFile}, env(map[string]string{
		"PLUMB_URL": "https://plumb.lan:8443", "PLUMB_TOKEN": "tok_env.secret", "PLUMB_CA": ca}), &bytes.Buffer{})
	if err != nil || cfg.URL != "https://plumb.lan:8443" || cfg.Token != "tok_file.secret" || string(cfg.CAPEM) != "PEM" {
		t.Fatalf("file wins: %+v %v", cfg, err)
	}
	cfg, err = parseMCP([]string{"--url", "http://127.0.0.1:8443"}, env(map[string]string{
		"PLUMB_URL": "https://ignored", "PLUMB_TOKEN": " tok_env.secret "}), &bytes.Buffer{})
	if err != nil || cfg.URL != "http://127.0.0.1:8443" || cfg.Token != "tok_env.secret" || cfg.CAPEM != nil {
		t.Fatalf("flag over env: %+v %v", cfg, err)
	}
	for _, c := range []struct {
		args []string
		env  map[string]string
		want string
	}{
		{nil, map[string]string{}, "URL is missing"},
		{[]string{"--token-file", loose}, map[string]string{"PLUMB_URL": "http://127.0.0.1:1"}, "mode 0600"},
		{[]string{"--token-file", filepath.Join(dir, "nope")}, map[string]string{"PLUMB_URL": "http://127.0.0.1:1"}, "token file"},
		{[]string{"--ca", filepath.Join(dir, "nope")}, map[string]string{"PLUMB_URL": "http://127.0.0.1:1"}, "CA file"},
		{[]string{"extra"}, map[string]string{"PLUMB_URL": "http://127.0.0.1:1"}, "usage"},
	} {
		if _, err := parseMCP(c.args, env(c.env), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
}

// plumb mcp refuses a bad config with exit 2, and stdout stays empty
// (stdout is the protocol).
func TestMCPCmdRefusesBadConfig(t *testing.T) {
	t.Setenv("PLUMB_URL", "http://plumb.lan:8443")
	t.Setenv("PLUMB_TOKEN", "tok_x.secret")
	var out, errOut bytes.Buffer
	if code := run([]string{"mcp"}, &out, &errOut); code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "loopback") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	if strings.Contains(errOut.String(), "tok_x.secret") {
		t.Fatal("the token is in a message")
	}
}
