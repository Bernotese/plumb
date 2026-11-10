package resource

import (
	"context"
	"errors"
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
	// The environment makes tools quiet and predictable.
	out, _ = ExecRunner{}.Run(ctx, "sh", "-c", `printf "%s %s" "$LC_ALL" "$DEBIAN_FRONTEND"`)
	if string(out) != "C noninteractive" {
		t.Fatalf("env = %q", out)
	}
}
