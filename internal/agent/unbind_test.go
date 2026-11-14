package agent

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recRunner struct{ calls []string }

func (r *recRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil, nil
}

// unbind removes the agent, its units, /etc/plumb-agent and the repair
// lines; finishUnbind removes the credential and stops the unit. What the
// agent applied elsewhere stays.
func TestUnbind(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "var/lib/plumb-agent")
	put := func(p, s string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("root/.ssh/authorized_keys", "ssh-ed25519 AAAAuser u\nrestrict ssh-ed25519 AAAAk plumb-repair-1\n")
	for _, p := range []string{"etc/plumb-agent/agent.json", "etc/plumb-agent/ca.pem", "usr/local/bin/plumb-agent",
		"etc/systemd/system/plumb-agent.service", "var/lib/plumb-agent/credential",
		"etc/plumb-agent/firewall.nft", "etc/systemd/system/plumb-agent-firewall.service", "etc/motd"} {
		put(p, "x")
	}
	rr := &recRunner{}
	a := &Agent{opt: Options{Root: root, Runner: rr}, cfg: Config{StateDir: state}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := a.unbind(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal("the credential went before the result was sent")
	}
	a.finishUnbind(context.Background())
	gone := []string{"etc/plumb-agent", "usr/local/bin/plumb-agent", "etc/systemd/system/plumb-agent.service",
		"etc/systemd/system/plumb-agent-firewall.service", "var/lib/plumb-agent"}
	for _, p := range gone {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Errorf("%s still there", p)
		}
	}
	for _, p := range []string{"etc/motd"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s removed: %v", p, err)
		}
	}
	keys, _ := os.ReadFile(filepath.Join(root, "root/.ssh/authorized_keys"))
	if string(keys) != "ssh-ed25519 AAAAuser u\n" {
		t.Fatalf("authorized_keys %q", keys)
	}
	if len(rr.calls) != 4 || !strings.HasPrefix(rr.calls[0], "systemctl disable plumb-agent-install.service") ||
		rr.calls[1] != "systemctl disable plumb-agent.service" || rr.calls[2] != "systemctl daemon-reload" ||
		rr.calls[3] != "systemctl --no-block stop plumb-agent.service" {
		t.Fatalf("calls %v", rr.calls)
	}
}
