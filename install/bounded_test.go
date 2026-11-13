package install

import (
	"bytes"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// runLimit bounds one run of a script or binary in these tests (T-136).
// A run takes seconds; a hang (a wait for a terminal, a loop that never
// ends) fails the test after this time instead of the whole job.
const runLimit = 2 * time.Minute

// bounded runs cmd like cmd.Run, in its own process group. After
// runLimit it kills the group and fails the test. WaitDelay keeps a
// child that holds the output pipes from blocking the wait.
func bounded(t testing.TB, cmd *exec.Cmd) error {
	t.Helper()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(runLimit)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		t.Fatalf("%q did not end within %v: a hang", cmd.Args, runLimit)
		return nil
	}
}

// boundedOutput is cmd.CombinedOutput with the bound of bounded.
func boundedOutput(t testing.TB, cmd *exec.Cmd) ([]byte, error) {
	t.Helper()
	var b bytes.Buffer // one writer for both: exec writes from one goroutine
	cmd.Stdout, cmd.Stderr = &b, &b
	err := bounded(t, cmd)
	return b.Bytes(), err
}
