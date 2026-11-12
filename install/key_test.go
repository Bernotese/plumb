// Tests of the key volume, plumb key init and the LXC address (T-052,
// D-177 … D-179) against the fake Proxmox commands.
package install

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var asRoot = []string{"PLUMB_INSTALL_ASSUME_ROOT=1"}

const keyInitCall = "lxc-attach -n 105 --clear-env -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb key init --data /var/lib/plumb"

// resetCalls forgets the calls of earlier runs.
func (h *host) resetCalls() { os.Remove(filepath.Join(h.state, "calls")) }

// index returns the position of the first call with the prefix, or -1.
func index(calls []string, prefix string) int {
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func TestKeyVolumeAndKeyInit(t *testing.T) {
	h := newHost(t)
	code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("install: %d\n%s\n%s", code, out, errOut)
	}
	calls := h.calls()
	create := calls[index(calls, "pct create")]
	if !strings.Contains(create, " --mp0 local-lvm:1,mp=/var/lib/plumb/key,backup=0 ") {
		t.Errorf("pct create without the key volume: %s", create)
	}
	for _, want := range []string{"pct exec 105 -- chown plumb:plumb /var/lib/plumb/key", "pct exec 105 -- chmod 0700 /var/lib/plumb/key"} {
		if index(calls, want) < 0 {
			t.Errorf("missing call %q", want)
		}
	}
	// plumb key init runs through lxc-attach as user plumb, after the unit
	// is enabled and before plumb.service starts.
	enable, keyInit := index(calls, "pct exec 105 -- systemctl enable plumb.service"), index(calls, keyInitCall)
	start := index(calls, "pct exec 105 -- systemctl restart plumb.service")
	if keyInit < 0 || enable < 0 || start < 0 || !(enable < keyInit && keyInit < start) {
		t.Errorf("order enable %d, key init %d, start %d\n%s", enable, keyInit, start, strings.Join(calls, "\n"))
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "pct exec") && strings.Contains(c, "key init") {
			t.Errorf("key init through pct exec (no terminal): %s", c)
		}
		if strings.Contains(c, "systemctl start") || strings.Contains(c, "enable --now") {
			t.Errorf("plumb.service may start before key init: %s", c)
		}
	}
	// The recovery key reaches the terminal once and nothing else.
	if n := strings.Count(out+errOut, "FAKERECOVERYKEY"); n != 1 {
		t.Errorf("recovery key printed %d times", n)
	}
	filepath.WalkDir(filepath.Dir(h.state), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && p == h.bin {
			return filepath.SkipDir // the fake commands themselves
		}
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "FAKERECOVERYKEY") {
				t.Errorf("recovery key stored in %s", p)
			}
		}
		return nil
	})

	// A repair run with a key skips key init and needs no terminal.
	h.resetCalls()
	code, out, errOut = h.run(append(asRoot, "PLUMB_INSTALL_ASSUME_TTY=0"), "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("repair: %d\n%s\n%s", code, out, errOut)
	}
	if index(h.calls(), "lxc-attach") >= 0 {
		t.Error("repair run with a key ran key init again")
	}
	if !strings.Contains(out, "The master key exists in LXC 105; it is kept.") {
		t.Errorf("repair: %s", out)
	}
}

func TestKeyInitNeedsTerminal(t *testing.T) {
	h := newHost(t)
	noTTY := append(asRoot, "PLUMB_INSTALL_ASSUME_TTY=0")
	code, _, errOut := h.run(noTTY, "--yes", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "only on a terminal") || !strings.Contains(errOut, "Nothing was changed") {
		t.Errorf("no terminal: %d %q", code, errOut)
	}
	for _, c := range h.calls() {
		if mutating.MatchString(c) {
			t.Errorf("changed the host without a terminal: %s", c)
		}
	}
	// --dry-run needs no terminal and only prints the step.
	h.resetCalls()
	code, out, errOut := h.run([]string{"PLUMB_INSTALL_ASSUME_TTY=0"}, "--dry-run", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("dry run: %d %s %s", code, out, errOut)
	}
	if !strings.Contains(out, `+ lxc-attach -n 105 --clear-env -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb key init --data /var/lib/plumb`) {
		t.Errorf("dry run does not print key init:\n%s", out)
	}
	if index(h.calls(), "lxc-attach") >= 0 {
		t.Error("dry run ran key init")
	}
	if !strings.Contains(out, "RECOVERY KEY once") {
		t.Errorf("dry run does not explain the recovery key:\n%s", out)
	}
}

