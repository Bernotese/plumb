//go:build guest

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/blocks"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
	"go.yaml.in/yaml/v3"
)

// T-095: the slice 3 starter Blocks applied by the agent in the guest,
// rendered from blocks/*.yaml by the controller's own code.

// starterRender renders a Target with the given Block uses and fills in
// the secret values, as the controller does before it sends an apply.
func starterRender(t *testing.T, uses []any, secrets map[string]string) []wire.Resource {
	t.Helper()
	var docs []state.Doc
	add := func(value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var doc state.Doc
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		docs = append(docs, doc)
	}
	for _, name := range []string{"ssh-hardening", "common-tools", "shell", "docker-compose", "deploy-user"} {
		raw, err := blocks.FS.ReadFile(name + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := yaml.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		add(value)
	}
	add(map[string]any{"kind": "Image", "name": "guest", "spec": map[string]any{"type": "vm", "url": "https://images.invalid/guest.img", "sha256": fmt.Sprintf("%064d", 0)}})
	add(map[string]any{"kind": "Target", "name": "guest", "spec": map[string]any{"type": "vm", "image": "guest", "guest": map[string]any{"cores": 1, "memory": 512, "disk": 8, "bridge": "vmbr0"}, "blocks": uses}})
	set, err := state.Validate(docs)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render("guest")
	if err != nil {
		t.Fatal(err)
	}
	list, err := state.WithSecrets(rendered.Resources, secrets)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

type starterGuest struct {
	t   *testing.T
	ctx context.Context
	a   *Agent
}

func newStarterGuest(t *testing.T, ctx context.Context) *starterGuest {
	t.Helper()
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || string(comm) != "systemd\n" {
		t.Fatal("requires root in the systemd guest")
	}
	ca := newCA(t)
	ctl := newController(t, ca, "guest-token")
	a, err := New(Options{Root: "/", Config: Config{URL: ctl.srv.URL, CA: ca.pem, TokenFile: filepath.Join(t.TempDir(), "token"), StateDir: filepath.Join(t.TempDir(), "state")}})
	if err != nil {
		t.Fatal(err)
	}
	return &starterGuest{t: t, ctx: ctx, a: a}
}

// apply applies the list and fails on a step that did not end applied or
// unchanged. It returns the items by resource id.
func (g *starterGuest) apply(id string, list []wire.Resource) map[string]wire.Item {
	g.t.Helper()
	items, err := g.a.apply(g.ctx, id, wire.ApplyBody{Resources: list, Planned: unverified(list...), MaxRisk: wire.RiskDisruptive})
	if err != nil {
		g.t.Fatal(err)
	}
	if len(items) != len(list) {
		g.t.Fatalf("missing apply results: %+v", items)
	}
	out := map[string]wire.Item{}
	for _, it := range items {
		out[it.Resource] = it
		if it.Status != wire.StatusApplied && it.Status != wire.StatusUnchanged {
			g.t.Errorf("%s: unsuccessful step: %+v", id, it)
		}
	}
	if g.t.Failed() {
		g.t.FailNow()
	}
	return out
}

// applyTwice applies the list, then once more: the second round must
// change nothing.
func (g *starterGuest) applyTwice(id string, list []wire.Resource) map[string]wire.Item {
	g.t.Helper()
	first := g.apply(id, list)
	for _, it := range g.apply(id+"-again", list) {
		if it.Status != wire.StatusUnchanged {
			g.t.Errorf("second round changed %s: %+v", it.Resource, it)
		}
	}
	return first
}

func (g *starterGuest) run(name string, args ...string) string {
	g.t.Helper()
	out, err := (resource.ExecRunner{}).Run(g.ctx, name, args...)
	if err != nil {
		g.t.Fatalf("%s %v: %s: %v", name, args, out, err)
	}
	return string(out)
}

func osRelease(t *testing.T) (id, codename string) {
	t.Helper()
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, _ := strings.Cut(line, "=")
		v = strings.Trim(v, `"`)
		switch k {
		case "ID":
			id = v
		case "VERSION_CODENAME":
			codename = v
		}
	}
	return id, codename
}

func TestGuestStarterShellAndTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	g := newStarterGuest(t, ctx)
	list := starterRender(t, []any{
		map[string]any{"use": "common-tools"},
		map[string]any{"use": "shell", "params": map[string]any{"history_size": "5000"}},
	}, nil)
	g.applyTwice("tools", list)
	for _, bin := range []string{"curl", "git", "htop", "jq", "lsof", "rsync", "tmux", "unzip", "vim", "dig", "ping"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Errorf("%s is not installed: %v", bin, err)
		}
	}
	// A bash login shell gets the settings and the aliases.
	out := g.run("bash", "-lc", `echo "$EDITOR $HISTSIZE $HISTTIMEFORMAT"; alias ll`)
	if !strings.Contains(out, "vim 5000 %F %T") || !strings.Contains(out, "alias ll='ls -alF'") {
		t.Errorf("login shell: %q", out)
	}
	// A POSIX sh login shell ignores the bash-only file.
	if out := g.run("sh", "-lc", `echo "ok:$HISTSIZE"`); strings.TrimSpace(out) != "ok:" {
		t.Errorf("sh login shell: %q", out)
	}
}

