package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T-186: a config error of plumb mcp never prints the token (D-364).
func TestQAMCPConfigErrorsHideToken(t *testing.T) {
	const tok = "tok_qa.v3ry-s3cret-value"
	dir := t.TempDir()
	wide := filepath.Join(dir, "wide")
	if err := os.WriteFile(wide, []byte(tok), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		env  map[string]string
		args []string
	}{
		{"bad url", map[string]string{"PLUMB_URL": "ftp://x" + tok, "PLUMB_TOKEN": tok}, nil},
		{"https without CA", map[string]string{"PLUMB_URL": "https://plumb.lan:8443", "PLUMB_TOKEN": tok}, nil},
		{"remote http", map[string]string{"PLUMB_URL": "http://plumb.lan:8443", "PLUMB_TOKEN": tok}, nil},
		{"token with a space", map[string]string{"PLUMB_URL": "http://127.0.0.1:1", "PLUMB_TOKEN": tok + " x"}, nil},
		{"token file too wide", map[string]string{"PLUMB_URL": "http://127.0.0.1:1"}, []string{"--token-file", wide}},
		{"bad CA file", map[string]string{"PLUMB_URL": "https://plumb.lan:8443", "PLUMB_TOKEN": tok, "PLUMB_CA": wide}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range []string{"PLUMB_URL", "PLUMB_TOKEN", "PLUMB_CA"} {
				t.Setenv(k, c.env[k])
			}
			var stderr, stdout strings.Builder
			code := mcpCmd(context.Background(), c.args, io.NopCloser(strings.NewReader("")), &stdout, &stderr)
			if code != 2 {
				t.Fatalf("exit %d, want 2 (stderr %q)", code, stderr.String())
			}
			if strings.Contains(stderr.String(), "s3cret") || strings.Contains(stdout.String(), "s3cret") {
				t.Fatalf("the token is printed: %q", stderr.String())
			}
		})
	}
}