func TestPreflightPctExecDoesNotConsumeConfirmation(t *testing.T) {
	h := newHost(t)
	config := "hostname: plumb\ndescription: plumb-installer-lxc Plumb controller\nmp0: local-lvm:vm-105-disk-1,mp=/var/lib/plumb/key,backup=0\n"
	if err := os.WriteFile(filepath.Join(h.state, "lxc.105"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.state, "key.105"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	code, output, errors := h.runTTY(t, "y\n", "--lxc-id", "105", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("install did not read the pretyped confirmation: %d\n%s\n%s", code, output, errors)
	}
	if !strings.Contains(output, "Continue? [type y]") {
		t.Errorf("confirmation prompt missing: %s", output)
	}
	code, output, errors = h.run([]string{"PLUMB_INSTALL_ASSUME_ROOT=1"}, "--dry-run", "--lxc-id", "105", "--plumb-binary", h.plumb)
	if code != 0 || strings.Contains(output, "lxc-attach") || strings.Contains(output, "plumb key init") {
		t.Errorf("dry run printed key init for an existing key: %d\n%s\n%s", code, output, errors)
	}
}

func TestKeyInitWaitsForRecoveryKeyAcknowledgement(t *testing.T) {
	h := newHost(t)
	code, output, errors := h.runTTY(t, "y\n\n", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("install did not finish after key acknowledgement: %d\n%s\n%s", code, output, errors)
	}
	key, prompt := strings.Index(output, "FAKERECOVERYKEY"), strings.Index(output, "Write the recovery key down, then press Enter")
	if key < 0 || prompt < key {
		t.Errorf("recovery key acknowledgement did not follow key init:\n%s", output)
	}
}

func TestKeyInitEOFStillRestartsService(t *testing.T) {
	h := newHost(t)
	code, output, errors := h.runTTY(t, "y\n\x04", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("install stopped at EOF after key init: %d\n%s\n%s", code, output, errors)
	}
	if index(h.calls(), "pct exec 105 -- systemctl restart plumb.service") < 0 {
		t.Errorf("plumb.service did not restart after EOF at key acknowledgement: %v", h.calls())
	}
	if strings.Contains(output, "then press Enter ") {
		t.Errorf("recovery key prompt has a trailing space: %s", output)
	}
}

func TestKeyInitFailureStopsBeforeStart(t *testing.T) {
	h := newHost(t)
	code, _, errOut := h.run(append(asRoot, "FAKE_KEYINIT_FAIL=1"), "--yes", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "plumb key init failed") {
		t.Errorf("failed key init: %d %q", code, errOut)
	}
	if index(h.calls(), "pct exec 105 -- systemctl restart plumb.service") >= 0 {
		t.Error("plumb.service started without a key")
	}
}

// An LXC of an older install.sh has no key volume: refused before any change.
func TestLXCWithoutKeyVolume(t *testing.T) {
	for name, mp := range map[string]string{
		"none":      "",
		"in backup": "mp0: local-lvm:vm-105-disk-1,mp=/var/lib/plumb/key\n",
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			os.WriteFile(filepath.Join(h.state, "lxc.105"), []byte("hostname: plumb\ndescription: plumb-installer-lxc Plumb controller\n"+mp), 0o644)
			code, _, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb, "--lxc-id", "105")
			want := "has no key volume"
			if mp != "" {
				want = "no backup=0"
			}
			if code == 0 || !strings.Contains(errOut, want) || !strings.Contains(errOut, "Nothing was changed") {
				t.Errorf("%d %q", code, errOut)
			}
			for _, c := range h.calls() {
				if mutating.MatchString(c) {
					t.Errorf("changed the host: %s", c)
				}
			}
		})
	}
}

func TestRemoveKeyVolume(t *testing.T) {
	for _, keep := range []bool{false, true} {
		h := newHost(t)
		env := asRoot
		if keep {
			env = append(env, "FAKE_KEEP_VOLUME=1")
		}
		if code, out, errOut := h.run(env, "--yes", "--plumb-binary", h.plumb); code != 0 {
			t.Fatalf("install: %s %s", out, errOut)
		}
		h.resetCalls()
		code, out, errOut := h.run(env, "--remove", "--yes")
		if code != 0 {
			t.Fatalf("remove: %d %s %s", code, out, errOut)
		}
		for _, want := range []string{"its key volume local-lvm:vm-105-disk-1 (the master key)", "only with the recovery key"} {
			if !strings.Contains(out, want) {
				t.Errorf("remove confirmation without %q:\n%s", want, out)
			}
		}
		free := index(h.calls(), "pvesm free local-lvm:vm-105-disk-1")
		if keep != (free >= 0) {
			t.Errorf("volume left %v, pvesm free at %d", keep, free)
		}
		if _, err := os.Stat(filepath.Join(h.state, "vol.105")); err == nil {
			t.Error("the key volume is still there")
		}
	}
}