func TestGuestStarterSSHHardening(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	g := newStarterGuest(t, ctx)
	g.run("apt-get", "-q", "-y", "install", "--no-install-recommends", "sshpass", "openssh-client")
	distro, _ := osRelease(t)
	enabledBefore := g.run("sh", "-c", "systemctl is-enabled ssh.service ssh.socket || true")

	dir := t.TempDir()
	key := filepath.Join(dir, "id")
	g.run("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	// The cloud-init bootstrap user: a key, also a password (to prove that
	// passwords stop working), and root with the same key.
	const boot, password = "plumbboot", "Guest-pw-73915"
	g.run("useradd", "-m", "-s", "/bin/bash", boot)
	t.Cleanup(func() { _ = exec.Command("userdel", "-r", boot).Run() })
	g.run("sh", "-c", fmt.Sprintf("echo '%s:%s' | chpasswd", boot, password))
	for _, home := range []string{"/home/" + boot, "/root"} {
		owner := boot
		if home == "/root" {
			owner = "root"
		}
		write(t, home+"/.ssh/authorized_keys", string(pub))
		g.run("chown", "-R", owner+":", home+"/.ssh")
		g.run("chmod", "700", home+"/.ssh")
	}
	t.Cleanup(func() { _ = os.Remove("/root/.ssh/authorized_keys") })
	// cloud-init writes a later drop-in that allows passwords.
	write(t, "/etc/ssh/sshd_config.d/50-cloud-init.conf", "PasswordAuthentication yes\n")
	t.Cleanup(func() { _ = os.Remove("/etc/ssh/sshd_config.d/50-cloud-init.conf") })

	opts := []string{"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=5", "-o", "LogLevel=ERROR"}
	login := func(user string, withKey bool) bool {
		args := append([]string{}, opts...)
		name := "ssh"
		if withKey {
			args = append(args, "-o", "BatchMode=yes", "-o", "PasswordAuthentication=no", "-i", key, user+"@127.0.0.1", "true")
		} else {
			name = "sshpass"
			args = append([]string{"-p", password, "ssh"}, append(args, "-o", "PreferredAuthentications=password", "-o", "PubkeyAuthentication=no", user+"@127.0.0.1", "true")...)
		}
		return exec.CommandContext(ctx, name, args...).Run() == nil
	}
	g.run("systemctl", "restart", "ssh.service")
	if !login(boot, false) || !login(boot, true) {
		t.Fatal("logins do not work before the Block")
	}

	pubKey := strings.TrimSpace(string(pub))
	list := starterRender(t, []any{
		map[string]any{"use": "ssh-hardening"},
		map[string]any{"use": "deploy-user", "params": map[string]any{"name": "deploy", "ssh_key": pubKey}},
	}, nil)
	t.Cleanup(func() {
		for _, p := range []string{"/etc/ssh/sshd_config.d/10-plumb-hardening.conf", "/etc/systemd/system/plumb-ssh-hardening.path", "/etc/systemd/system/plumb-ssh-hardening-reload.service"} {
			_ = os.Remove(p)
		}
		_ = exec.Command("systemctl", "disable", "--now", "plumb-ssh-hardening.path").Run()
		_ = exec.Command("systemctl", "daemon-reload").Run()
		_ = exec.Command("systemctl", "try-restart", "ssh.service").Run()
		_ = exec.Command("userdel", "-r", "deploy").Run()
	})
	g.applyTwice("ssh", list)
	g.run("sh", "-c", "mkdir -p -m 0755 /run/sshd && sshd -t")

	// The path unit restarts a running sshd shortly after the drop-in
	// changes. A login during the restart fails, so wait for the whole
	// expected state at once.
	eventually := func(what string, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(30 * time.Second); !ok(); time.Sleep(500 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatal(what)
			}
		}
	}
	keysWork := func(users ...string) bool {
		for _, u := range users {
			if !login(u, true) {
				return false
			}
		}
		return true
	}
	eventually("after the Block: password login still works, or a key login fails (lockout)", func() bool {
		return !login(boot, false) && keysWork(boot, "deploy", "root")
	})
	// A broken drop-in (drift by hand) fails sshd -t: no restart, the
	// running sshd keeps the last good settings and every key still logs in.
	const dropIn = "/etc/ssh/sshd_config.d/10-plumb-hardening.conf"
	pidBefore := g.run("systemctl", "show", "-p", "MainPID", "--value", "ssh.service")
	good, err := os.ReadFile(dropIn)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dropIn, string(good)+"PermitRootLogin maybe\n")
	deadline := time.Now().Add(20 * time.Second)
	for {
		state, _ := exec.Command("systemctl", "is-failed", "plumb-ssh-hardening-reload.service").Output()
		if strings.TrimSpace(string(state)) == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the reload unit did not refuse the broken drop-in: %q", state)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if pid := g.run("systemctl", "show", "-p", "MainPID", "--value", "ssh.service"); pid != pidBefore {
		t.Errorf("sshd restarted with a broken drop-in: pid %q → %q", pidBefore, pid)
	}
	for _, user := range []string{boot, "deploy", "root"} {
		if !login(user, true) {
			t.Errorf("key login of %s fails with a broken drop-in", user)
		}
	}
	// The next apply repairs the drift.
	if it := g.apply("ssh-repair", list)["file:"+dropIn]; it.Status != wire.StatusApplied {
		t.Errorf("drift not repaired: %+v", it)
	}
	g.run("systemctl", "reset-failed", "plumb-ssh-hardening-reload.service")
	g.run("sh", "-c", "mkdir -p -m 0755 /run/sshd && sshd -t")

	// The start mode of sshd is not touched (Debian: ssh.service, Ubuntu: ssh.socket).
	if after := g.run("sh", "-c", "systemctl is-enabled ssh.service ssh.socket || true"); after != enabledBefore {
		t.Errorf("%s: ssh start mode changed: %q → %q", distro, enabledBefore, after)
	}

	// A changed parameter goes through the same path: root without login.
	list = starterRender(t, []any{
		map[string]any{"use": "ssh-hardening", "params": map[string]any{"permit_root_login": "no"}},
		map[string]any{"use": "deploy-user", "params": map[string]any{"name": "deploy", "ssh_key": pubKey}},
	}, nil)
	g.apply("ssh-root-no", list)
	eventually("permit_root_login no: root still logs in, or a user key fails", func() bool {
		return !login("root", true) && keysWork(boot, "deploy")
	})
}

