package agent

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
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
	if len(rr.calls) != 4 || rr.calls[0] != "systemctl disable plumb-agent-firewall.service" ||
		rr.calls[1] != "systemctl disable plumb-agent.service" || rr.calls[2] != "systemctl daemon-reload" ||
		rr.calls[3] != "systemctl --no-block stop plumb-agent.service" {
		t.Fatalf("calls %v", rr.calls)
	}
}

// T-205: only unit files that exist are disabled (systemctl disable fails
// on a missing one), and a failed unbind keeps the agent: no finishUnbind
// after its result, so the parked agent takes unbind again (D-396).
func TestUnbindOnlyPresentUnitsAndFailureKeepsAgent(t *testing.T) {
	root := t.TempDir()
	rr := &recRunner{}
	a := &Agent{opt: Options{Root: root, Runner: rr}, cfg: Config{StateDir: filepath.Join(root, "state")}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := a.unbind(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rr.calls) != 1 || rr.calls[0] != "systemctl daemon-reload" {
		t.Fatalf("calls without unit files %v", rr.calls)
	}
	// A failing step: /etc/plumb-agent cannot be removed.
	etc := filepath.Join(root, "etc/plumb-agent")
	os.MkdirAll(filepath.Join(etc, "sub"), 0o755)
	os.WriteFile(filepath.Join(etc, "sub/x"), []byte("x"), 0o600)
	os.Chmod(etc, 0o500)
	defer os.Chmod(etc, 0o755)
	if os.Geteuid() == 0 {
		t.Skip("root removes a read-only directory")
	}
	rep := a.handle(context.Background(), wire.Command{ID: "u1", Type: wire.CommandUnbind, Body: []byte("{}")})
	if rep.Error == "" {
		t.Fatal("the unbind did not fail")
	}
	if a.unboundBy("u1") {
		t.Fatal("a failed unbind finishes the agent")
	}
}

// Review L1 of #299: a 401 on the result of the unbind (the Run revoked
// the agent, the result got lost) still finishes the unbind; a 401 on
// another result does nothing here.
func TestUnbindRefused(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "var/lib/plumb-agent")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	rr := &recRunner{}
	a := &Agent{opt: Options{Root: root, Runner: rr}, cfg: Config{StateDir: state}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.unbindID = "c7"
	if a.unbindRefused(context.Background(), "c6") {
		t.Fatal("another command finished the unbind")
	}
	if _, err := os.Stat(state); err != nil || len(rr.calls) != 0 {
		t.Fatalf("state %v calls %v", err, rr.calls)
	}
	if !a.unbindRefused(context.Background(), "c7") {
		t.Fatal("the unbind result's 401 did not finish the unbind")
	}
	if _, err := os.Stat(state); err == nil {
		t.Fatal("the state stays")
	}
	if len(rr.calls) != 1 || rr.calls[0] != "systemctl --no-block stop plumb-agent.service" {
		t.Fatalf("calls %v", rr.calls)
	}
}