func TestLXCAddress(t *testing.T) {
	h := newHost(t)
	_, out, _ := h.run(nil, "--dry-run", "--plumb-binary", h.plumb)
	if !strings.Contains(out, "WARNING: the LXC gets its address by DHCP") {
		t.Errorf("no DHCP warning:\n%s", out)
	}
	code, out, errOut := h.run(nil, "--dry-run", "--plumb-binary", h.plumb, "--lxc-ip", "192.168.1.60/24", "--lxc-gw", "192.168.1.1")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if strings.Contains(out, "DHCP") {
		t.Error("DHCP warning with a static address")
	}
	for _, want := range []string{`--net0 name=eth0\,bridge=vmbr0\,ip=192.168.1.60/24\,gw=192.168.1.1 `, `"url": "https://192.168.1.60:8444"`} {
		if !strings.Contains(out, want) {
			t.Errorf("static address: missing %q", want)
		}
	}
}

// Proxmox refuses an ACL for a token that does not exist yet (test host,
// T-013 step 3): the token is made before any ACL.
func TestTokenBeforeACLs(t *testing.T) {
	h := newHost(t)
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("install: %d %s %s", code, out, errOut)
	}
	calls := h.calls()
	token, acl := index(calls, "pveum user token add"), index(calls, "pveum acl modify")
	if token < 0 || acl < 0 || token > acl {
		t.Errorf("token add at %d, first acl modify at %d", token, acl)
	}
}

