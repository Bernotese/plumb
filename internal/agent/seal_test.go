package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// sealRunner is the fake runner of seal: it records every call, and the
// sync of the agent, in one list.
type sealRunner struct {
	mu       sync.Mutex
	calls    []string
	apt      string           // output of apt-get -s dist-upgrade
	fail     map[string]error // command line → error
	onSync   func()
	onPower  func()
	onDracut func() // runs when dracut is called
}

func (r *sealRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	r.mu.Lock()
	r.calls = append(r.calls, line)
	err, apt, power, dracut := r.fail[line], r.apt, r.onPower, r.onDracut
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	switch {
	case line == "apt-get -s dist-upgrade":
		return []byte(apt), nil
	case strings.HasPrefix(line, "dracut ") && dracut != nil:
		dracut()
	case line == "systemctl poweroff" && power != nil:
		power()
	}
	return nil, nil
}

func (r *sealRunner) sync() {
	r.mu.Lock()
	r.calls = append(r.calls, "sync")
	hook := r.onSync
	r.mu.Unlock()
	if hook != nil {
		hook()
	}
}

func (r *sealRunner) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// buildGuest fills a temp root like a build guest (Ubuntu with dracut and
// rsyslog) and returns the path of a file outside /var/log that a Block
// linked into it (review S6).
func buildGuest(t *testing.T, root string) (shadow string) {
	t.Helper()
	uid, gid := os.Getuid(), os.Getgid()
	write(t, filepath.Join(root, "etc/passwd"), fmt.Sprintf("root:x:%d:%d:root:/root:/bin/bash\nplumb-build:x:1000:1000::/home/plumb-build:/bin/bash\n", uid, gid))
	files := map[string]string{
		"usr/bin/dracut": "", "usr/lib/systemd/system/rsyslog.service": "", "usr/lib/systemd/system/syslog.socket": "",
		"etc/cloud/cloud-init.disabled": "", "etc/ssh/ssh_host_ed25519_key": "k", "etc/ssh/ssh_host_ed25519_key.pub": "k",
		"etc/ssh/sshd_config": "keep", "home/plumb-build/.profile": "", "etc/sudoers.d/90-cloud-init-users": "x",
		"var/lib/systemd/random-seed": "s", "var/lib/systemd/credential.secret": "s",
		"var/log/syslog": "build-host 10.0.0.9 plumb-build", "var/log/syslog.1": "old", "var/log/dpkg.log.2.gz": "old",
		"var/log/apt/history.log": "x", "var/log/journal/0123/system.journal": "j",
		"var/log/cloud-init-output.log": "upgrade log", "tmp/a/b": "x", "var/tmp/c": "x", "root/.bash_history": "ls",
		"etc/shadow":     "root:secret",
		"etc/hostname":   "plumb-build-web-v3\n",
		"etc/machine-id": "0123456789abcdef0123456789abcdef\n",
		"etc/hosts":      "127.0.0.1\tlocalhost\n127.0.1.1\tplumb-build-web-v3.lan plumb-build-web-v3\n10.0.0.5 db plumb-build-web-v3 # build\n::1 localhost ip6-localhost\n",
		"etc/passwd-":    "plumb-build:x:1000", "etc/shadow-": "plumb-build:!", "etc/group-": "plumb-build:x:1000:",
		"etc/gshadow-": "plumb-build:!::", "etc/subuid-": "plumb-build:100000:65536", "etc/subgid-": "plumb-build:100000:65536",
		"usr/lib/systemd/system/systemd-random-seed.service": "",
		"etc/plumb-agent/agent.json":                         "{}", "etc/plumb-agent/ca.pem": "pem", "usr/local/bin/plumb-agent": "bin",
		"usr/local/sbin/plumb-agent-install": "sh", "etc/systemd/system/plumb-agent.service": "[Unit]",
	}
	for p, c := range files {
		write(t, filepath.Join(root, p), c)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "etc/systemd/system/multi-user.target.wants"), 0o755))
	must(os.Symlink("/etc/systemd/system/plumb-agent.service", filepath.Join(root, "etc/systemd/system/multi-user.target.wants/plumb-agent.service")))
	shadow = filepath.Join(root, "etc/shadow")
	// S6: a link and a hard link under /var/log to a file outside it, and
	// a link to a directory.
	must(os.Symlink(shadow, filepath.Join(root, "var/log/evil")))
	must(os.Symlink("/etc/shadow", filepath.Join(root, "var/log/evil-guest")))
	must(os.Link(shadow, filepath.Join(root, "var/log/hard")))
	must(os.Symlink(filepath.Join(root, "etc"), filepath.Join(root, "var/log/etcdir")))
	return shadow
}

