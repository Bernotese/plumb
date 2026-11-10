// Tests of the key volume, plumb key init and the LXC address (T-052,
// D-177 … D-179) against the fake Proxmox commands.
package install

import (
	"os"
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
