//go:build guest

package agent

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// The seal guest test changes the whole container (plan §8 "Guest, in
// CI"). Its name does not start with TestGuest: test/guest/run.sh runs it
// last, alone.

// guestSealRunner runs the real commands, but only records the power-off.
type guestSealRunner struct {
	mu      sync.Mutex
	calls   []string
	powered chan struct{}
}

func (r *guestSealRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	r.mu.Lock()
	r.calls = append(r.calls, line)
	r.mu.Unlock()
	if line == "systemctl poweroff" {
		close(r.powered)
		return nil, nil
	}
	return (resource.ExecRunner{}).Run(ctx, name, args...)
}

func (r *guestSealRunner) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func sh(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return string(out)
}

// startGuestAgent runs an agent on the real guest root with the default
// state dir. Nothing of it is under /tmp: seal empties /tmp.
func startGuestAgent(t *testing.T, build bool, r resource.Runner) (*controller, context.CancelFunc, chan struct{}) {
	t.Helper()
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ctl.build = build
	if err := os.MkdirAll("/var/lib/plumb-guest", 0o700); err != nil {
		t.Fatal(err)
	}
	tokenFile := "/var/lib/plumb-guest/seal-token"
	write(t, tokenFile, "tok-1\n")
	a, err := New(Options{
		Config: Config{URL: ctl.srv.URL, CA: ca.pem, TokenFile: tokenFile, FullCheck: Duration(time.Hour)},
		Runner: r, Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollWait: 1, BackoffMin: 10 * time.Millisecond, BackoffMax: 50 * time.Millisecond,
		Sync: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = a.Run(ctx)
	}()
	waitFor(t, "enrollment", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
	return ctl, cancel, done
}

func TestSealInGuest(t *testing.T) {
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || strings.TrimSpace(string(comm)) != "systemd" {
		t.Fatal("requires root in the systemd guest")
	}
	for _, c := range []string{"cloud-init", "sshd", "userdel", "pkill"} {
		if _, err := exec.LookPath(c); err != nil {
			t.Fatalf("the guest image needs %s: %v", c, err)
		}
	}
	// The build guest as cloud-init leaves it.
	sh(t, "ssh-keygen", "-A")
	sh(t, "useradd", "-m", "-s", "/bin/bash", buildUser)
	write(t, buildSudoers, "plumb-build ALL=(ALL) NOPASSWD:ALL\n")
	sleeper := exec.Command("runuser", "-u", buildUser, "--", "sleep", "600")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	go sleeper.Wait()
	write(t, "/var/lib/cloud/instances/iid-build/obj.pkl", "state")
	write(t, "/var/log/cloud-init.log", "hostname plumb-build-debian-v1 10.0.0.9")
	write(t, "/var/log/syslog.1", "old")
	write(t, "/var/log/plumb-test/deep/x.log", "plumb-build")
	write(t, "/tmp/build-file", "x")
	write(t, "/root/.bash_history", "secret command")
	write(t, "/var/lib/systemd/random-seed", "seed")
	oldID := strings.TrimSpace(sh(t, "cat", "/etc/machine-id"))
	// The agent's own files, as the vendor snippet installs them.
	for p, c := range map[string]string{
		"/etc/plumb-agent/agent.json": "{}", "/etc/plumb-agent/ca.pem": "pem",
		"/usr/local/bin/plumb-agent": "bin", "/usr/local/sbin/plumb-agent-install": "#!/bin/sh\n",
		"/etc/systemd/system/plumb-agent.service": "[Service]\nExecStart=/bin/true\n[Install]\nWantedBy=multi-user.target\n",
	} {
		write(t, p, c)
	}
	sh(t, "systemctl", "enable", "plumb-agent.service")
	// Review S6: a Block put links under /var/log.
	// The links aim at a canary file: userdel rewrites /etc/shadow itself.
	const canary = "/etc/plumb-seal-canary"
	write(t, canary, "must stay")
	for _, cmd := range [][]string{{"ln", "-sf", "/etc/shadow", "/var/log/evil"}, {"ln", "-sf", canary, "/var/log/evil2"},
		{"ln", "-f", canary, "/var/log/hard"}, {"ln", "-sfn", "/etc", "/var/log/etcdir"}} {
		sh(t, cmd...)
	}
	if _, err := exec.LookPath("rsyslogd"); err == nil {
		sh(t, "systemctl", "start", "rsyslog.service")
		sh(t, "logger", "plumb seal guest test")
	}

	t.Run("a normal agent refuses", func(t *testing.T) {
		r := &guestSealRunner{powered: make(chan struct{})}
		ctl, cancel, done := startGuestAgent(t, false, r)
		defer func() { cancel(); <-done }()
		ctl.send(t, "s0", wire.CommandSeal, wire.SealBody{PlanID: 1})
		rep := ctl.result(t, "s0")
		if len(rep.Items) != 1 || rep.Items[0].Status != wire.StatusRefused {
			t.Fatalf("seal of a normal agent: %+v", rep.Items)
		}
		if calls := r.list(); len(calls) > 0 {
			t.Fatalf("a refused seal ran %q", calls)
		}
		if m, _ := filepath.Glob("/etc/ssh/ssh_host_*"); len(m) == 0 {
			t.Fatal("host keys gone after a refusal")
		}
	})
	// The normal agent is enrolled now: start again from an empty state.
	if err := os.RemoveAll(DefaultStateDir); err != nil {
		t.Fatal(err)
	}

	r := &guestSealRunner{powered: make(chan struct{})}
	ctl, cancel, done := startGuestAgent(t, true, r)
	defer func() { cancel(); <-done }()
	ctl.send(t, "s1", wire.CommandSeal, wire.SealBody{PlanID: 2})
	rep := ctl.result(t, "s1")
	for _, it := range rep.Items {
		t.Logf("%s %s %s", it.Resource, it.Status, it.Output)
		if it.Status != wire.StatusApplied && it.Status != wire.StatusSkipped {
			t.Fatalf("seal step %s: %s %s", it.Resource, it.Status, it.Output)
		}
	}
	select {
	case <-r.powered:
	case <-time.After(30 * time.Second):
		t.Fatal("no power off")
	}

	if m, _ := filepath.Glob("/etc/ssh/ssh_host_*"); len(m) > 0 {
		t.Errorf("host keys left: %v", m)
	}
	if id := strings.TrimSpace(sh(t, "cat", "/etc/machine-id")); id == oldID || (id != "" && id != "uninitialized") {
		t.Errorf("machine-id not reset: %q", id)
	}
	if m, _ := filepath.Glob("/var/lib/cloud/instances/*"); len(m) > 0 {
		t.Errorf("cloud-init state left: %v", m)
	}
	if _, err := os.Lstat(CloudInitFlag); err == nil {
		t.Error("cloud-init.disabled left")
	}
	if b, _ := os.ReadFile("/etc/passwd"); strings.Contains(string(b), buildUser+":") {
		t.Error("user plumb-build left")
	}
	for _, p := range []string{"/home/plumb-build", buildSudoers, "/var/lib/systemd/random-seed", "/var/log/syslog.1",
		"/var/log/hard", "/tmp/build-file", "/root/.bash_history"} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s left", p)
		}
	}
	for _, p := range append([]string{"/etc/systemd/system/multi-user.target.wants/plumb-agent.service"}, agentFiles...) {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("agent file %s left", p)
		}
	}
	if got := sh(t, "cat", canary); got != "must stay" {
		t.Errorf("%s changed through a link under /var/log: %q", canary, got)
	}
	if fi, err := os.Stat("/etc/shadow"); err != nil || fi.Size() == 0 {
		t.Errorf("/etc/shadow emptied through a link: %v", err)
	}
	err = filepath.WalkDir("/var/log", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil && fi.Size() != 0 {
				t.Errorf("%s has %d bytes", p, fi.Size())
			}
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
	calls := r.list()
	if calls[len(calls)-1] != "systemctl poweroff" {
		t.Errorf("last call %q", calls[len(calls)-1])
	}
}

// The upgrade check reads the real output of apt-get on both distributions.
func TestSealAptOutputInGuest(t *testing.T) {
	out, err := exec.Command("apt-get", "-s", "dist-upgrade").Output()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := aptPending(string(out)); !ok {
		t.Fatalf("no apt summary in:\n%s", out)
	}
}
