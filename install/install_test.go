// Tests of install/install.sh against fake Proxmox commands
// (testdata/fakepve). They need bash; they run in make test.
package install

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const fakeSecret = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"

// fakeNames are the commands that testdata/fakepve plays.
var fakeNames = []string{"pveversion", "dpkg-query", "dpkg", "pveum", "pvesm", "pvesh", "pveam", "pct", "hostname", "ip", "install", "lxc-attach", "stat"}

type host struct {
	t       *testing.T
	state   string // fake state
	bin     string // fake commands, first in PATH
	plumb   string // a fake plumb binary
	pveCA   string
	snippet string // snippet dir
	stateF  string // install.state
	env     []string
}

func newHost(t *testing.T) *host {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	dir := t.TempDir()
	h := &host{t: t, state: filepath.Join(dir, "state"), bin: filepath.Join(dir, "bin"),
		plumb: filepath.Join(dir, "plumb"), pveCA: filepath.Join(dir, "pve-root-ca.pem"),
		snippet: filepath.Join(dir, "snippets"), stateF: filepath.Join(dir, "etc", "install.state")}
	for _, d := range []string{h.state, h.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fake, err := filepath.Abs("testdata/fakepve")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range fakeNames {
		if err := os.Symlink(fake, filepath.Join(h.bin, n)); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(h.plumb, []byte("#!/bin/sh\necho plumb\n"), 0o755)
	// Like a host with systemd-resolved off: a LAN resolver and a search domain.
	os.WriteFile(filepath.Join(dir, "resolv.conf"), []byte("search plumb.test\nnameserver 192.0.2.53\n"), 0o644)
	os.WriteFile(h.pveCA, []byte("-----BEGIN CERTIFICATE-----\nMIIBpveroot\n-----END CERTIFICATE-----\n"), 0o644)
	h.env = append(os.Environ(),
		"PATH="+h.bin+":"+os.Getenv("PATH"),
		"FAKE_STATE="+h.state,
		"FAKE_SECRET="+fakeSecret,
		"PLUMB_INSTALL_PVE_CA="+h.pveCA,
		"PLUMB_INSTALL_STATE="+h.stateF,
		"PLUMB_INSTALL_LXC_DIR="+filepath.Join(dir, "lxc"),
		"PLUMB_INSTALL_RESOLV="+filepath.Join(dir, "resolv.conf"),
		// The tests have no terminal; the fake plumb key init does not check.
		// TestKeyInitNeedsTerminal sets it back to 0.
		"PLUMB_INSTALL_ASSUME_TTY=1",
	)
	return h
}

// run runs install.sh with stdin closed (no terminal).
func (h *host) run(env []string, args ...string) (int, string, string) {
	h.t.Helper()
	cmd := exec.Command("bash", append([]string{"install.sh", "--snippet-dir", h.snippet, "--image-dir", h.images()}, args...)...)
	cmd.Env = append(append([]string{}, h.env...), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		h.t.Fatal(err)
	}
	return code, out.String(), errOut.String()
}

func (h *host) images() string { return filepath.Join(filepath.Dir(h.state), "images") }

func (h *host) calls() []string {
	b, _ := os.ReadFile(filepath.Join(h.state, "calls"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// mutating reports a fake call that changes the host.
var mutating = regexp.MustCompile(`^(pveum (pool (add|delete)|role (add|modify|delete)|user (add|delete)|acl (modify|delete)|user token (add|remove))|` +
	`pvesm (add|set|remove|free)|pveam (update|download)|pct (create|start|stop|destroy|push|pull|exec [0-9]+ -- (install|sh|chown|chmod|systemctl))|install |lxc-attach )`)

func TestDryRunChangesNothing(t *testing.T) {
	h := newHost(t)
	code, out, errOut := h.run(nil, "--dry-run", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	for _, c := range h.calls() {
		if mutating.MatchString(c) {
			t.Errorf("dry run ran a changing command: %s", c)
		}
	}
	// Every change is printed.
	for _, want := range []string{
		"+ pveum pool add plumb",
		"+ pveum user add plumb@pve",
		`+ pveum acl modify /pool/plumb --tokens plumb@pve\!plumb --roles PlumbGuests --propagate 1`,
		"+ pveum acl modify /storage/plumb-snippets --tokens plumb@pve\\!plumb --roles PlumbSnippets --propagate 0",
		"+ pveum user token add plumb@pve plumb --privsep 1",
		"+ pvesm add dir plumb-snippets --path " + h.snippet + " --content snippets --shared 0",
		"+ pvesm add dir plumb-images --path " + h.images() + " --content import --shared 0",
		"+ pveam download local debian-13-standard_13.1-2_amd64.tar.zst",
		"+ pct create 105 local:vztmpl/debian-13-standard_13.1-2_amd64.tar.zst --hostname plumb --unprivileged 1",
		"+ pct push 105 " + h.plumb + " /usr/local/bin/plumb --perms 0755",
		"/etc/plumb/plumb.env --perms 0600",
		"+ pct exec 105 -- systemctl restart plumb.service",
		"--- snippet " + h.snippet + "/snippets/plumb-vendor.yaml ---",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run does not print %q", want)
		}
	}
	// The LXC has no host mount and is not in the Plumb pool (§7). Its one
	// mount point is a new volume on the disk storage: the key volume.
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "+ pct create") {
			continue
		}
		if strings.Contains(line, "--pool") || strings.Count(line, "--mp") != 1 ||
			!strings.Contains(line, `--mp0 local-lvm:1\,mp=/var/lib/plumb/key\,backup=0 `) {
			t.Errorf("LXC with a host mount, without the key volume, or in the pool: %s", line)
		}
	}
	if strings.Contains(out, "PLUMB_PVE_SECRET") || strings.Contains(out+errOut, "dry-run-secret") {
		t.Error("the dry run printed the token or the env file")
	}
}

// The rights are printed before any change and are exactly those of D-081.
func TestRights(t *testing.T) {
	h := newHost(t)
	_, out, _ := h.run(nil, "--dry-run", "--plumb-binary", h.plumb)
	rights := out[:strings.Index(out, "+ ")]
	want := map[string]string{
		"/pool/plumb":                   "VM.Allocate VM.Audit VM.Clone VM.Config.CPU VM.Config.Cloudinit VM.Config.Disk VM.Config.HWType VM.Config.Memory VM.Config.Network VM.Config.Options VM.PowerMgmt Pool.Audit",
		"/storage/local-lvm":            "Datastore.AllocateSpace Datastore.Audit",
		"/storage/plumb-images":         "Datastore.AllocateTemplate Datastore.Audit",
		"/storage/plumb-snippets":       "Datastore.AllocateSpace",
		"/sdn/zones/localnetwork/vmbr0": "SDN.Use",
		"/nodes/pve1":                   "Sys.AccessNetwork",
	}
	for path, privs := range want {
		re := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(path) + ` +Plumb\w+ +` + regexp.QuoteMeta(privs) + `$`)
		if !re.MatchString(rights) {
			t.Errorf("rights of %s: want %q in\n%s", path, privs, rights)
		}
	}
	// Only the rights lines grant something; the sentence after them names
	// what is not granted.
	var granted []string
	for _, line := range strings.Split(rights, "\n") {
		if strings.HasPrefix(line, "  /") {
			granted = append(granted, line)
		}
	}
	if len(granted) != len(want) {
		t.Errorf("%d rights lines, want %d", len(granted), len(want))
	}
	all := strings.Join(granted, "\n")
	for _, bad := range []string{"Sys.Audit", "VM.Config.CDROM", "Sys.Modify", "Permissions.Modify", "Pool.Allocate", "Sys.Console", "VM.Console", "VM.Monitor", "VM.GuestAgent", "Realm.", "User.Modify", "VM.Backup", "VM.Snapshot", "VM.Migrate", "Datastore.Allocate "} {
		if strings.Contains(all+" ", bad) {
			t.Errorf("forbidden right %s granted", bad)
		}
	}
}

func TestOldProxmoxIsRefused(t *testing.T) {
	for _, v := range []string{"9.0.11", "8.4.1", "7.4-3"} {
		h := newHost(t)
		code, _, errOut := h.run([]string{"FAKE_PVE_VERSION=" + v}, "--dry-run", "--plumb-binary", h.plumb)
		if code == 0 || !strings.Contains(errOut, "needs Proxmox VE 9.1 or newer") || !strings.Contains(errOut, "Nothing was changed") {
			t.Errorf("PVE %s: exit %d %q", v, code, errOut)
		}
		if calls := h.calls(); len(calls) != 1 || !strings.HasPrefix(calls[0], "pveversion") {
			t.Errorf("PVE %s: calls %v", v, calls)
		}
	}
}

func TestNeedsRootAndTerminal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as root")
	}
	h := newHost(t)
	if code, _, errOut := h.run(nil, "--plumb-binary", h.plumb); code == 0 || !strings.Contains(errOut, "run as root") {
		t.Errorf("not root: %d %q", code, errOut)
	}
	// As "root" but without a terminal and without --yes: no change.
	code, _, errOut := h.run([]string{"PLUMB_INSTALL_ASSUME_ROOT=1"}, "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "no terminal") {
		t.Errorf("no terminal: %d %q", code, errOut)
	}
	for _, c := range h.calls() {
		if mutating.MatchString(c) {
			t.Errorf("changed without a confirmation: %s", c)
		}
	}
}

