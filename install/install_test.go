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
var fakeNames = []string{"pveversion", "pveum", "pvesm", "pvesh", "pveam", "pct", "hostname", "ip", "install"}

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
	os.WriteFile(h.pveCA, []byte("-----BEGIN CERTIFICATE-----\nMIIBpveroot\n-----END CERTIFICATE-----\n"), 0o644)
	h.env = append(os.Environ(),
		"PATH="+h.bin+":"+os.Getenv("PATH"),
		"FAKE_STATE="+h.state,
		"FAKE_SECRET="+fakeSecret,
		"PLUMB_INSTALL_PVE_CA="+h.pveCA,
		"PLUMB_INSTALL_STATE="+h.stateF,
	)
	return h
}

// run runs install.sh with stdin closed (no terminal).
func (h *host) run(env []string, args ...string) (int, string, string) {
	h.t.Helper()
	cmd := exec.Command("bash", append([]string{"install.sh", "--snippet-dir", h.snippet}, args...)...)
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

func (h *host) calls() []string {
	b, _ := os.ReadFile(filepath.Join(h.state, "calls"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// mutating reports a fake call that changes the host.
var mutating = regexp.MustCompile(`^(pveum (pool (add|delete)|role (add|modify|delete)|user (add|delete)|acl|user token (add|remove))|` +
	`pvesm (add|set|remove)|pveam (update|download)|pct (create|start|stop|destroy|push|pull|exec [0-9]+ -- (install|sh|chown|systemctl))|install )`)

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
		"+ pvesm set local --content iso\\,vztmpl\\,backup\\,import",
		"+ pveam download local debian-13-standard_13.1-2_amd64.tar.zst",
		"+ pct create 105 local:vztmpl/debian-13-standard_13.1-2_amd64.tar.zst --hostname plumb --unprivileged 1",
		"+ pct push 105 " + h.plumb + " /usr/local/bin/plumb --perms 0755",
		"/etc/plumb/plumb.env --perms 0600",
		"+ pct exec 105 -- systemctl enable --now plumb.service",
		"--- snippet " + h.snippet + "/snippets/plumb-vendor.yaml ---",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run does not print %q", want)
		}
	}
	// The LXC has no host mount and is not in the Plumb pool (§7).
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "+ pct create") && (strings.Contains(line, "--mp") || strings.Contains(line, "--pool")) {
			t.Errorf("LXC with a mount or in the pool: %s", line)
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
		"/pool/plumb":                   "VM.Allocate VM.Audit VM.Clone VM.Config.CDROM VM.Config.CPU VM.Config.Cloudinit VM.Config.Disk VM.Config.HWType VM.Config.Memory VM.Config.Network VM.Config.Options VM.PowerMgmt Pool.Audit",
		"/storage/local-lvm":            "Datastore.AllocateSpace Datastore.Audit",
		"/storage/local":                "Datastore.AllocateTemplate Datastore.Audit",
		"/storage/plumb-snippets":       "Datastore.Audit",
		"/sdn/zones/localnetwork/vmbr0": "SDN.Use",
		"/nodes/pve1":                   "Sys.Audit Sys.AccessNetwork",
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
	for _, bad := range []string{"Sys.Modify", "Permissions.Modify", "Pool.Allocate", "Sys.Console", "VM.Console", "VM.Monitor", "VM.GuestAgent", "Realm.", "User.Modify", "VM.Backup", "VM.Snapshot", "VM.Migrate", "Datastore.Allocate "} {
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
		Runcmd [][]string `yaml:"runcmd"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("snippet is not YAML: %v\n%s", err, text)
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
