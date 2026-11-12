package resource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// Check the descendant itself: a fast timeout return alone does not prove
// the process group was killed. The child ignores TERM.
func TestQAProgramKillsDescendant(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "child")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (ExecRunner{}).RunProgram(ctx, []string{"/bin/sh", "-c", `sh -c 'trap "" TERM; while :; do sleep 1; done' & echo $! > "$1"; wait`, "qa", pidfile})
	}()
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(pidfile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		if pid > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		cancel()
		<-done
		t.Fatal("child did not start")
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not return on cancel")
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		// A zombie has exited and cannot execute more code.
		if os.IsNotExist(err) || strings.Contains(string(b), ") Z ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("descendant is still running after cancellation")
}

type qaProgramRunner struct {
	t      *testing.T
	called bool
}

func (r *qaProgramRunner) Run(context.Context, string, ...string) ([]byte, error) {
	r.t.Fatal("command used the output-capturing Runner")
	return nil, nil
}
func (r *qaProgramRunner) RunProgram(ctx context.Context, argv []string) error {
	r.called = true
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 2*time.Second {
		r.t.Fatal("command timeout not passed to ProgramRunner")
	}
	return &ExitError{Name: argv[0], Code: 7, Stderr: "private-output"}
}
func TestQACommandUsesProgramRunner(t *testing.T) {
	run := &qaProgramRunner{t: t}
	r := build(t, res("command", "qa", map[string]any{"argv": []any{"/bin/true"}, "creates": "/done", "timeout": 2}), NewEnv(guestRoot(t), run))
	err := r.Apply(context.Background(), []wire.Change{{Attr: wire.AttrState}})
	if !run.called || err == nil || strings.Contains(err.Error(), "private-output") || !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("Apply = %v, called = %v", err, run.called)
	}
}

func TestQAProgramStdinEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := (ExecRunner{}).RunProgram(ctx, []string{"/bin/sh", "-c", "if read value; then exit 9; fi"}); err != nil {
		t.Fatalf("stdin was not EOF: %v", err)
	}
}
