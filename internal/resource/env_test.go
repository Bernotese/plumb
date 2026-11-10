package resource

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestExecRunner(t *testing.T) {
	ctx := context.Background()
	out, err := ExecRunner{}.Run(ctx, "sh", "-c", `printf out; printf err >&2`)
	if err != nil || string(out) != "out" {
		t.Fatalf("Run = %q, %v", out, err)
	}
	_, err = ExecRunner{}.Run(ctx, "sh", "-c", `echo "bad thing" >&2; exit 3`)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 || exitCode(err) != 3 || !strings.Contains(err.Error(), "bad thing") {
		t.Fatalf("Run error = %#v", err)
	}
	if _, err := (ExecRunner{}).Run(ctx, "/nonexistent/plumb-test"); err == nil || exitCode(err) != -1 {
		t.Fatalf("missing command: %v", err)
	}
}

func TestExecRunnerEnv(t *testing.T) {
	t.Setenv("PLUMB_AGENT_SECRET_PATH", "/var/lib/plumb-agent/credential")
	t.Setenv("HTTPS_PROXY", "http://proxy:3128")
	out, err := ExecRunner{}.Run(context.Background(), "/usr/bin/env")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		k, _, _ := strings.Cut(line, "=")
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"DEBIAN_FRONTEND", "HOME", "LC_ALL", "PATH"}; !slices.Equal(keys, want) {
		t.Fatalf("command env keys = %v, want %v\n%s", keys, want, out)
	}
}

func TestExecRunnerStderrBounded(t *testing.T) {
	_, err := ExecRunner{}.Run(context.Background(), "sh", "-c", `head -c 200000 /dev/zero | tr '\0' a >&2; exit 1`)
	var ee *ExitError
	if !errors.As(err, &ee) || len(ee.Stderr) != MaxStderr {
		t.Fatalf("stderr length = %d, want %d", len(ee.Stderr), MaxStderr)
	}
}
