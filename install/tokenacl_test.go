package install

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// T-129 (T-122 F4): PVE 9.2 keeps the ACL lines of a removed token, and the
// next token with the same name gets them again. The fake PVE does the same.

const plumbToken = "plumb@pve!plumb"

// wantTokenACLs is the token rights of D-081 and D-240, as the fake stores
// them: path ugid role type propagate.
var wantTokenACLs = []string{
	"/ plumb@pve!plumb PlumbAudit token 0",
	"/nodes/pve1 plumb@pve!plumb PlumbNode token 0",
	"/pool/plumb plumb@pve!plumb PlumbGuests token 1",
	"/sdn/zones/localnetwork/vmbr0 plumb@pve!plumb PlumbBridge token 0",
	"/storage/local-lvm plumb@pve!plumb PlumbDisk token 0",
	"/storage/plumb-images plumb@pve!plumb PlumbImages token 0",
	"/storage/plumb-snippets plumb@pve!plumb PlumbSnippets token 0",
}

// aclLines returns the sorted lines of user.cfg (the fake acl file) for a
// ugid, also the lines that the list hides.
func aclLines(t *testing.T, h *host, ugid string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.state, "acl"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[1] == ugid {
			out = append(out, strings.Join(f, " "))
		}
	}
	sort.Strings(out)
	return out
}

func checkTokenACLs(t *testing.T, h *host, when string) {
	t.Helper()
	if got := aclLines(t, h, plumbToken); strings.Join(got, "\n") != strings.Join(wantTokenACLs, "\n") {
		t.Errorf("token ACLs after %s:\n%s\nwant:\n%s", when, strings.Join(got, "\n"), strings.Join(wantTokenACLs, "\n"))
	}
}

// tokenACLDeletesBefore checks that every token ACL is deleted before the
// token is removed.
func tokenACLDeletesBefore(t *testing.T, calls []string) {
	t.Helper()
	rm := index(calls, "pveum user token remove plumb@pve plumb")
	if rm < 0 {
		t.Fatalf("no token remove:\n%s", strings.Join(calls, "\n"))
	}
	for _, l := range wantTokenACLs {
		f := strings.Fields(l)
		c := "pveum acl delete " + f[0] + " --tokens " + plumbToken + " --roles " + f[2]
		if i := index(calls, c); i < 0 || i > rm {
			t.Errorf("%q at %d, token remove at %d", c, i, rm)
		}
	}
}

func TestTokenACLsKeepGuestsAndRestore(t *testing.T) {
	b := installedWithGuests(t)
	checkTokenACLs(t, b.host, "install")
	userACLs := aclLines(t, b.host, "plumb@pve")
	if len(userACLs) != len(wantTokenACLs) {
		t.Fatalf("user ACLs after install: %v", userACLs)
	}
	b.resetCalls()
	if code, out, errOut := b.keepGuests(nil); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	tokenACLDeletesBefore(t, b.calls())
	if got := aclLines(t, b.host, plumbToken); len(got) != 0 {
		t.Errorf("token ACL lines stay after --keep-guests:\n%s", strings.Join(got, "\n"))
	}
	// D-290: the user keeps its ACLs.
	if got := aclLines(t, b.host, "plumb@pve"); strings.Join(got, "\n") != strings.Join(userACLs, "\n") {
		t.Errorf("user ACLs changed: %v, want %v", got, userACLs)
	}

	file := filepath.Join(b.store, "plumb-backups", "plumb-20261113T030000Z.pbk")
	b.resetCalls()
	if code, out, errOut := b.restore(nil, file, "--lxc-ip", "192.168.1.50/24", "--lxc-gw", "192.168.1.1", "--backup-storage", "backups"); code != 0 {
		t.Fatalf("restore: %d\n%s\n%s", code, out, errOut)
	}
	checkTokenACLs(t, b.host, "restore")

	b.install("--backup-storage", "backups", "--lxc-ip", "192.168.1.50/24", "--lxc-gw", "192.168.1.1")
	checkTokenACLs(t, b.host, "repair")
}

// A host that an older installer left with the lines of a removed token
// (one more than planned): the next install sets the token ACLs itself and
// removes the extra line.
func TestTokenACLLeftoverCleaned(t *testing.T) {
	h := newHost(t)
	root := h.installed()
	acl := filepath.Join(h.state, "acl")
	raw, _ := os.ReadFile(acl)
	os.WriteFile(acl, append(raw, []byte("/nodes/pve1 plumb@pve!plumb PlumbAudit token 1\n")...), 0o644)
	// The old installer: token gone, its lines stay; the LXC is gone too.
	os.Remove(filepath.Join(h.state, "token.plumb"))
	os.Remove(filepath.Join(h.state, "lxc.105"))
	os.RemoveAll(filepath.Join(h.state, "push"))
	if code, out, errOut := h.run(root, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	calls := h.calls()
	add := index(calls, "pveum user token add plumb@pve plumb")
	if add < 0 {
		t.Fatal("no new token")
	}
	// Every planned token ACL is set after the token add, even where a
	// leftover line has it already.
	for _, l := range wantTokenACLs {
		f := strings.Fields(l)
		c := "pveum acl modify " + f[0] + " --tokens " + plumbToken + " --roles " + f[2] + " --propagate " + f[4]
		if i := index(calls, c); i < add {
			t.Errorf("%q at %d, token add at %d", c, i, add)
		}
	}
	if i := index(calls, "pveum acl delete /nodes/pve1 --tokens plumb@pve!plumb --roles PlumbAudit"); i < add {
		t.Errorf("leftover line not removed (%d):\n%s", i, strings.Join(calls, "\n"))
	}
	checkTokenACLs(t, h, "a run over leftover lines")
}

func TestTokenACLsFullRemove(t *testing.T) {
	h := newHost(t)
	root := h.installed()
	// The dry run shows the new steps.
	code, out, errOut := h.run(root, "--remove", "--yes", "--dry-run")
	if code != 0 || !strings.Contains(out, `+ pveum acl delete / --tokens plumb@pve\!plumb --roles PlumbAudit`) {
		t.Errorf("dry run: %d\n%s\n%s", code, out, errOut)
	}
	if c := h.changed(); len(c) != 0 {
		t.Errorf("dry run changed: %v", c)
	}
	os.Remove(filepath.Join(h.state, "calls"))
	if code, out, errOut := h.run(root, "--remove", "--yes"); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	tokenACLDeletesBefore(t, h.calls())
	if got := aclLines(t, h, plumbToken); len(got) != 0 {
		t.Errorf("token ACL lines stay after --remove:\n%s", strings.Join(got, "\n"))
	}
}