// seedHalfInstalled makes the state of the test host after the first real
// run stopped at the token ACL (T-013 step 3b): plumb-images, pool, roles,
// user and one user ACL, but no token, no LXC and no state file.
func seedHalfInstalled(t *testing.T, h *host, imagesPath string) {
	t.Helper()
	files := map[string]string{
		"storage.plumb-images": "",
		"type.plumb-images":    "dir\n",
		"content.plumb-images": "import\n",
		"path.plumb-images":    imagesPath + "\n",
		"pool.plumb":           "",
		"user.plumb@pve":       "",
		"acl":                  "/pool/plumb plumb@pve PlumbGuests user 1\n",
	}
	for name, privs := range map[string]string{
		"PlumbGuests":   "VM.Allocate,VM.Audit,VM.Clone,VM.Config.CPU,VM.Config.Cloudinit,VM.Config.Disk,VM.Config.HWType,VM.Config.Memory,VM.Config.Network,VM.Config.Options,VM.PowerMgmt,Pool.Audit",
		"PlumbDisk":     "Datastore.AllocateSpace,Datastore.Audit",
		"PlumbImages":   "Datastore.AllocateTemplate,Datastore.Audit",
		"PlumbSnippets": "Datastore.AllocateSpace",
		"PlumbBridge":   "SDN.Use",
		"PlumbNode":     "Sys.AccessNetwork",
		"PlumbAudit":    "Sys.Audit",
	} {
		files["role."+name] = privs + "\n"
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(h.state, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(h.images(), "import"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The dry run on the half-installed host lists only what is missing, and
// the real run repairs it and adopts plumb-images, so --remove removes it.
func TestRepairHalfInstalledHost(t *testing.T) {
	h := newHost(t)
	seedHalfInstalled(t, h, h.images())
	code, out, errOut := h.run(nil, "--dry-run", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("dry run: %d %s %s", code, out, errOut)
	}
	for _, c := range h.calls() {
		if mutating.MatchString(c) {
			t.Errorf("dry run changed the host: %s", c)
		}
	}
	for _, gone := range []string{"+ pvesm add dir plumb-images", "+ install -d -o root -g root -m 0755 " + h.images(),
		"+ pveum pool add", "+ pveum role", "+ pveum user add", "+ pveum acl modify /pool/plumb --users"} {
		if strings.Contains(out, gone) {
			t.Errorf("dry run lists %q, which exists", gone)
		}
	}
	for _, want := range []string{"exists without a state entry; it is exactly what install.sh makes, so it is adopted",
		"+ pveum user token add plumb@pve plumb", `+ pveum acl modify /pool/plumb --tokens plumb@pve\!plumb`,
		"+ pveum acl modify /storage/local-lvm --users plumb@pve", "+ pvesm add dir plumb-snippets", "+ pct create 105"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run does not list %q", want)
		}
	}
	// Three writes: after plumb-images, after plumb-snippets (snippet_dir,
	// review S2) and after the LXC (its id). The last state_put has
	// nothing new and writes nothing.
	if n := strings.Count(out, "/install.state"); n != 3 {
		t.Errorf("dry run writes the state file %d times, want 3", n)
	}

	code, out, errOut = h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("repair: %d %s %s", code, out, errOut)
	}
	st, _ := os.ReadFile(h.stateF)
	if !strings.Contains(string(st), "image_dir="+h.images()+"\n") || !strings.Contains(string(st), "lxc_id=105\n") {
		t.Errorf("state after the repair:\n%s", st)
	}
	h.resetCalls()
	if code, out, errOut := h.run(asRoot, "--remove", "--yes"); code != 0 {
		t.Fatalf("remove: %d %s %s", code, out, errOut)
	}
	if index(h.calls(), "pvesm remove plumb-images") < 0 {
		t.Error("--remove kept the adopted plumb-images")
	}
}

// A plumb-images that differs from what install.sh makes is used, not
// adopted: --remove keeps it.
func TestForeignImagesNotAdopted(t *testing.T) {
	h := newHost(t)
	other := filepath.Join(t.TempDir(), "someone-elses-images")
	os.MkdirAll(other, 0o755)
	seedHalfInstalled(t, h, other)
	code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("install: %d %s %s", code, out, errOut)
	}
	if !strings.Contains(out, "not as install.sh makes it") {
		t.Errorf("no note on the foreign storage:\n%s", out)
	}
	h.resetCalls()
	h.run(asRoot, "--remove", "--yes")
	if index(h.calls(), "pvesm remove plumb-images") >= 0 {
		t.Error("--remove removed a storage that install.sh did not make")
	}
}

// A run that stops halfway has already written the state, so --remove
// removes what it made (T-013 step 3b, minor 5).
func TestStoppedRunLeavesState(t *testing.T) {
	h := newHost(t)
	code, _, _ := h.run(append(asRoot, "FAKE_FAIL=pveum user token add"), "--yes", "--plumb-binary", h.plumb)
	if code == 0 {
		t.Fatal("the run did not stop")
	}
	st, err := os.ReadFile(h.stateF)
	if err != nil || !strings.Contains(string(st), "image_dir="+h.images()+"\n") || !strings.Contains(string(st), "lxc_id=\n") {
		t.Fatalf("state after the stop: %v\n%s", err, st)
	}
	h.resetCalls()
	if code, out, errOut := h.run(asRoot, "--remove", "--yes"); code != 0 {
		t.Fatalf("remove: %d %s %s", code, out, errOut)
	}
	for _, want := range []string{"pvesm remove plumb-images", "pvesm remove plumb-snippets", "pveum user delete plumb@pve", "pveum pool delete plumb"} {
		if index(h.calls(), want) < 0 {
			t.Errorf("--remove after a stopped run did not run %q", want)
		}
	}
}

// Review S1: a failed pct start leaves an LXC that the state names; the
// next run starts it and makes no second LXC.
func TestFailedStartKeepsOneLXC(t *testing.T) {
	h := newHost(t)
	if code, _, _ := h.run(append(asRoot, "FAKE_FAIL=pct start"), "--yes", "--plumb-binary", h.plumb); code == 0 {
		t.Fatal("the run did not stop")
	}
	st, _ := os.ReadFile(h.stateF)
	if !strings.Contains(string(st), "lxc_id=105\n") {
		t.Fatalf("state after the failed start:\n%s", st)
	}
	h.resetCalls()
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("second run: %d %s %s", code, out, errOut)
	}
	calls := h.calls()
	if index(calls, "pct create") >= 0 {
		t.Error("the second run made another LXC")
	}
	if index(calls, "pct start 105") < 0 {
		t.Error("the kept, stopped LXC was not started")
	}
	// A running kept LXC is not started again.
	h.resetCalls()
	h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
	if index(h.calls(), "pct start") >= 0 {
		t.Error("a running LXC was started again")
	}
}

// A pct create that failed left an empty /var/lib/lxc/<id>/rootfs (0700)
// on the test host. The next run removes it; a directory with files stays
// and the run stops before pct create.
func TestStaleLXCDir(t *testing.T) {
	for _, withFile := range []bool{false, true} {
		h := newHost(t)
		lxcDir := filepath.Join(filepath.Dir(h.state), "lxc", "105")
		if err := os.MkdirAll(filepath.Join(lxcDir, "rootfs"), 0o700); err != nil {
			t.Fatal(err)
		}
		if withFile {
			os.WriteFile(filepath.Join(lxcDir, "rootfs", "data"), []byte("x"), 0o600)
		}
		code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
		_, err := os.Stat(lxcDir)
		if !withFile {
			if code != 0 || err == nil {
				t.Errorf("empty stale dir: %d, still there: %v\n%s %s", code, err == nil, out, errOut)
			}
			continue
		}
		if code == 0 || !strings.Contains(errOut, "is not empty") || err != nil {
			t.Errorf("stale dir with a file: %d %q, removed: %v", code, errOut, err != nil)
		}
		if index(h.calls(), "pct create") >= 0 {
			t.Error("pct create ran over a stale directory with files")
		}
	}
}

