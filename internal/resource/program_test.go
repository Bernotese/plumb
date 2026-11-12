package resource

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// T-094 review H1: a real program. A background child must not hold the
// call, and the timeout kills the whole process group.
func TestRunProgram(t *testing.T) {
	run := ExecRunner{}
	ctx := context.Background()

	start := time.Now()
	if err := run.RunProgram(ctx, []string{"/bin/sh", "-c", "sleep 5 >/dev/null 2>&1 & exit 0"}); err != nil {
		t.Fatalf("background child: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("a background child held the call for %v", d)
	}

	err := run.RunProgram(ctx, []string{"/bin/sh", "-c", "echo secret; echo secret >&2; exit 3"})
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 || ee.Stderr != "" {
		t.Fatalf("exit 3: %#v", err)
	}

	tctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	start = time.Now()
	err = run.RunProgram(tctx, []string{"/bin/sh", "-c", "sleep 60 & sleep 60"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("timeout returned after %v", d)
	}
}

func TestCommandTimeoutRealProgram(t *testing.T) {
	env := NewEnv(guestRoot(t), ExecRunner{})
	r := build(t, res("command", "slow", map[string]any{"argv": []any{"/bin/sh", "-c", "sleep 60 & sleep 60"}, "creates": "/done", "timeout": 1}), env)
	start := time.Now()
	err := r.Apply(context.Background(), []wire.Change{{Attr: wire.AttrState, From: wire.StateAbsent, To: wire.StatePresent, Risk: wire.RiskDestructive}})
	if err == nil || !strings.Contains(err.Error(), "killed after the timeout of 1 s") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Apply returned after %v", d)
	}
}