func TestGuestStarterDockerCompose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	g := newStarterGuest(t, ctx)
	distro, suite := osRelease(t)
	// A canary in the secret: the app serves it, nothing may log it.
	const canary = "plumb-compose-canary-5c1e9"
	compose := func(port int) string {
		return fmt.Sprintf(`services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", "echo \"$$GREETING\" > /www/index.html && httpd -f -p 8080 -h /www"]
    environment:
      GREETING: ${GREETING}
    ports: ["127.0.0.1:%d:8080"]
    tmpfs: [/www]
    restart: unless-stopped
`, port)
	}
	uses := func(port int) []any {
		return []any{map[string]any{"use": "docker-compose", "params": map[string]any{
			"name": "whoami", "compose": compose(port), "env": map[string]any{"secret": "whoami-env"},
			"distro": distro, "suite": suite,
		}}}
	}
	secrets := map[string]string{"whoami-env": "GREETING=" + canary + "\n"}
	t.Cleanup(func() {
		c := exec.Command("docker", "compose", "--project-name", "whoami", "down", "--rmi", "all")
		c.Dir = "/opt/plumb-compose-whoami"
		_ = c.Run()
		_ = exec.Command("systemctl", "disable", "--now", "plumb-compose-whoami.service").Run()
		for _, p := range []string{"/etc/systemd/system/plumb-compose-whoami.service", "/opt/plumb-compose-whoami", "/etc/apt/sources.list.d/plumb-docker.sources", "/etc/apt/keyrings/plumb-docker.asc", "/etc/sysctl.d/60-plumb-net.ipv4.ip_forward.conf"} {
			_ = os.RemoveAll(p)
		}
		_ = exec.Command("systemctl", "daemon-reload").Run()
		_ = exec.Command("apt-get", "-q", "-y", "purge", "docker-ce", "docker-ce-cli", "containerd.io", "docker-compose-plugin").Run()
		_ = exec.Command("apt-get", "-q", "-y", "autoremove").Run()
		_ = exec.Command("apt-get", "-q", "update").Run()
	})
	g.run("apt-get", "-q", "-y", "install", "--no-install-recommends", "curl")

	get := func(port int) string {
		g.t.Helper()
		var out []byte
		var err error
		for end := time.Now().Add(60 * time.Second); time.Now().Before(end); time.Sleep(time.Second) {
			out, err = exec.CommandContext(ctx, "curl", "--fail", "--silent", "--noproxy", "*", "--max-time", "5", fmt.Sprintf("http://127.0.0.1:%d/", port)).Output()
			if err == nil {
				return string(out)
			}
		}
		t.Fatalf("app on port %d: %v", port, err)
		return ""
	}

	items := g.applyTwice("compose", starterRender(t, uses(18081), secrets))
	if got := get(18081); strings.TrimSpace(got) != canary {
		t.Fatalf("app served %q", got)
	}
	if st, err := os.Stat("/opt/plumb-compose-whoami/.env"); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf(".env: %v %v", st, err)
	}
	if v := strings.TrimSpace(g.run("sysctl", "-n", "net.ipv4.ip_forward")); v != "1" {
		t.Errorf("ip_forward = %s", v)
	}

	// A changed compose file restarts the unit: the app moves to a new port.
	items2 := g.apply("compose-port", starterRender(t, uses(18082), secrets))
	if got := get(18082); strings.TrimSpace(got) != canary {
		t.Fatalf("app after the change served %q", got)
	}
	if it := items2["service:plumb-compose-whoami.service"]; it.Status != wire.StatusApplied {
		t.Errorf("no restart of the compose unit: %+v", it)
	}

	// The secret is in no report and not in the journal.
	for _, set := range []map[string]wire.Item{items, items2} {
		raw, _ := json.Marshal(set)
		if strings.Contains(string(raw), canary) {
			t.Errorf("a report holds the secret: %s", raw)
		}
	}
	journal := g.run("journalctl", "--no-pager", "-o", "cat", "-u", "plumb-compose-whoami.service", "-u", "docker.service", "-u", "containerd.service")
	if strings.Contains(journal, canary) {
		t.Error("the journal holds the secret")
	}
	if all := g.run("journalctl", "--no-pager", "-o", "cat", "--since", "-15min"); strings.Contains(all, canary) {
		t.Error("the system journal holds the secret")
	}
}