// Review S2: a run that stops before plumb-snippets is set up does not put
// snippet_dir into the state, so --remove never deletes a snippet that
// this script did not write.
func TestStateNamesSnippetDirOnlyAfterSetup(t *testing.T) {
	h := newHost(t)
	foreign := filepath.Join(h.snippet, "snippets", "plumb-vendor.yaml")
	os.MkdirAll(filepath.Dir(foreign), 0o755)
	os.WriteFile(foreign, []byte("someone else's snippet\n"), 0o644)
	if code, _, _ := h.run(append(asRoot, "FAKE_FAIL=pveum pool add"), "--yes", "--plumb-binary", h.plumb); code == 0 {
		t.Fatal("the run did not stop")
	}
	st, _ := os.ReadFile(h.stateF)
	if !strings.Contains(string(st), "snippet_dir=\n") {
		t.Errorf("state names the snippet dir before it was set up:\n%s", st)
	}
	if code, out, errOut := h.runRemove(); code != 0 {
		t.Fatalf("remove: %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("--remove deleted a snippet that install.sh did not write")
	}
}

// Review S3: the adopt decision is printed before the question; a storage
// with more options than install.sh sets is not adopted; an unsafe image
// directory stops the run before any change.
func TestImagesAdoptRules(t *testing.T) {
	h := newHost(t)
	seedHalfInstalled(t, h, h.images())
	_, out, _ := h.run(nil, "--dry-run", "--plumb-binary", h.plumb)
	adopt, ask := strings.Index(out, "so it is adopted"), strings.Index(out, "would ask: Continue?")
	if adopt < 0 || ask < 0 || adopt > ask {
		t.Errorf("adopt line at %d, question at %d", adopt, ask)
	}

	h = newHost(t)
	seedHalfInstalled(t, h, h.images())
	os.WriteFile(filepath.Join(h.state, "extra.plumb-images"), []byte(`"nodes":"pve2"`), 0o644)
	_, out, _ = h.run(nil, "--dry-run", "--plumb-binary", h.plumb)
	if !strings.Contains(out, "not as install.sh makes it") {
		t.Errorf("a storage with the option nodes was adopted:\n%s", out)
	}

	for name, env := range map[string][]string{
		"owner":    {"FAKE_DIR_OWNER=nobody"},
		"writable": nil,
		"symlink":  nil,
	} {
		h = newHost(t)
		seedHalfInstalled(t, h, h.images())
		switch name {
		case "writable":
			os.Chmod(h.images(), 0o777)
		case "symlink":
			real := filepath.Join(t.TempDir(), "real")
			os.MkdirAll(real, 0o755)
			os.RemoveAll(h.images())
			os.Symlink(real, h.images())
		}
		code, _, errOut := h.run(append(asRoot, env...), "--yes", "--plumb-binary", h.plumb)
		if code == 0 || !strings.Contains(errOut, "Nothing was changed") {
			t.Errorf("%s: %d %q", name, code, errOut)
		}
		for _, c := range h.calls() {
			if mutating.MatchString(c) {
				t.Errorf("%s: changed the host: %s", name, c)
			}
		}
	}
}

// Review S4: --remove without a state entry keeps plumb-images and says so.
func TestRemoveKeepsUnrecordedImages(t *testing.T) {
	h := newHost(t)
	seedHalfInstalled(t, h, h.images())
	code, out, errOut := h.runRemove()
	if code != 0 || !strings.Contains(out, "Kept storage plumb-images: the state file has no entry") {
		t.Errorf("remove: %d %s %s", code, out, errOut)
	}
	if index(h.calls(), "pvesm remove plumb-images") >= 0 {
		t.Error("removed plumb-images without a state entry")
	}
}

// D-183: plumb serve exits 78 for a start failure that a restart cannot
// fix; systemd must not restart it then.
func TestUnitStopsOnExit78(t *testing.T) {
	h := newHost(t)
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	unit, err := os.ReadFile(filepath.Join(h.state, "push", "_etc_systemd_system_plumb.service"))
	if err != nil || !strings.Contains(string(unit), "\nRestartPreventExitStatus=78\n") {
		t.Errorf("unit: %v\n%s", err, unit)
	}
}

// Review nits 1 and 2: an empty stale dir without rootfs is removed; a
// symbolic link stops the run.
func TestStaleLXCDirNits(t *testing.T) {
	h := newHost(t)
	dir := filepath.Join(filepath.Dir(h.state), "lxc", "105")
	os.MkdirAll(dir, 0o700)
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Errorf("empty dir without rootfs: %d %s %s", code, out, errOut)
	}
	h = newHost(t)
	dir = filepath.Join(filepath.Dir(h.state), "lxc")
	os.MkdirAll(dir, 0o755)
	os.Symlink(t.TempDir(), filepath.Join(dir, "105"))
	if code, _, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code == 0 || !strings.Contains(errOut, "symbolic link") {
		t.Errorf("symlinked dir: %d %q", code, errOut)
	}
}

