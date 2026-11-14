package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ARCH §19 (T-193 part): after unbind there is no plumb-repair- line, and
// every other line stays byte for byte.
func TestRemoveRepairLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "authorized_keys")
	in := "ssh-ed25519 AAAAuser user@laptop\n" +
		`restrict,from="192.0.2.5",command="umask 077; d=/run/plumb-repair" ssh-ed25519 AAAAk1 plumb-repair-1` + "\n" +
		"# a comment plumb-repair-not-a-key\n" +
		`restrict,from="192.0.2.5",command="x" ssh-ed25519 AAAAk2 plumb-repair-2` + "\n" +
		"ssh-rsa AAAAother plumb-repairman\n"
	if err := os.WriteFile(p, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeRepairLines(p); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	want := "ssh-ed25519 AAAAuser user@laptop\n# a comment plumb-repair-not-a-key\nssh-rsa AAAAother plumb-repairman\n"
	if string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if err := removeRepairLines(filepath.Join(t.TempDir(), "none")); err != nil {
		t.Fatalf("missing file: %v", err)
	}
}

type recRunner struct{ calls []string }

func (r *recRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil, nil
}

// unbind removes the agent, its credential and the repair lines, and keeps
// the firewall boot unit and file and everything the agent applied.
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
	a := &Agent{opt: Options{Root: root, Runner: rr}, cfg: Config{StateDir: state}}
	if err := a.unbind(context.Background()); err != nil {
		t.Fatal(err)
	}
	gone := []string{"etc/plumb-agent/agent.json", "etc/plumb-agent/ca.pem", "usr/local/bin/plumb-agent",
		"etc/systemd/system/plumb-agent.service", "var/lib/plumb-agent"}
	for _, p := range gone {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Errorf("%s still there", p)
		}
	}
	for _, p := range []string{"etc/plumb-agent/firewall.nft", "etc/systemd/system/plumb-agent-firewall.service", "etc/motd"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s removed: %v", p, err)
		}
	}
	keys, _ := os.ReadFile(filepath.Join(root, "root/.ssh/authorized_keys"))
	if string(keys) != "ssh-ed25519 AAAAuser u\n" {
		t.Fatalf("authorized_keys %q", keys)
	}
	if len(rr.calls) != 2 || !strings.HasPrefix(rr.calls[0], "systemctl disable plumb-agent.service") || rr.calls[1] != "systemctl daemon-reload" {
		t.Fatalf("calls %v", rr.calls)
	}
}