// A real run (against the fakes) keeps the token out of every output,
// writes it only to the env file in the LXC (0600), and a second run
// changes nothing that exists.
func TestInstallAndRunAgain(t *testing.T) {
	h := newHost(t)
	root := []string{"PLUMB_INSTALL_ASSUME_ROOT=1"}
	code, out, errOut := h.run(root, "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("install: %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out+errOut, fakeSecret) {
		t.Fatal("the token secret was printed")
	}
	env, err := os.ReadFile(filepath.Join(h.state, "push", "_etc_plumb_plumb.env"))
	if err != nil || !strings.Contains(string(env), "PLUMB_PVE_SECRET="+fakeSecret) || !strings.Contains(string(env), "PLUMB_PVE_TOKEN_ID=plumb@pve!plumb") {
		t.Fatalf("env file: %s %v", env, err)
	}
	if opts, _ := os.ReadFile(filepath.Join(h.state, "push", "_etc_plumb_plumb.env.opts")); !strings.Contains(string(opts), "--perms 0600") {
		t.Errorf("env file mode: %s", opts)
	}
	if !strings.Contains(out, "https://192.168.1.50:8443") || !strings.Contains(out, "password pw-from-journal") {
		t.Errorf("end of install: %s", out)
	}
	snippet, err := os.ReadFile(filepath.Join(h.snippet, "snippets", "plumb-vendor.yaml"))
	if err != nil || !strings.Contains(string(snippet), "https://192.168.1.50:8444") || strings.Contains(string(snippet), fakeSecret) {
		t.Fatalf("snippet: %v\n%s", err, snippet)
	}
	if st, _ := os.ReadFile(h.stateF); !strings.Contains(string(st), "lxc_id=105") || strings.Contains(string(st), fakeSecret) {
		t.Errorf("state file: %s", st)
	}

	// Second run: nothing is created again, the token is kept.
	os.Remove(filepath.Join(h.state, "calls"))
	code, out, errOut = h.run(root, "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("second run: %d\n%s\n%s", code, out, errOut)
	}
	for _, c := range h.calls() {
		for _, again := range []string{"pveum pool add", "pveum user add", "pveum role add", "pveum user token add", "pvesm add", "pct create", "pveam download"} {
			if strings.HasPrefix(c, again) {
				t.Errorf("second run created again: %s", c)
			}
		}
	}
	if !strings.Contains(out, "is kept") {
		t.Errorf("second run output: %s", out)
	}
}