// runRemove runs install.sh --remove --yes as root, without --snippet-dir:
// the directory comes from the state file, as for a person.
func (h *host) runRemove(env ...string) (int, string, string) {
	h.t.Helper()
	cmd := exec.Command("bash", "install.sh", "--remove", "--yes")
	cmd.Env = append(append(append([]string{}, h.env...), asRoot...), env...)
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

// D-187: plumb.env gets the DNS of new VMs from the host resolver.
func TestDNSFromHost(t *testing.T) {
	envOf := func(h *host) string {
		b, _ := os.ReadFile(filepath.Join(h.state, "push", "_etc_plumb_plumb.env"))
		return string(b)
	}
	resolv := func(h *host, s string) {
		os.WriteFile(filepath.Join(filepath.Dir(h.state), "resolv.conf"), []byte(s), 0o644)
	}
	h := newHost(t)
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if e := envOf(h); !strings.Contains(e, "\nPLUMB_PVE_NAMESERVER=192.0.2.53\n") || !strings.Contains(e, "\nPLUMB_PVE_SEARCHDOMAIN=plumb.test\n") {
		t.Errorf("env:\n%s", e)
	}

	// Loopback is left out; at most 3; the host domain when there is no search.
	h = newHost(t)
	resolv(h, "nameserver 127.0.0.53\nnameserver 192.0.2.1\nnameserver 192.0.2.2\nnameserver 2001:db8::3\nnameserver 192.0.2.4\n")
	h.run(append(asRoot, "FAKE_HOST_DOMAIN=lan.example"), "--yes", "--plumb-binary", h.plumb)
	if e := envOf(h); !strings.Contains(e, "\nPLUMB_PVE_NAMESERVER=192.0.2.1,192.0.2.2,2001:db8::3\n") || !strings.Contains(e, "\nPLUMB_PVE_SEARCHDOMAIN=lan.example\n") {
		t.Errorf("env:\n%s", e)
	}

	// Only a loopback resolver and no domain: stop before any change …
	h = newHost(t)
	resolv(h, "nameserver 127.0.0.53\nsearch .\n")
	code, _, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
	if code == 0 || !strings.Contains(errOut, "use --nameserver IP") {
		t.Errorf("loopback only: %d %q", code, errOut)
	}
	for _, c := range h.calls() {
		if mutating.MatchString(c) {
			t.Errorf("changed the host: %s", c)
		}
	}
	// … unless the options give the values; bad values are refused.
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb, "--nameserver", "192.0.2.9", "--searchdomain", "corp.example"); code != 0 {
		t.Fatalf("options: %d %s %s", code, out, errOut)
	}
	if e := envOf(h); !strings.Contains(e, "\nPLUMB_PVE_NAMESERVER=192.0.2.9\n") || !strings.Contains(e, "\nPLUMB_PVE_SEARCHDOMAIN=corp.example\n") {
		t.Errorf("env:\n%s", e)
	}
	for _, bad := range [][]string{{"--nameserver", "dns.example"}, {"--searchdomain", "a b"}, {"--nameserver", "192.0.2.1,192.0.2.2,192.0.2.3,192.0.2.4"}} {
		h = newHost(t)
		if code, _, _ := h.run(asRoot, append([]string{"--dry-run", "--plumb-binary", h.plumb}, bad...)...); code == 0 {
			t.Errorf("accepted %v", bad)
		}
	}
}