const aptClean = "Reading package lists...\n0 upgraded, 0 newly installed, 0 to remove and 3 not upgraded.\n"

// startBuildAgent starts an agent in a build guest root against a fake
// controller whose enrollment answer says build.
func startBuildAgent(t *testing.T, build bool, r *sealRunner) (*testAgent, *controller) {
	t.Helper()
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ctl.build = build
	var root string
	ta := startAgentWith(t, ctl, ca, "tok-1", func(o *Options) {
		root = o.Root
		buildGuest(t, root)
		o.Runner = r
		o.Sync = r.sync
		// The state dir of the guest itself, so resources can aim at it.
		o.Config.StateDir = filepath.Join(root, DefaultStateDir)
	})
	waitFor(t, "enrollment", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
	return ta, ctl
}

// writeUpdateFiles leaves the files of a self-update in a guest root
// (review L1 of #190): .prev, the guard units and the timer's enable link.
func writeUpdateFiles(t *testing.T, root string) {
	t.Helper()
	for _, p := range updateFiles {
		if strings.Contains(p, ".wants/") {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(DefaultUnitDir+"/"+guardTimer, filepath.Join(root, p)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		write(t, filepath.Join(root, p), "x")
	}
	for _, p := range updateFiles {
		if !exists(root, p) {
			t.Fatalf("%s not written", p)
		}
	}
}

func exists(root, p string) bool {
	_, err := os.Lstat(filepath.Join(root, p))
	return err == nil
}

func statuses(items []wire.Item) string {
	var out []string
	for _, it := range items {
		out = append(out, strings.TrimPrefix(it.Resource, "seal:")+"="+string(it.Status))
	}
	return strings.Join(out, " ")
}

// Plan §8 "Agent unit": every step in order, the handshake result →
// remove files → sync → power off, no second result, S6.
func TestSealBuildGuest(t *testing.T) {
	r := &sealRunner{apt: aptClean}
	ta, ctl := startBuildAgent(t, true, r)
	root := ta.root
	shadow := filepath.Join(root, "etc/shadow")
	writeUpdateFiles(t, root)
	r.onSync = func() {
		// At the sync the agent's files are gone already.
		for _, p := range append(append([]string{DefaultStateDir + "/credential"}, agentFiles...), updateFiles...) {
			if exists(root, p) {
				t.Errorf("%s still there at the sync", p)
			}
		}
	}
	powered := make(chan struct{})
	r.onPower = func() { close(powered) }

	ctl.send(t, "s1", wire.CommandSeal, wire.SealBody{PlanID: 7, Upgraded: true})
	rep := ctl.result(t, "s1")
	want := "guard=applied initramfs=applied upgrade=applied log-stop=applied cloud-init=applied host-keys=applied build-user=applied seed=applied logs=applied trim=applied"
	if got := statuses(rep.Items); got != want {
		t.Fatalf("seal result:\n got %s\nwant %s\n%+v", got, want, rep.Items)
	}
	select {
	case <-powered:
	case <-time.After(5 * time.Second):
		t.Fatal("no power off")
	}
	wantCalls := []string{
		"dracut -f --regenerate-all",
		"apt-get -s dist-upgrade",
		"systemctl stop rsyslog.service", "systemctl stop syslog.socket", "journalctl --relinquish-var",
		"cloud-init clean --logs --seed --machine-id --configs all",
		"pkill -KILL -u plumb-build", "userdel -r -f plumb-build",
		"systemctl stop systemd-random-seed.service",
		"apt-get clean", "fstrim --all",
		"sync", "systemctl poweroff",
	}
	if got := r.list(); !slices.Equal(got, wantCalls) {
		t.Fatalf("calls:\n got %q\nwant %q", got, wantCalls)
	}
	if b, _ := os.ReadFile(filepath.Join(root, dracutConf)); !strings.Contains(string(b), `omit_dracutmodules+=" network systemd-networkd "`) {
		t.Fatalf("dracut config = %q", b)
	}
	gone := []string{"etc/cloud/cloud-init.disabled", "etc/ssh/ssh_host_ed25519_key", "etc/ssh/ssh_host_ed25519_key.pub",
		"etc/sudoers.d/90-cloud-init-users", "var/lib/systemd/random-seed", "var/lib/systemd/credential.secret",
		"etc/passwd-", "etc/shadow-", "etc/group-", "etc/gshadow-", "etc/subuid-", "etc/subgid-",
		"var/log/syslog.1", "var/log/dpkg.log.2.gz", "var/log/journal/0123", "var/log/hard", "tmp/a", "var/tmp/c", "root/.bash_history"}
	for _, p := range gone {
		if exists(root, p) {
			t.Errorf("%s still there", p)
		}
	}
	for _, p := range []string{"etc/ssh/sshd_config", "var/log/journal", "tmp", "var/tmp", "var/log/evil", "var/log/etcdir"} {
		if !exists(root, p) {
			t.Errorf("%s removed", p)
		}
	}
	for _, p := range []string{"var/log/syslog", "var/log/apt/history.log", "var/log/cloud-init-output.log"} {
		if fi, err := os.Stat(filepath.Join(root, p)); err != nil || fi.Size() != 0 {
			t.Errorf("%s not empty: %v", p, err)
		}
	}
	// T-066 F1: no build hostname in /etc/hostname or /etc/hosts.
	if b, _ := os.ReadFile(filepath.Join(root, "etc/hostname")); string(b) != "localhost\n" {
		t.Errorf("hostname = %q", b)
	}
	wantHosts := "127.0.0.1\tlocalhost\n10.0.0.5\tdb # build\n::1 localhost ip6-localhost\n"
	if b, _ := os.ReadFile(filepath.Join(root, "etc/hosts")); string(b) != wantHosts {
		t.Errorf("hosts =\n%q\nwant\n%q", b, wantHosts)
	}
	// S6: nothing was written through a link.
	if b, _ := os.ReadFile(shadow); string(b) != "root:secret" {
		t.Fatalf("shadow changed through a link: %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(root, "etc/passwd")); fi == nil || fi.Size() == 0 {
		t.Fatal("a file behind a directory link was emptied")
	}
	// No second result, no poll after the result.
	ctl.mu.Lock()
	polls := len(ctl.pollTimes)
	ctl.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	ctl.mu.Lock()
	after := len(ctl.pollTimes)
	ctl.mu.Unlock()
	if n := len(ctl.reportsOf(wire.ReportResult)); n != 1 || after != polls {
		t.Fatalf("after seal: %d results, polls %d → %d", n, polls, after)
	}
}

// A failed step ends seal: the rest is skipped, and the agent stays.
func TestSealFailedStepKeepsAgent(t *testing.T) {
	r := &sealRunner{apt: "Inst libc6 [2.41-1] (2.41-2 Debian:13)\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"}
	ta, ctl := startBuildAgent(t, true, r)
	ctl.send(t, "s1", wire.CommandSeal, wire.SealBody{PlanID: 7, Upgraded: true})
	rep := ctl.result(t, "s1")
	want := "guard=applied initramfs=applied upgrade=failed log-stop=skipped cloud-init=skipped host-keys=skipped build-user=skipped seed=skipped logs=skipped trim=skipped"
	if got := statuses(rep.Items); got != want {
		t.Fatalf("got %s", got)
	}
	if out := rep.Items[2].Output; !strings.Contains(out, "1 packages left") || !strings.Contains(out, "upgrade log") {
		t.Fatalf("upgrade output = %q", out)
	}
	if out := rep.Items[3].Output; out != "not run: seal:upgrade failed" {
		t.Fatalf("skipped output = %q", out)
	}
	time.Sleep(200 * time.Millisecond)
	for _, c := range r.list() {
		if c == "sync" || c == "systemctl poweroff" || strings.HasPrefix(c, "cloud-init") {
			t.Fatalf("ran %q after a failed step", c)
		}
	}
	if !exists(ta.root, "usr/local/bin/plumb-agent") || storedCred(t, ta.cfg.StateDir) == "" {
		t.Fatal("agent removed after a failed seal")
	}
	// The agent still answers.
	ctl.send(t, "o1", wire.CommandObserve, wire.ObserveBody{})
	ctl.result(t, "o1")
}

// Plan §4: no upgrade check without the upgrade; no dracut step without
// dracut (Debian 13); fstrim without discard is not a failure.
func TestSealSkips(t *testing.T) {
	r := &sealRunner{fail: map[string]error{"fstrim --all": &resource.ExitError{Name: "fstrim", Code: 1, Stderr: "the discard operation is not supported"}}}
	ta, ctl := startBuildAgent(t, true, r)
	for _, p := range []string{"usr/bin/dracut", "usr/lib/systemd/system/rsyslog.service"} {
		if err := os.Remove(filepath.Join(ta.root, p)); err != nil {
			t.Fatal(err)
		}
	}
	ctl.send(t, "s1", wire.CommandSeal, wire.SealBody{PlanID: 7})
	rep := ctl.result(t, "s1")
	want := "guard=applied initramfs=skipped upgrade=skipped log-stop=applied cloud-init=applied host-keys=applied build-user=applied seed=applied logs=applied trim=skipped"
	if got := statuses(rep.Items); got != want {
		t.Fatalf("got %s", got)
	}
	waitFor(t, "power off", func() bool { return slices.Contains(r.list(), "systemctl poweroff") })
	for _, c := range r.list() {
		if strings.HasPrefix(c, "dracut") || strings.HasPrefix(c, "apt-get -s") || strings.Contains(c, "syslog") {
			t.Fatalf("ran %q", c)
		}
	}
	if exists(ta.root, dracutConf) {
		t.Fatal("dracut config written without dracut")
	}
}

// D-207, agent guard: no build mark, or a watch seen → refused, nothing
// changed. Also a Block that writes the mark into the credential file.
func TestSealRefused(t *testing.T) {
	cases := []struct {
		name  string
		build bool
		prep  func(t *testing.T, ta *testAgent, ctl *controller)
	}{
		{"normal agent", false, nil},
		{"build agent that got a watch", true, func(t *testing.T, ta *testAgent, ctl *controller) {
			ctl.send(t, "w1", wire.CommandWatch, wire.WatchBody{})
			ctl.result(t, "w1")
		}},
		{"Block writes the mark", false, func(t *testing.T, ta *testAgent, ctl *controller) {
			forged := `{"credential":"cred-1","build":true,"enrolled":true}`
			res := []wire.Resource{
				fileRes("evil1", "/var/lib/plumb-agent/credential", map[string]any{"content": forged}),
				fileRes("evil2", "/var/lib/plumb-agent/../plumb-agent/credential", map[string]any{"content": forged}),
				fileRes("evil3", "/etc/systemd/system/plumb-agent.service.d/x.conf", map[string]any{"content": "[Service]"}),
				{ID: "service:plumb-agent", Type: "service", Block: "evil4", Attrs: map[string]any{"name": "plumb-agent", "state": "stopped"}},
			}
			ctl.send(t, "a1", wire.CommandApply, wire.ApplyBody{PlanID: 1, Resources: res, Planned: unverified(res...), MaxRisk: wire.RiskDestructive})
			rep := ctl.result(t, "a1")
			for _, it := range rep.Items {
				if it.Status != wire.StatusRefused || !strings.Contains(it.Output, "agent") {
					t.Fatalf("%s: %s %q", it.Resource, it.Status, it.Output)
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &sealRunner{apt: aptClean}
			ta, ctl := startBuildAgent(t, c.build, r)
			if c.prep != nil {
				c.prep(t, ta, ctl)
			}
			ctl.send(t, "s1", wire.CommandSeal, wire.SealBody{PlanID: 7, Upgraded: true})
			rep := ctl.result(t, "s1")
			if len(rep.Items) != 1 || rep.Items[0].Resource != "seal:guard" || rep.Items[0].Status != wire.StatusRefused {
				t.Fatalf("seal answer %+v", rep.Items)
			}
			time.Sleep(100 * time.Millisecond)
			if calls := r.list(); len(calls) > 0 {
				t.Fatalf("a refused seal ran %q", calls)
			}
			for _, p := range []string{"etc/ssh/ssh_host_ed25519_key", "var/log/syslog.1", "usr/local/bin/plumb-agent", "etc/cloud/cloud-init.disabled"} {
				if !exists(ta.root, p) {
					t.Fatalf("%s removed by a refused seal", p)
				}
			}
			id, _, err := ta.a.state.identity()
			if err != nil || id.Build {
				t.Fatalf("identity after refusal: %+v %v", id, err)
			}
		})
	}
}

// Review S1 of T-058: the build mark only from a first enrollment from an
// empty state; a re-enrollment leaves it unset; no enrollment clears the
// watch mark (also after a restart).
func TestBuildMarkRules(t *testing.T) {
	t.Run("first enrollment from an empty state", func(t *testing.T) {
		ta, _ := startBuildAgent(t, true, &sealRunner{})
		if id, _, _ := ta.a.state.identity(); !id.Build || id.Watch || !id.Enrolled || id.Credential != "cred-1" {
			t.Fatalf("identity %+v", id)
		}
	})
	t.Run("re-enrollment with build", func(t *testing.T) {
		ta, ctl := startBuildAgent(t, false, &sealRunner{})
		ctl.mu.Lock()
		delete(ctl.creds, "cred-1")
		ctl.tokens["tok-2"] = true
		ctl.build = true
		ctl.mu.Unlock()
		write(t, ta.cfg.TokenFile, "tok-2\n")
		waitFor(t, "second enrollment", func() bool { return storedCred(t, ta.cfg.StateDir) == "cred-2" })
		if id, _, _ := ta.a.state.identity(); id.Build {
			t.Fatalf("re-enrollment set the build mark: %+v", id)
		}
		// A build agent that enrolls again loses its mark too.
		s := stateDir{dir: t.TempDir() + "/s"}
		if err := s.ensure(); err != nil {
			t.Fatal(err)
		}
		if id, _ := s.enrolled("c1", true); !id.Build {
			t.Fatal("first build enrollment")
		}
		if err := s.dropCredential(); err != nil {
			t.Fatal(err)
		}
		if id, _ := s.enrolled("c2", true); id.Build {
			t.Fatal("re-enrollment after a 401 kept the build mark")
		}
	})
	t.Run("desired list is no empty state", func(t *testing.T) {
		s := stateDir{dir: t.TempDir() + "/s"}
		if err := s.ensure(); err != nil {
			t.Fatal(err)
		}
		if err := s.saveDesired(nil); err != nil {
			t.Fatal(err)
		}
		if id, _ := s.enrolled("c1", true); id.Build {
			t.Fatal("build mark with a desired list on disk")
		}
	})
	t.Run("old credential file", func(t *testing.T) {
		s := stateDir{dir: t.TempDir() + "/s"}
		if err := s.ensure(); err != nil {
			t.Fatal(err)
		}
		if err := s.write(credentialFile, []byte("old-cred\n")); err != nil {
			t.Fatal(err)
		}
		if id, _, _ := s.identity(); id.Credential != "old-cred" || id.Build || !id.Enrolled {
			t.Fatalf("old file reads as %+v", id)
		}
		if id, _ := s.enrolled("c2", true); id.Build {
			t.Fatal("an agent with an old credential file took the build mark")
		}
	})
	t.Run("watch mark survives enrollment and restart", func(t *testing.T) {
		ta, ctl := startBuildAgent(t, true, &sealRunner{})
		ctl.send(t, "w1", wire.CommandWatch, wire.WatchBody{})
		ctl.result(t, "w1")
		if id, _, _ := ta.a.state.identity(); !id.Watch || id.Build {
			t.Fatalf("after watch: %+v", id)
		}
		ctl.mu.Lock()
		delete(ctl.creds, "cred-1")
		ctl.tokens["tok-2"] = true
		ctl.mu.Unlock()
		write(t, ta.cfg.TokenFile, "tok-2\n")
		waitFor(t, "second enrollment", func() bool { return storedCred(t, ta.cfg.StateDir) == "cred-2" })
		ta.cancel()
		<-ta.done
		// A restart reads the mark from disk.
		a, err := New(ta.a.opt)
		if err != nil {
			t.Fatal(err)
		}
		if id, _, _ := a.state.identity(); !id.Watch || id.Build {
			t.Fatalf("after re-enrollment and restart: %+v", id)
		}
		if why := a.mayNotSeal(); !strings.Contains(why, "watch") {
			t.Fatalf("mayNotSeal = %q", why)
		}
	})
}

// Review S1 of T-059: the agent refuses a path of its own also through a
// link that exists only in the guest.
func TestOwnPathThroughGuestLinks(t *testing.T) {
	ta, _ := startBuildAgent(t, false, &sealRunner{})
	root := ta.root
	for link, target := range map[string]string{
		"opt/x": "/var/lib", "opt/u": "/etc/systemd/system", "opt/rel": "../var/lib/plumb-agent",
		"opt/loop": "/opt/loop", "srv/up": "/usr/local",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(link)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	refused := []wire.Resource{
		fileRes("b", "/opt/x/plumb-agent/credential", nil),
		fileRes("b", "/opt/u/plumb-agent.service", nil),
		fileRes("b", "/opt/u/multi-user.target.wants/plumb-agent.service", nil),
		fileRes("b", "/opt/rel/desired.json", nil),
		fileRes("b", "/srv/up/bin/plumb-agent", nil),
		fileRes("b", "/opt/loop/x", nil),
		{ID: "directory:/opt/x/plumb-agent", Type: "directory", Attrs: map[string]any{"path": "/opt/x/plumb-agent"}},
		{ID: "symlink:/srv/l", Type: "symlink", Attrs: map[string]any{"path": "/srv/l", "target": "/opt/x/plumb-agent/credential"}},
		{ID: "symlink:/opt/x/plumb-agent", Type: "symlink", Attrs: map[string]any{"path": "/opt/x/plumb-agent", "target": "/tmp"}},
		{ID: "symlink:/opt/u/rel", Type: "symlink", Attrs: map[string]any{"path": "/opt/u/rel", "target": "plumb-agent.service"}},
		{ID: "user:x", Type: "user", Attrs: map[string]any{"name": "x", "home": "/opt/x/plumb-agent"}},
		{ID: "service:plumb-agent.service", Type: "service", Attrs: map[string]any{"name": "plumb-agent.service"}},
	}
	for _, r := range refused {
		if why := ta.a.ownPathRefusal(r); why == "" {
			t.Errorf("%s not refused", r.ID)
		}
	}
	allowed := []wire.Resource{
		fileRes("b", "/opt/x/other/file", nil),
		fileRes("b", "/etc/nginx/nginx.conf", nil),
		{ID: "symlink:/srv/l", Type: "symlink", Attrs: map[string]any{"path": "/srv/l", "target": "/opt/x/other"}},
		{ID: "service:nginx", Type: "service", Attrs: map[string]any{"name": "nginx"}},
	}
	for _, r := range allowed {
		if why := ta.a.ownPathRefusal(r); why != "" {
			t.Errorf("%s refused: %s", r.ID, why)
		}
	}
}

func TestAptPending(t *testing.T) {
	cases := []struct {
		out string
		n   int
		ok  bool
	}{
		{aptClean, 0, true},
		{"Calculating upgrade...\nThe following upgrades have been deferred due to phasing:\n  libfoo\n0 upgraded, 0 newly installed, 0 to remove and 4 not upgraded.\n", 0, true},
		{"Inst a (1 x)\nInst b (2 y)\n2 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 2, true},
		{"0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.\n", 1, true},
		{"Summary:\n  Upgrading: 0, Installing: 0, Removing: 0, Not Upgrading: 3\n", 0, true},
		{"Summary:\n  Upgrading: 5, Installing: 1, Removing: 0, Not Upgrading: 0\n", 6, true},
		{"Inst a (1 x)\nSummary:\n  Upgrading: 0, Installing: 0, Removing: 0, Not Upgrading: 0\n", 1, true},
		{"E: Could not get lock\n", 0, false},
	}
	for _, c := range cases {
		n, ok := aptPending(c.out)
		if n != c.n || ok != c.ok {
			t.Errorf("aptPending(%q) = %d %v, want %d %v", c.out, n, ok, c.n, c.ok)
		}
	}
}

// When a file of the agent cannot be removed, the guest is not powered
// off: the controller times out and deletes it (review B1).
func TestSealNoPowerOffWhenRemoveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	r := &sealRunner{apt: aptClean}
	ta, ctl := startBuildAgent(t, true, r)
	dir := filepath.Join(ta.root, "usr/local/bin")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	ctl.send(t, "s1", wire.CommandSeal, wire.SealBody{PlanID: 7})
	ctl.result(t, "s1")
	time.Sleep(300 * time.Millisecond)
	for _, c := range r.list() {
		if c == "sync" || c == "systemctl poweroff" {
			t.Fatalf("ran %q although the binary is still there", c)
		}
	}
}

func TestCleanHosts(t *testing.T) {
	cases := []struct{ in, want string }{
		{"127.0.1.1 b-v1\n", ""},
		{"127.0.1.1 b-v1.lan b-v1\n", ""},
		{"127.0.0.1 localhost b-v1\n", "127.0.0.1\tlocalhost\n"},
		{"# b-v1 in a comment\n127.0.0.1 localhost\n", "# b-v1 in a comment\n127.0.0.1 localhost\n"},
		{"10.0.0.1 b-v10 xb-v1 b-v1x\n", "10.0.0.1 b-v10 xb-v1 b-v1x\n"},
		{"127.0.0.1 localhost", "127.0.0.1 localhost"},
	}
	for _, c := range cases {
		if got := cleanHosts(c.in, "b-v1"); got != c.want {
			t.Errorf("cleanHosts(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// T-066 F1 with a link: /etc/hosts or /etc/hostname as a symbolic link,
// or /etc/hosts as a hard link,
// fails the step; the target is not written, and the guest stays on.
func TestSealHostnameLinkFails(t *testing.T) {
	for _, c := range []struct {
		name string
		hard bool
	}{{"etc/hosts", false}, {"etc/hostname", false}, {"etc/hosts", true}} {
		name := c.name
		t.Run(fmt.Sprintf("%s hard=%v", name, c.hard), func(t *testing.T) {
			r := &sealRunner{apt: aptClean}
			ta, ctl := startBuildAgent(t, true, r)
			canary := filepath.Join(ta.root, "canary")
			write(t, canary, "127.0.1.1 plumb-build-web-v3 # must stay\n")
			p := filepath.Join(ta.root, name)
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			link := os.Symlink
			if c.hard {
				link = os.Link
			}
			if err := link(canary, p); err != nil {
				t.Fatal(err)
			}
			ctl.send(t, "s1", wire.CommandSeal, wire.SealBody{PlanID: 7})
			rep := ctl.result(t, "s1")
			// The first step that touches the file fails: initramfs (the
			// fixture has dracut, so the reset runs before it).
			if got := statuses(rep.Items); !strings.Contains(got, "initramfs=failed upgrade=skipped") {
				t.Fatalf("got %s", got)
			}
			if b, _ := os.ReadFile(canary); string(b) != "127.0.1.1 plumb-build-web-v3 # must stay\n" {
				t.Fatalf("written through the link: %q", b)
			}
			time.Sleep(200 * time.Millisecond)
			if slices.Contains(r.list(), "systemctl poweroff") {
				t.Fatal("powered off after a failed step")
			}
		})
	}
}

// T-066 re-check: dracut copies /etc/hostname, /etc/hosts and
// /etc/machine-id into the initrd, so seal resets them BEFORE it runs
// dracut (a clone's first-boot journal held the build name). The order of
// the step ids on the wire stays the same.
func TestSealResetsIdentityBeforeDracut(t *testing.T) {
	r := &sealRunner{apt: aptClean}
	ta, ctl := startBuildAgent(t, true, r)
	root := ta.root
	type seen struct{ hostname, hosts, machineID string }
	var at seen
	r.onDracut = func() {
		read := func(p string) string { b, _ := os.ReadFile(filepath.Join(root, p)); return string(b) }
		at = seen{read("etc/hostname"), read("etc/hosts"), read("etc/machine-id")}
	}
	ctl.send(t, "s1", wire.CommandSeal, wire.SealBody{PlanID: 7})
	rep := ctl.result(t, "s1")
	if got := statuses(rep.Items); !strings.HasPrefix(got, "guard=applied initramfs=applied") {
		t.Fatalf("got %s", got)
	}
	if at.hostname != "localhost\n" || strings.Contains(at.hosts, "plumb-build") || at.machineID != "uninitialized\n" {
		t.Fatalf("when dracut ran: %+v", at)
	}
	// The wire order is unchanged: the engine and the Run view expect it.
	var ids []string
	for _, it := range rep.Items {
		ids = append(ids, strings.TrimPrefix(it.Resource, "seal:"))
	}
	if !slices.Equal(ids, wire.SealSteps) {
		t.Fatalf("step ids %v, want %v", ids, wire.SealSteps)
	}
}

// D-252, D-253: seal in a container. No dracut, no cloud-init, no fstrim;
// the upgrade runs in seal; machine-id emptied; the SSH host key unit is
// written and enabled; the first-boot installer stays, the agent goes.
func TestSealContainer(t *testing.T) {
	r := &sealRunner{apt: aptClean}
	ta, ctl := startBuildAgent(t, true, r)
	root := ta.root
	write(t, filepath.Join(root, "etc/systemd/system/plumb-agent-install.service"), "[Unit]")
	if err := os.Symlink("/etc/systemd/system/plumb-agent-install.service", filepath.Join(root, "etc/systemd/system/multi-user.target.wants/plumb-agent-install.service")); err != nil {
		t.Fatal(err)
	}
	writeUpdateFiles(t, root)
	powered := make(chan struct{})
	r.onPower = func() { close(powered) }
	ctl.send(t, "s1", wire.CommandSeal, wire.SealBody{PlanID: 7, Upgraded: true, Container: true})
	rep := ctl.result(t, "s1")
	want := "guard=applied initramfs=skipped upgrade=applied log-stop=applied cloud-init=applied host-keys=applied build-user=applied seed=applied logs=applied trim=skipped"
	if out := rep.Items[2].Output; !strings.Contains(out, "no package removed") {
		t.Errorf("upgrade output %q", out)
	}
	if got := statuses(rep.Items); got != want {
		t.Fatalf("seal result:\n got %s\nwant %s\n%+v", got, want, rep.Items)
	}
	select {
	case <-powered:
	case <-time.After(5 * time.Second):
		t.Fatal("no power off")
	}
	wantCalls := []string{
		"apt-get -q -o DPkg::Lock::Timeout=300 update",
		"apt-get -s dist-upgrade",
		"apt-get -q -y -o DPkg::Lock::Timeout=300 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold dist-upgrade",
		"apt-get -s dist-upgrade",
		"systemctl stop rsyslog.service", "systemctl stop syslog.socket", "journalctl --relinquish-var",
		"systemctl enable plumb-ssh-hostkeys.service",
		"pkill -KILL -u plumb-build", "userdel -r -f plumb-build",
		"systemctl stop systemd-random-seed.service",
		"apt-get clean",
		"sync", "systemctl poweroff",
	}
	if got := r.list(); !slices.Equal(got, wantCalls) {
		t.Fatalf("calls:\n got %q\nwant %q", got, wantCalls)
	}
	if b, err := os.ReadFile(filepath.Join(root, "etc/machine-id")); err != nil || len(b) != 0 {
		t.Errorf("machine-id %q %v", b, err)
	}
	unit, _ := os.ReadFile(filepath.Join(root, hostKeyUnitPath))
	for _, part := range []string{"DefaultDependencies=no", "Before=ssh.service ssh.socket sshd-keygen.service sockets.target", "ExecStart=/usr/bin/ssh-keygen -A", "WantedBy=sysinit.target"} {
		if !strings.Contains(string(unit), part) {
			t.Errorf("unit misses %q:\n%s", part, unit)
		}
	}
	for _, p := range append([]string{"etc/ssh/ssh_host_ed25519_key", "usr/local/bin/plumb-agent", DefaultStateDir}, updateFiles...) {
		if exists(root, p) {
			t.Errorf("%s still there", p)
		}
	}
	// Review B1 of #174: the installer starts the agent through its unit.
	for _, p := range []string{"etc/systemd/system/plumb-agent.service", "etc/systemd/system/multi-user.target.wants/plumb-agent.service",
		"etc/plumb-agent/agent.json", "etc/plumb-agent/ca.pem", "usr/local/sbin/plumb-agent-install",
		"etc/systemd/system/plumb-agent-install.service", "etc/systemd/system/multi-user.target.wants/plumb-agent-install.service",
		"etc/cloud/cloud-init.disabled"} {
		if !exists(root, p) {
			t.Errorf("%s removed", p)
		}
	}
}

// Review N2 of #174: the upgrade in a container names what it removed.
func TestSealContainerNamesRemovedPackages(t *testing.T) {
	r := &sealRunner{apt: aptClean + "Remv openssh-server [1:10.0p1-5]\nRemv xauth [1:1.1.2-1]\n"}
	_, ctl := startBuildAgent(t, true, r)
	ctl.send(t, "s1", wire.CommandSeal, wire.SealBody{PlanID: 7, Upgraded: true, Container: true})
	rep := ctl.result(t, "s1")
	if out := rep.Items[2].Output; !strings.Contains(out, "removed by the upgrade: openssh-server, xauth") {
		t.Errorf("upgrade output %q", out)
	}
}