func TestRemove(t *testing.T) {
	h := newHost(t)
	root := []string{"PLUMB_INSTALL_ASSUME_ROOT=1"}
	if code, out, errOut := h.run(root, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("install: %s %s", out, errOut)
	}
	// Without a terminal and without --yes: nothing is removed.
	os.Remove(filepath.Join(h.state, "calls"))
	if code, _, errOut := h.run(root, "--remove"); code == 0 || !strings.Contains(errOut, "no terminal") {
		t.Errorf("remove without confirmation: %d %q", code, errOut)
	}
	for _, c := range h.calls() {
		if mutating.MatchString(c) {
			t.Errorf("removed without a confirmation: %s", c)
		}
	}
	// Guests in the pool: refused.
	if code, _, errOut := h.run(append(root, `FAKE_POOL_MEMBERS=[{"poolid":"plumb","members":[{"vmid":9001}]}]`), "--remove", "--yes"); code == 0 || !strings.Contains(errOut, "still has guests") {
		t.Errorf("remove with guests: %d %q", code, errOut)
	}
	code, out, errOut := h.run(root, "--remove", "--yes")
	if code != 0 {
		t.Fatalf("remove: %d %s %s", code, out, errOut)
	}
	left, _ := filepath.Glob(filepath.Join(h.state, "*.*"))
	if len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
	if _, err := os.Stat(h.snippet); !os.IsNotExist(err) {
		t.Errorf("snippet dir stays: %v", err)
	}
}

