package resource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// cmdRunner is a fake guest for command: it records calls and runs fn.
type cmdRunner struct {
	root  string
	calls [][]string
	fn    func(ctx context.Context, root string, argv []string) ([]byte, error)
}

func (r *cmdRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	argv := append([]string{name}, args...)
	r.calls = append(r.calls, argv)
	return r.fn(ctx, r.root, argv)
}

// touch creates the creates path given as the last argument.
func touch(_ context.Context, root string, argv []string) ([]byte, error) {
	p := filepath.Join(root, argv[len(argv)-1])
	return []byte("secret-looking stdout"), os.WriteFile(p, nil, 0o600)
}

func cmdEnv(t *testing.T, fn func(context.Context, string, []string) ([]byte, error)) (*cmdRunner, Env) {
	r := &cmdRunner{root: guestRoot(t), fn: fn}
	return r, NewEnv(r.root, r)
}

func TestCommandRunsOnce(t *testing.T) {
	run, env := cmdEnv(t, touch)
	r := build(t, res("command", "init", map[string]any{"argv": []any{"/usr/bin/touch", "/var/lib/done"}, "creates": "/var/lib/done"}), env)
	got := converge(t, r)
	if wire.ActionOf(got) != wire.ActionCreate || attrsOf(got) != "state=destructive" {
		t.Fatalf("changes = %s (%s)", attrsOf(got), wire.ActionOf(got))
	}
	if len(run.calls) != 1 || strings.Join(run.calls[0], " ") != "/usr/bin/touch /var/lib/done" {
		t.Fatalf("calls = %q", run.calls)
	}

	// No drift: another argv on a guest where creates exists changes nothing.
	r = build(t, res("command", "init", map[string]any{"argv": []any{"/bin/other"}, "creates": "/var/lib/done"}), env)
	if c := plan(t, r); len(c) != 0 {
		t.Fatalf("changed argv gives changes %+v", c)
	}
	if r.Watch() != nil {
		t.Fatal("a command watches no path")
	}
	// Apply with a stale change does not run again: the guard is checked again.
	if err := r.Apply(context.Background(), got); err != nil || len(run.calls) != 1 {
		t.Fatalf("second apply: err %v, calls %d", err, len(run.calls))
	}
}

func TestCommandFailures(t *testing.T) {
	ctx := context.Background()
	change := []wire.Change{{Attr: wire.AttrState, From: wire.StateAbsent, To: wire.StatePresent, Risk: wire.RiskDestructive}}
	attrs := map[string]any{"argv": []any{"/opt/app/init", "--token", "s3cret"}, "creates": "/done"}

	// A non-zero exit: no stdout and no stderr in the error (they can hold
	// any secret of the guest).
	_, env := cmdEnv(t, func(context.Context, string, []string) ([]byte, error) {
		return []byte("stdout s3cret"), &ExitError{Name: "/opt/app/init", Code: 3, Stderr: "stderr s3cret"}
	})
	err := build(t, res("command", "c", attrs), env).Apply(ctx, change)
	if err == nil || err.Error() != "/opt/app/init: exit status 3 (output not shown)" {
		t.Fatalf("exit: %v", err)
	}

	// Exit 0 without creating the path: a failure, it runs again later.
	_, env = cmdEnv(t, func(context.Context, string, []string) ([]byte, error) { return nil, nil })
	err = build(t, res("command", "c", attrs), env).Apply(ctx, change)
	if err == nil || !strings.Contains(err.Error(), "did not create /done") {
		t.Fatalf("no creates: %v", err)
	}

	// Timeout: the context of the program ends; the text names the limit.
	_, env = cmdEnv(t, func(ctx context.Context, _ string, _ []string) ([]byte, error) {
		<-ctx.Done()
		return nil, &ExitError{Name: "/opt/app/init", Code: -1, Stderr: "s3cret"}
	})
	a := map[string]any{"argv": attrs["argv"], "creates": "/done", "timeout": 1}
	err = build(t, res("command", "c", a), env).Apply(ctx, change)
	if err == nil || err.Error() != "/opt/app/init: killed after the timeout of 1 s (output not shown)" {
		t.Fatalf("timeout: %v", err)
	}
}

func TestCommandBadAttrs(t *testing.T) {
	_, env := cmdEnv(t, touch)
	for _, a := range []map[string]any{
		{"argv": []any{"init"}, "creates": "/x"},
		{"argv": []any{}, "creates": "/x"},
		{"argv": []any{"/bin/true"}, "creates": "/a/../b"},
		{"argv": []any{"/bin/true"}, "creates": "/x", "timeout": 0},
		{"argv": []any{"/bin/true"}, "creates": "/x", "state": "absent"},
	} {
		if _, err := New(res("command", "c", a), env); err == nil {
			t.Errorf("New(%v) = nil error", a)
		}
	}
}