// D-187 on a repair run: an env file of an older install gets the two
// lines; an env file that has them is not changed.
func TestDNSAddedOnRepair(t *testing.T) {
	h := newHost(t)
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	envFile := filepath.Join(h.state, "push", "_etc_plumb_plumb.env")
	b, _ := os.ReadFile(envFile)
	var old []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(l, "PLUMB_PVE_NAMESERVER=") && !strings.HasPrefix(l, "PLUMB_PVE_SEARCHDOMAIN=") {
			old = append(old, l)
		}
	}
	os.WriteFile(envFile, []byte(strings.Join(old, "\n")), 0o600)
	h.resetCalls()
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("repair: %d %s %s", code, out, errOut)
	}
	n := 0
	for _, c := range h.calls() {
		if strings.HasPrefix(c, "pct exec 105 -- sh -c test -f /etc/plumb/plumb.env && { grep -q '^PLUMB_PVE_") {
			n++
			if strings.Contains(c, "SECRET") {
				t.Errorf("the repair touched the secret: %s", c)
			}
		}
	}
	if n != 2 {
		t.Errorf("DNS lines added: %d, want 2\n%s", n, strings.Join(h.calls(), "\n"))
	}
	// With the lines in place, nothing is added.
	os.WriteFile(envFile, b, 0o600)
	h.resetCalls()
	h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
	if index(h.calls(), "pct exec 105 -- sh -c test -f /etc/plumb/plumb.env") >= 0 {
		t.Error("lines added although the env file has them")
	}
}

// Review S1 of the PR #26 delta: a value with a newline (or any other bad
// character) never reaches plumb.env; strict address checks.
func TestDNSValuesAreChecked(t *testing.T) {
	for _, args := range [][]string{
		{"--nameserver", "192.0.2.53\nPLUMB_PVE_URL=https://evil.example:8006"},
		{"--nameserver", "192.0.2.53\n"},
		{"--searchdomain", "plumb.test\nPLUMB_PVE_URL=https://evil.example:8006"},
		{"--nameserver", "999.1.1.1"},
		{"--nameserver", ":::"},
		{"--nameserver", "1:2:3:4:5:6:7:8:9"},
		{"--nameserver", "2001:db8::1::2"},
		{"--nameserver", "0.0.0.0"},
		{"--nameserver", "127.0.0.1"},
		{"--nameserver", "::1"},
		{"--nameserver", "192.0.2.1;id"},
	} {
		h := newHost(t)
		code, _, _ := h.run(asRoot, append([]string{"--yes", "--plumb-binary", h.plumb}, args...)...)
		if code == 0 {
			t.Errorf("accepted %q", args)
		}
		for _, c := range h.calls() {
			if mutating.MatchString(c) {
				t.Errorf("%q: changed the host: %s", args, c)
				break
			}
		}
	}
	for _, ok := range []string{"192.0.2.1", "2001:db8::53", "2001:db8:0:0:0:0:0:53", "192.0.2.1,2001:db8::1,192.0.2.2"} {
		h := newHost(t)
		if code, out, errOut := h.run(nil, "--dry-run", "--plumb-binary", h.plumb, "--nameserver", ok); code != 0 {
			t.Errorf("refused %q: %s %s", ok, out, errOut)
		}
	}
	// 0.0.0.0 in resolv.conf is skipped like loopback.
	h := newHost(t)
	os.WriteFile(filepath.Join(filepath.Dir(h.state), "resolv.conf"), []byte("nameserver 0.0.0.0\nnameserver 192.0.2.7\nsearch plumb.test\n"), 0o644)
	h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
	b, _ := os.ReadFile(filepath.Join(h.state, "push", "_etc_plumb_plumb.env"))
	if !strings.Contains(string(b), "\nPLUMB_PVE_NAMESERVER=192.0.2.7\n") {
		t.Errorf("env:\n%s", b)
	}
}

// Test host (T-013 reinstall): pct destroy --purge keeps rules.seccomp and
// an empty passthrough/ in /var/lib/lxc/<id>. --remove cleans them and the
// empty state dir; the next install with the same id works.
func TestRemoveThenInstallSameID(t *testing.T) {
	h := newHost(t)
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("install: %d %s %s", code, out, errOut)
	}
	lxcDir := filepath.Join(filepath.Dir(h.state), "lxc", "105")
	if code, out, errOut := h.runRemove(); code != 0 {
		t.Fatalf("remove: %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(lxcDir); !os.IsNotExist(err) {
		t.Errorf("remove kept %s: %v", lxcDir, err)
	}
	if _, err := os.Stat(filepath.Dir(h.stateF)); !os.IsNotExist(err) {
		t.Errorf("remove kept the empty state dir: %v", err)
	}
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 || index(h.calls(), "pct create 105") < 0 {
		t.Errorf("install after remove: %d %s %s", code, out, errOut)
	}
}