func snippetFile(t *testing.T, h *host, url string) (int, string, string) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\nMIIBplumbCAline1\nline2+/=\n-----END CERTIFICATE-----\n"), 0o644)
	return h.run(nil, "--dry-run", "--snippet-only", "--ca", ca, "--url", url)
}

// The snippet is valid cloud-config with valid agent.json, no secret, and
// an install command that is valid sh and idempotent.
func TestSnippet(t *testing.T) {
	h := newHost(t)
	code, out, errOut := snippetFile(t, h, "https://10.0.0.5:8444")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	start := strings.Index(out, "#cloud-config")
	end := strings.Index(out, "--- end of snippet ---")
	if start < 0 || end < 0 {
		t.Fatalf("no snippet in:\n%s", out)
	}
	text := out[start:end]
	var doc struct {
		WriteFiles []struct {
			Path, Owner, Permissions, Content string
		} `yaml:"write_files"`
		Runcmd        [][]string `yaml:"runcmd"`
		SSHDeleteKeys *bool      `yaml:"ssh_deletekeys"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("snippet is not YAML: %v\n%s", err, text)
	}
	// D-185: a new cloud-init instance keeps the SSH host keys.
	if doc.SSHDeleteKeys == nil || *doc.SSHDeleteKeys {
		t.Errorf("snippet without ssh_deletekeys: false")
	}
	files := map[string]string{}
	for _, f := range doc.WriteFiles {
		files[f.Path] = f.Content
		if f.Owner != "root:root" {
			t.Errorf("%s owner %s", f.Path, f.Owner)
		}
	}
	var cfg struct{ URL, CA string }
	if err := json.Unmarshal([]byte(files["/etc/plumb-agent/agent.json"]), &cfg); err != nil {
		t.Fatalf("agent.json: %v\n%s", err, files["/etc/plumb-agent/agent.json"])
	}
	if cfg.URL != "https://10.0.0.5:8444" || !strings.Contains(cfg.CA, "MIIBplumbCAline1\nline2+/=\n") || files["/etc/plumb-agent/ca.pem"] != cfg.CA {
		t.Errorf("agent.json = %+v", cfg)
	}
	script := files["/usr/local/sbin/plumb-agent-install"]
	if !strings.Contains(script, "is-active --quiet plumb-agent.service") || !strings.Contains(script, "--cacert") || strings.Contains(script, "-k ") || strings.Contains(script, "--insecure") {
		t.Errorf("install script:\n%s", script)
	}
	sh := exec.Command("sh", "-n")
	sh.Stdin = strings.NewReader(script)
	if out, err := sh.CombinedOutput(); err != nil {
		t.Errorf("install script is not valid sh: %v %s", err, out)
	}
	if len(doc.Runcmd) != 1 || doc.Runcmd[0][0] != "/usr/local/sbin/plumb-agent-install" {
		t.Errorf("runcmd = %v", doc.Runcmd)
	}
	if !strings.Contains(files["/etc/systemd/system/plumb-agent.service"], "ExecStart=/usr/local/bin/plumb-agent run") {
		t.Error("agent unit")
	}
	// No other command than the snippet storage and the file is printed.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "+ ") && !strings.HasPrefix(line, "+ install") && !strings.HasPrefix(line, "+ pvesm") {
			t.Errorf("--snippet-only does more: %s", line)
		}
	}
	// A URL that could break the YAML or the script is refused.
	for _, bad := range []string{"http://10.0.0.5:8444", "https://x'; rm -rf /; '", "https://a b", "https://x\n- evil", "https://x/path"} {
		h := newHost(t)
		if code, _, _ := snippetFile(t, h, bad); code == 0 {
			t.Errorf("url %q accepted", bad)
		}
	}
}

func TestBashSyntax(t *testing.T) {
	for _, f := range []string{"install.sh", "testdata/fakepve"} {
		if out, err := exec.Command("bash", "-n", f).CombinedOutput(); err != nil {
			t.Errorf("%s: %v %s", f, err, out)
		}
	}
}

// installed runs a real install against the fakes and returns the root env.
func (h *host) installed() []string {
	h.t.Helper()
	root := []string{"PLUMB_INSTALL_ASSUME_ROOT=1", "TMPDIR=" + h.tmpDir()}
	if code, out, errOut := h.run(root, "--yes", "--plumb-binary", h.plumb); code != 0 {
		h.t.Fatalf("install: %d\n%s\n%s", code, out, errOut)
	}
	os.Remove(filepath.Join(h.state, "calls"))
	return root
}

func (h *host) tmpDir() string {
	d := filepath.Join(filepath.Dir(h.state), "tmp")
	os.MkdirAll(d, 0o700)
	return d
}

func (h *host) changed() []string {
	var out []string
	for _, c := range h.calls() {
		if mutating.MatchString(c) {
			out = append(out, c)
		}
	}
	return out
}

// Review T-013 #1: --remove never deletes a directory from the command
// line, and keeps a directory that holds other files.
func TestRemoveSnippetDir(t *testing.T) {
	h := newHost(t)
	root := h.installed()
	code, out, errOut := h.run(root, "--remove", "--yes", "--dry-run", "--snippet-dir", "/etc")
	if code == 0 || !strings.Contains(errOut, "is not the directory of the install") || strings.Contains(out, "rm -rf") {
		t.Errorf("--snippet-dir /etc: %d %q %q", code, out, errOut)
	}
	if c := h.changed(); len(c) != 0 {
		t.Errorf("changed: %v", c)
	}
	other := filepath.Join(h.snippet, "snippets", "someone-else.yaml")
	os.WriteFile(other, []byte("x"), 0o644)
	cmd := exec.Command("bash", "install.sh", "--remove", "--yes") // no --snippet-dir: from the state file
	cmd.Env = append(append([]string{}, h.env...), root...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("remove: %v %s", err, out)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a file the script did not make was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.snippet, "snippets", "plumb-vendor.yaml")); !os.IsNotExist(err) {
		t.Errorf("the vendor snippet stays: %v", err)
	}
	for _, c := range h.calls() {
		if strings.Contains(c, "rm -rf") {
			t.Errorf("rm -rf: %s", c)
		}
	}
}

// Review T-013 #2: a container that this script did not make is neither
// destroyed nor given the token.
func TestForeignLXC(t *testing.T) {
	h := newHost(t)
	root := h.installed()
	// Someone replaced container 105 (stale state file, id reused).
	os.WriteFile(filepath.Join(h.state, "lxc.105"), []byte("hostname: db\ndescription: production database\n"), 0o644)
	if code, _, errOut := h.run(root, "--remove", "--yes"); code == 0 || !strings.Contains(errOut, "not the Plumb LXC") {
		t.Errorf("remove: %d %q", code, errOut)
	}
	if code, _, errOut := h.run(root, "--yes", "--plumb-binary", h.plumb); code == 0 || !strings.Contains(errOut, "not made by this script") {
		t.Errorf("install: %d %q", code, errOut)
	}
	for _, c := range h.changed() {
		if strings.HasPrefix(c, "pct ") {
			t.Errorf("the foreign container was touched: %s", c)
		}
	}
	// --lxc-id of a foreign container on a fresh host.
	h2 := newHost(t)
	os.WriteFile(filepath.Join(h2.state, "lxc.200"), []byte("hostname: web\ndescription: \n"), 0o644)
	code, _, errOut := h2.run([]string{"PLUMB_INSTALL_ASSUME_ROOT=1"}, "--yes", "--plumb-binary", h2.plumb, "--lxc-id", "200")
	if code == 0 || !strings.Contains(errOut, "not made by this script") {
		t.Errorf("--lxc-id foreign: %d %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(h2.state, "push", "_etc_plumb_plumb.env")); err == nil {
		t.Error("the token went into a foreign container")
	}
}

// Review T-013 #3: the pool guard fails closed.
func TestRemovePoolGuardFailsClosed(t *testing.T) {
	h := newHost(t)
	root := h.installed()
	if code, _, errOut := h.run(append(root, "FAKE_POOLS_FAIL=1"), "--remove", "--yes"); code == 0 || !strings.Contains(errOut, "cannot read the members") {
		t.Errorf("unreadable pool: %d %q", code, errOut)
	}
	if code, _, errOut := h.run(append(root, `FAKE_POOL_MEMBERS=[{"poolid":"other"}]`), "--remove", "--yes"); code == 0 || !strings.Contains(errOut, "cannot read the members") {
		t.Errorf("answer for another pool: %d %q", code, errOut)
	}
	if c := h.changed(); len(c) != 0 {
		t.Errorf("changed: %v", c)
	}
}

// Review T-013 #4 and host item 6: old rights are printed and removed;
// a role with extra rights stops the install.
func TestOldRights(t *testing.T) {
	h := newHost(t)
	env := []string{"PLUMB_INSTALL_ASSUME_ROOT=1", "FAKE_EXTRA_ACL=/ plumb@pve Plumb user"}
	code, out, errOut := h.run(env, "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if !strings.Contains(out, "older rights of Plumb are not in the plan") || !strings.Contains(out, "  / plumb@pve Plumb user") {
		t.Errorf("stray ACL not printed:\n%s", out)
	}
	if strings.Index(out, "older rights") > strings.Index(out, "+ ") && strings.Contains(out, "+ ") {
		t.Error("printed after a change")
	}
	acl, _ := os.ReadFile(filepath.Join(h.state, "acl"))
	if strings.Contains(string(acl), "/ plumb@pve Plumb") {
		t.Errorf("the old right stays:\n%s", acl)
	}
	// A role with an extra right is modified; a PVE that appends instead of
	// replacing leaves the extra right, and the install stops.
	os.WriteFile(filepath.Join(h.state, "role.PlumbNode"), []byte("Sys.AccessNetwork,Sys.Modify\n"), 0o644)
	code, _, errOut = h.run([]string{"PLUMB_INSTALL_ASSUME_ROOT=1", "FAKE_ROLE_APPEND=1"}, "--yes", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "has the rights") {
		t.Errorf("appended role: %d %q", code, errOut)
	}
}

// Review T-013 #6: a lost LXC gets a new token, not an LXC without one.
func TestLostLXCGetsNewToken(t *testing.T) {
	h := newHost(t)
	root := h.installed()
	os.Remove(filepath.Join(h.state, "lxc.105"))
	os.RemoveAll(filepath.Join(h.state, "push"))
	code, out, errOut := h.run(root, "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if env, err := os.ReadFile(filepath.Join(h.state, "push", "_etc_plumb_plumb.env")); err != nil || !strings.Contains(string(env), fakeSecret) {
		t.Errorf("new LXC without the token: %v", err)
	}
	var created bool
	for _, c := range h.calls() {
		if strings.HasPrefix(c, "pveum user token add") {
			created = true
		}
	}
	if !created {
		t.Error("no new token")
	}
}

// Review T-013 #7, #9, #10, #13.
func TestInstallerMinors(t *testing.T) {
	h := newHost(t)
	h.installed()
	if left, _ := os.ReadDir(h.tmpDir()); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
	for _, bad := range []string{"\"", "\\", "x\" , \"evil\": \"1"} {
		ca := filepath.Join(t.TempDir(), "ca.pem")
		os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\nMIIB"+bad+"\n-----END CERTIFICATE-----\n"), 0o644)
		if code, _, errOut := h.run(nil, "--dry-run", "--snippet-only", "--ca", ca, "--url", "https://10.0.0.5:8444"); code == 0 || !strings.Contains(errOut, "more than PEM") {
			t.Errorf("CA with %q: %d %q", bad, code, errOut)
		}
	}
	_, out, _ := snippetFile(t, newHost(t), "https://10.0.0.5:8444")
	if !strings.Contains(out, "--connect-timeout 10 --max-time 300") || !strings.Contains(out, "--timeout=60") {
		t.Error("downloads without timeouts")
	}
	// D-081: the installer never changes a storage that it did not make.
	_, out, _ = h.run([]string{"PLUMB_INSTALL_ASSUME_ROOT=1"}, "--remove", "--yes")
	for _, c := range h.calls() {
		if strings.HasPrefix(c, "pvesm set") || strings.Contains(c, "pvesm remove local") {
			t.Errorf("a foreign storage was changed: %s", c)
		}
	}
	if !strings.Contains(out, "Plumb is removed.") {
		t.Errorf("remove: %s", out)
	}
}

// D-081: package floor and storage rules, all refused before any change.
func TestStorageAndVersionRules(t *testing.T) {
	refused := func(name string, env []string, setup func(h *host), want string, args ...string) {
		t.Helper()
		h := newHost(t)
		if setup != nil {
			setup(h)
		}
		code, _, errOut := h.run(env, append([]string{"--dry-run", "--plumb-binary", h.plumb}, args...)...)
		if code == 0 || !strings.Contains(errOut, want) || !strings.Contains(errOut, "Nothing was changed") {
			t.Errorf("%s: %d %q", name, code, errOut)
		}
		if c := h.changed(); len(c) != 0 {
			t.Errorf("%s: changed %v", name, c)
		}
	}
	content := func(id, c string) func(h *host) {
		return func(h *host) { os.WriteFile(filepath.Join(h.state, "content."+id), []byte(c), 0o644) }
	}
	refused("old qemu-server", []string{"FAKE_QEMU_SERVER=9.2.10"}, nil, "qemu-server 9.2.11 or newer")
	refused("old storage perl", []string{"FAKE_STORAGE_PERL=9.1.12-1"}, nil, "libpve-storage-perl 9.1.13 or newer")
	refused("disk storage with snippets", nil, content("local-lvm", "images,rootdir,snippets"), `has the content "snippets"`)
	refused("image storage local", nil, nil, `must have exactly "import"`, "--image-storage", "local")
	refused("image storage import+iso", nil, content("imgs", "import,iso"), `must have exactly "import"`, "--image-storage", "imgs")
	refused("plumb-snippets with more content", nil, content("plumb-snippets", "snippets,images"), `not "snippets"`)

	// An existing storage with exactly "import" is used as it is.
	h := newHost(t)
	os.WriteFile(filepath.Join(h.state, "content.imgs"), []byte("import"), 0o644)
	code, out, errOut := h.run(nil, "--dry-run", "--plumb-binary", h.plumb, "--image-storage", "imgs")
	if code != 0 || strings.Contains(out, "plumb-images") || !strings.Contains(out, "/storage/imgs") {
		t.Errorf("import-only storage: %d %s %s", code, out, errOut)
	}
	// Newer packages pass.
	h = newHost(t)
	if code, _, errOut := h.run([]string{"FAKE_QEMU_SERVER=9.2.11", "FAKE_STORAGE_PERL=9.1.13"}, "--dry-run", "--plumb-binary", h.plumb); code != 0 {
		t.Errorf("minimum versions: %d %q", code, errOut)
	}
}

// Review T-013 n1, n2, n3.
func TestReReviewMinors(t *testing.T) {
	// n1: an unreadable ACL list stops the install before any change.
	h := newHost(t)
	code, _, errOut := h.run([]string{"FAKE_ACL_FAIL=1"}, "--dry-run", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "cannot read the ACLs") {
		t.Errorf("acl list fails: %d %q", code, errOut)
	}
	if c := h.changed(); len(c) != 0 {
		t.Errorf("changed: %v", c)
	}
	// n2: an existing plumb@pve in a group is refused.
	h = newHost(t)
	os.WriteFile(filepath.Join(h.state, "user.plumb@pve"), nil, 0o644)
	code, _, errOut = h.run([]string{"FAKE_USER_GROUPS=admins"}, "--dry-run", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, `in the groups "admins"`) {
		t.Errorf("user in a group: %d %q", code, errOut)
	}
	// n3: a plumb-images storage that existed before stays at --remove.
	h = newHost(t)
	// Like a real dir storage, but in another directory: not adopted.
	other := filepath.Join(t.TempDir(), "other-images")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(h.state, "storage.plumb-images"), nil, 0o644)
	os.WriteFile(filepath.Join(h.state, "type.plumb-images"), []byte("dir\n"), 0o644)
	os.WriteFile(filepath.Join(h.state, "path.plumb-images"), []byte(other+"\n"), 0o644)
	os.WriteFile(filepath.Join(h.state, "content.plumb-images"), []byte("import"), 0o644)
	root := h.installed()
	if st, _ := os.ReadFile(h.stateF); !strings.Contains(string(st), "image_dir=\n") {
		t.Errorf("state file claims plumb-images: %s", st)
	}
	if code, out, errOut := h.run(root, "--remove", "--yes"); code != 0 {
		t.Fatalf("remove: %s %s", out, errOut)
	}
	for _, c := range h.calls() {
		if strings.HasPrefix(c, "pvesm remove plumb-images") {
			t.Errorf("removed a storage the script did not make: %s", c)
		}
	}
	// A storage that the script made is removed.
	h = newHost(t)
	root = h.installed()
	h.run(root, "--remove", "--yes")
	found := false
	for _, c := range h.calls() {
		found = found || strings.HasPrefix(c, "pvesm remove plumb-images")
	}
	if !found {
		t.Error("plumb-images made by the script was not removed")
	}
}
