package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Bernotese/plumb/internal/mcp"
)

// mcpCmd is `plumb mcp` (D-364, ARCHITECTURE §18): an MCP server over
// stdin/stdout for the user's own AI client. It calls only the public API
// with one API token. stdout carries the protocol only; messages go to
// stderr.
func mcpCmd(ctx context.Context, args []string, stdin io.ReadCloser, stdout io.Writer, stderr io.Writer) int {
	cfg, err := parseMCP(args, os.Getenv, stderr)
	if errors.Is(err, errUsage) {
		fmt.Fprintln(stderr, "usage: plumb mcp [--url URL] [--token-file FILE] [--ca FILE]  (env PLUMB_URL, PLUMB_TOKEN, PLUMB_CA)")
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "plumb mcp:", err)
		return 2
	}
	c, err := mcp.NewClient(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "plumb mcp:", err)
		return 2
	}
	if err := mcp.Run(ctx, c, version, stdin, nopCloser{stdout}); err != nil && ctx.Err() == nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(stderr, "plumb mcp:", err)
		return 1
	}
	return 0
}

// parseMCP reads flags and env. Flags win over env. The token comes from
// --token-file (a file that only its owner may read) or PLUMB_TOKEN.
func parseMCP(args []string, getenv func(string) string, stderr io.Writer) (mcp.ClientConfig, error) {
	var cfg mcp.ClientConfig
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", getenv("PLUMB_URL"), "controller URL, https://host:8443 (env PLUMB_URL)")
	tokenFile := fs.String("token-file", "", "file with the API token, mode 0600 (else env PLUMB_TOKEN)")
	caFile := fs.String("ca", getenv("PLUMB_CA"), "the controller CA certificate, ca.crt (env PLUMB_CA); required for https")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return cfg, errUsage
	}
	cfg.URL = *url
	if cfg.URL == "" {
		return cfg, errors.New("the controller URL is missing (--url or PLUMB_URL)")
	}
	switch {
	case *tokenFile != "":
		tok, err := readSecretFile(*tokenFile)
		if err != nil {
			return cfg, err
		}
		cfg.Token = tok
	default:
		cfg.Token = strings.TrimSpace(getenv("PLUMB_TOKEN"))
	}
	if *caFile != "" {
		b, err := os.ReadFile(*caFile)
		if err != nil {
			return cfg, fmt.Errorf("read the CA file: %w", err)
		}
		cfg.CAPEM = b
	}
	return cfg, nil
}

// readSecretFile reads a token file that no group or other user may read.
func readSecretFile(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %s must be a regular file with mode 0600", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if len(b) > 1024 {
		return "", errors.New("token file is too long")
	}
	return strings.TrimSpace(string(b)), nil
}

// nopCloser keeps the caller's stdout open when the MCP session ends.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