// The host as the failed run left it: leftovers of pct destroy, no
// container, a state file without lxc_id. The install removes exactly the
// known set; any other entry, or a symbolic link, stops it before a change.
func TestStaleLXCDirKnownSet(t *testing.T) {
	cases := []struct {
		name string
		make func(dir string)
		ok   bool
	}{
		{"pct destroy leftovers", func(dir string) {}, true},
		{"with empty rootfs", func(dir string) { os.Mkdir(filepath.Join(dir, "rootfs"), 0o700) }, true},
		{"unknown file", func(dir string) { os.WriteFile(filepath.Join(dir, "config"), nil, 0o644) }, false},
		{"file in passthrough", func(dir string) { os.WriteFile(filepath.Join(dir, "passthrough", "mounts"), nil, 0o644) }, false},
		{"rules.seccomp is a dir", func(dir string) {
			os.Remove(filepath.Join(dir, "rules.seccomp"))
			os.Mkdir(filepath.Join(dir, "rules.seccomp"), 0o755)
		}, false},
		{"symlinked rules.seccomp", func(dir string) {
			target := filepath.Join(t.TempDir(), "keep")
			os.WriteFile(target, []byte("x"), 0o644)
			os.Remove(filepath.Join(dir, "rules.seccomp"))
			os.Symlink(target, filepath.Join(dir, "rules.seccomp"))
		}, false},
		{"symlinked passthrough", func(dir string) {
			os.Remove(filepath.Join(dir, "passthrough"))
			os.Symlink(t.TempDir(), filepath.Join(dir, "passthrough"))
		}, false},
		{"hidden file", func(dir string) { os.WriteFile(filepath.Join(dir, ".x"), nil, 0o644) }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHost(t)
			dir := filepath.Join(filepath.Dir(h.state), "lxc", "105")
			os.MkdirAll(filepath.Join(dir, "passthrough"), 0o755)
			os.WriteFile(filepath.Join(dir, "rules.seccomp"), []byte("rules"), 0o644)
			c.make(dir)
			os.MkdirAll(filepath.Dir(h.stateF), 0o700)
			os.WriteFile(h.stateF, []byte("lxc_id=\npool=plumb\n"), 0o600)
			code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
			_, err := os.Stat(dir)
			if c.ok {
				if code != 0 || !os.IsNotExist(err) || index(h.calls(), "pct create 105") < 0 {
					t.Errorf("known leftovers: %d, dir gone: %v\n%s %s", code, os.IsNotExist(err), out, errOut)
				}
				return
			}
			if code == 0 || !strings.Contains(errOut, "check it by hand") || !strings.Contains(errOut, "Nothing was changed") || err != nil {
				t.Errorf("unknown entry: %d %q, dir kept: %v", code, errOut, err == nil)
			}
			for _, call := range h.calls() {
				if mutating.MatchString(call) {
					t.Errorf("changed the host before the stop: %s", call)
				}
			}
			if c.name == "symlinked rules.seccomp" {
				if _, err := os.Stat(filepath.Join(dir, "rules.seccomp")); err != nil {
					t.Error("removed a symbolic link entry")
				}
			}
		})
	}
}

// Review nit: an lxc_id in the state file that is not a number stops every
// mode before a change (it goes into pct commands and paths).
func TestStateLXCIDMustBeNumber(t *testing.T) {
	for _, args := range [][]string{{"--yes", "--plumb-binary", ""}, {"--dry-run", "--plumb-binary", ""}, {"--remove", "--yes"}} {
		h := newHost(t)
		if args[len(args)-1] == "" {
			args[len(args)-1] = h.plumb
		}
		os.MkdirAll(filepath.Dir(h.stateF), 0o700)
		os.WriteFile(h.stateF, []byte("lxc_id=../105\npool=plumb\n"), 0o600)
		code, _, errOut := h.run(asRoot, args...)
		if code == 0 || !strings.Contains(errOut, "lxc_id that is not a number") {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
		for _, c := range h.calls() {
			if mutating.MatchString(c) {
				t.Errorf("%v: changed the host: %s", args, c)
			}
		}
	}
}

// Review nit: the leftover cleanup never touches the dir of an id whose
// container still exists (here: a pct destroy that left it).
func TestStaleCleanupNeedsNoContainer(t *testing.T) {
	h := newHost(t)
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("install: %d %s %s", code, out, errOut)
	}
	dir := filepath.Join(filepath.Dir(h.state), "lxc", "105")
	os.MkdirAll(filepath.Join(dir, "passthrough"), 0o755)
	os.WriteFile(filepath.Join(dir, "rules.seccomp"), []byte("rules"), 0o644)
	code, out, errOut := h.runRemove("FAKE_DESTROY_NOOP=1")
	if !strings.Contains(out, "Kept "+dir+": container 105 exists.") {
		t.Errorf("remove: %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "rules.seccomp")); err != nil {
		t.Errorf("removed a file of a live container: %v", err)
	}
}
