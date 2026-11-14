//go:build guest

package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Bernotese/plumb/internal/link/agentinstall"
	planpkg "github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/repair"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// TestGuestRepair proves the SSH repair (D-377, D-383 … D-385, T-191) in
// a cloud VM with the real sshd:
//   - the agent writes the line; its report pins the real host keys;
//   - a dead agent (unit masked, no binary) is repaired: the forced
//     command takes the bundle, the install script unmasks and starts the
//     unit and leaves the one-shot token 0600;
//   - the line allows nothing but the fixed command: a shell command, a
//     PTY, a remote forward and scp all fail;
//   - new host keys: the Run is refused before anything is sent.
//
// It runs in the VMs of lxd.sh (PLUMB_REPAIR_GUEST=1), last: it changes
// the host keys and root's authorized_keys.
func TestGuestRepair(t *testing.T) {
	if os.Getenv("PLUMB_REPAIR_GUEST") != "1" {
		t.Skip("the SSH repair needs a cloud VM with sshd; only lxd.sh runs it (PLUMB_REPAIR_GUEST=1)")
	}
	if os.Geteuid() != 0 {
		t.Fatal("requires root in the guest")
	}
	ctx := context.Background()
	runOut := func(name string, args ...string) (string, error) {
		o, err := exec.Command(name, args...).Output()
		return string(o), err
	}
	run := func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).CombinedOutput()
		return string(out), err
	}
	if out, err := run("systemctl", "start", "ssh.service"); err != nil {
		t.Fatalf("start sshd: %v %s", err, out)
	}
	// The controller side, in this process.
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sec, err := secret.Open(ctx, st, secret.NewKey())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := repair.New(repair.Config{Store: st, Secrets: sec, From: []string{"127.0.0.1"}, ConnectTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.PutBinding(ctx, store.Binding{Kind: state.KindTarget, Name: "web1", Ref: "vm/100"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutEnrollToken(ctx, "web1", "h", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Enroll(ctx, "h", time.Now(), store.Agent{ID: "a1", CredentialHash: "x"}); err != nil {
		t.Fatal(err)
	}

	// The agent side: the line and the report, as at a watch.
	a := &Agent{opt: Options{Root: "/", Runner: resource.ExecRunner{}}}
	w := svc.Watch(ctx)
	if w == nil {
		t.Fatal("no repair key set")
	}
	if err := writeRepairLines("/", w); err != nil {
		t.Fatal(err)
	}
	rep := a.repairReport(ctx, w)
	if rep.SSHD.Status != wire.SSHDOk || len(rep.Lines) != 1 || len(rep.HostKeys) == 0 {
		t.Fatalf("report %+v", rep)
	}
	if err := svc.OnReport(ctx, "web1", rep); err != nil {
		t.Fatal(err)
	}
	if s, _ := svc.Status(ctx, "web1"); s == nil || s.State != planpkg.RepairReady {
		t.Fatalf("status %+v", s)
	}

	// A dead agent.
	run("systemctl", "stop", "plumb-agent.service")
	if out, err := run("systemctl", "mask", "plumb-agent.service"); err != nil {
		t.Fatalf("mask: %v %s", err, out)
	}
	os.Remove("/usr/local/bin/plumb-agent")
	t.Cleanup(func() {
		run("systemctl", "stop", "plumb-agent.service")
		run("systemctl", "disable", "plumb-agent.service")
		os.RemoveAll("/etc/plumb-agent")
		os.Remove("/usr/local/bin/plumb-agent")
		os.Remove("/etc/systemd/system/plumb-agent.service")
		run("systemctl", "daemon-reload")
	})
	bundle := agentinstall.Bundle{Agent: []byte("#!/bin/sh\nexec sleep 3600\n"),
		CA: []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), URL: "https://127.0.0.1:8444",
		Token: "guestTokenguestTokenguestTokenguestTokenAAA"}
	var out bytes.Buffer
	exit, sent, err := svc.Send(ctx, planpkg.RepairSend{Target: "web1", StaticIP: "127.0.0.1",
		Bundle: func(w io.Writer) error { return agentinstall.WriteBundle(w, bundle) }, Output: &out})
	if err != nil || !sent || exit != 0 {
		t.Fatalf("repair: exit %d sent %v err %v\n%s", exit, sent, err, out.String())
	}
	if s, _ := run("systemctl", "is-active", "plumb-agent.service"); strings.TrimSpace(s) != "active" {
		t.Fatalf("the agent unit is %q after the repair", s)
	}
	fi, err := os.Stat(wire.RepairTokenFile)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("one-shot token: %v %v", fi, err)
	}
	if _, err := os.Stat("/run/plumb-repair"); !os.IsNotExist(err) {
		t.Fatal("the bundle dir stays")
	}

	// Nothing but the fixed command: with the repair key on disk (test
	// only). The forced command waits on stdin, so every check has a time
	// limit and stdin from /dev/null; a timeout counts as refused (review
	// C1 of #298), and each check looks for its side effect.
	keyFile := writeRepairKeyFile(t, st, sec)
	opts := []string{"-i", keyFile, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10"}
	limited := func(env []string, stdin io.Reader, name string, args ...string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, name, args...)
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdin = stdin
		out, err := cmd.CombinedOutput()
		if cctx.Err() != nil {
			return string(out), errors.New("timeout")
		}
		return string(out), err
	}
	ssh := func(args ...string) (string, error) {
		return limited(nil, nil, "ssh", append(append([]string{}, opts...), args...)...)
	}
	if out, _ := ssh("root@127.0.0.1", "id; touch /tmp/plumb-shell"); strings.Contains(out, "uid=") || exists("/", "/tmp/plumb-shell") {
		t.Fatalf("a shell command ran: %s", out)
	}
	if out, _ := ssh("-tt", "root@127.0.0.1", "id"); strings.Contains(out, "uid=") {
		t.Fatalf("a PTY command ran: %s", out)
	}
	if out, err := ssh("-o", "ExitOnForwardFailure=yes", "-N", "-R", "127.0.0.1:19999:127.0.0.1:22", "root@127.0.0.1"); err == nil {
		t.Fatalf("a remote forward passed: %s", out)
	}
	// -W opens the same direct-tcpip channel as -L: it must be refused.
	if out, err := limited(nil, strings.NewReader(""), "ssh", append(append([]string{}, opts...), "-W", "127.0.0.1:22", "root@127.0.0.1")...); err == nil || strings.Contains(out, "SSH-2.0") {
		t.Fatalf("a local forward passed: %s", out)
	}
	if out, err := limited(nil, nil, "scp", append(append([]string{}, opts...), "/etc/hostname", "root@127.0.0.1:/tmp/plumb-scp")...); err == nil || exists("/", "/tmp/plumb-scp") {
		t.Fatalf("scp passed: %s", out)
	}
	if out, err := limited(nil, strings.NewReader("put /etc/hostname /tmp/plumb-sftp\n"), "sftp",
		append(append([]string{}, opts...), "-b", "-", "root@127.0.0.1")...); err == nil || exists("/", "/tmp/plumb-sftp") {
		t.Fatalf("sftp passed: %s", out)
	}
	// An environment the client sets never reaches the shell that runs the
	// forced command (bash reads BASH_ENV).
	if err := os.WriteFile("/tmp/plumb-bashenv.sh", []byte("touch /tmp/plumb-bashenv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ssh("-o", "SetEnv=BASH_ENV=/tmp/plumb-bashenv.sh", "root@127.0.0.1", "x")
	if exists("/", "/tmp/plumb-bashenv") {
		t.Fatal("BASH_ENV from the client ran")
	}
	// Agent forwarding: sshd makes no agent socket for the session.
	agentSock := filepath.Join(t.TempDir(), "agent.sock")
	agentOut, err := runOut("ssh-agent", "-a", agentSock)
	if err != nil {
		t.Fatalf("ssh-agent: %v %s", err, agentOut)
	}
	if _, rest, ok := strings.Cut(agentOut, "SSH_AGENT_PID="); ok {
		pid, _, _ := strings.Cut(rest, ";")
		if n, err := strconv.Atoi(pid); err == nil {
			defer syscall.Kill(n, syscall.SIGTERM)
		}
	}
	before, _ := filepath.Glob("/tmp/ssh-*/agent.*")
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		limited([]string{"SSH_AUTH_SOCK=" + agentSock}, pr, "ssh", append(append([]string{}, opts...), "-A", "root@127.0.0.1", "x")...)
		close(done)
	}()
	time.Sleep(3 * time.Second) // the session waits on stdin meanwhile
	after, _ := filepath.Glob("/tmp/ssh-*/agent.*")
	pw.Close()
	<-done
	if len(after) > len(before) {
		t.Fatalf("an agent socket was made for the session: %v", after)
	}

	// New host keys: refused before anything is sent.
	for _, f := range []string{"rsa", "ecdsa", "ed25519"} {
		os.Remove("/etc/ssh/ssh_host_" + f + "_key")
		os.Remove("/etc/ssh/ssh_host_" + f + "_key.pub")
	}
	if out, err := run("ssh-keygen", "-A"); err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	run("systemctl", "restart", "ssh.service")
	time.Sleep(time.Second)
	called := false
	_, sent, err = svc.Send(ctx, planpkg.RepairSend{Target: "web1", StaticIP: "127.0.0.1",
		Bundle: func(io.Writer) error { called = true; return nil }})
	if !errors.Is(err, repair.ErrHostKey) || sent || called {
		t.Fatalf("new host keys: sent %v called %v err %v", sent, called, err)
	}
}

// writeRepairKeyFile writes the current repair private key as an OpenSSH
// key file, for the ssh client of the test only.
func writeRepairKeyFile(t *testing.T, st *store.Store, sec *secret.Store) string {
	t.Helper()
	keys, err := st.RepairKeys(context.Background())
	if err != nil || len(keys) == 0 {
		t.Fatal(err)
	}
	seed, err := sec.OpenBlob(secret.PurposeRepairKey, "repair-key/"+keys[0].ID, keys[0].Nonce, keys[0].Sealed)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKey(ed25519.NewKeyFromSeed(seed), "")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "id_repair")
	if err := os.WriteFile(p, pem.EncodeToMemory(blk), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
