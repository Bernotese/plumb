package proxmox

// The read-only Proxmox view of GET /settings (T-174, D-361).

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fullPerms is a permissions answer with every ACL line of the install.
func fullPerms() map[string]map[string]int {
	p := map[string]map[string]int{}
	cfg := Config{Pool: fakePool, DiskStorage: "local-lvm", ImageStorage: "local", SnippetStorage: "plumb-snippets", Bridge: "vmbr0", Node: fakeNode}
	addLines(p, installLines(cfg, InstallExtras{}))
	return p
}

// addLines puts the rights of lines into a permissions answer.
func addLines(p map[string]map[string]int, lines []installLine) {
	for _, l := range lines {
		if p[l.path] == nil {
			p[l.path] = map[string]int{}
		}
		for _, priv := range l.privs {
			p[l.path][priv] = 0 // propagate 0 still holds the right on the path
		}
	}
}

func TestViewACL(t *testing.T) {
	f := newFakePVE(t)
	f.perms = fullPerms()
	d := f.driver(t)
	v := NewViewer(d, TokenUser(fakeToken), InstallExtras{})
	got := v.View(context.Background())
	if got.ACL != ACLOK || got.ACLError != "" || len(got.ACLLines) != len(installRoles)-2 || got.GuestBackup != nil || got.ExtraNets != nil {
		t.Fatalf("full rights: %+v", got)
	}
	if got.Version != "9.2.26" || got.LastOK.IsZero() || got.Node != fakeNode || got.Pool != fakePool ||
		got.ImageStorage != "local" || got.SnippetStorage != "plumb-snippets" || got.TokenUser != "plumb@pve" {
		t.Errorf("view = %+v", got)
	}
	if !regexp.MustCompile(`^([0-9a-f]{2}:){31}[0-9a-f]{2}$`).MatchString(got.TLSSHA256) || got.TLSSHA256 != fingerprint(f.srv.Certificate().Raw) {
		t.Errorf("tls_sha256 = %q", got.TLSSHA256)
	}
	if got.APIHost != "127.0.0.1" || got.APIPort == 0 {
		t.Errorf("api = %s:%d", got.APIHost, got.APIPort)
	}
	// No token name or secret anywhere in the view.
	if s := strings.Join([]string{got.TokenUser, got.ACLError}, " "); strings.Contains(s, "!plumb") || strings.Contains(s, fakeSecret) {
		t.Errorf("view holds the token: %q", s)
	}

	// One right of a line is gone: that line is not ok, the state is missing.
	f.mu.Lock()
	delete(f.perms["/"], "Sys.Audit")
	f.mu.Unlock()
	v.now = func() time.Time { return time.Now().Add(time.Minute) } // past the cache
	got = v.View(context.Background())
	if got.ACL != ACLMissing {
		t.Fatalf("one right gone: %+v", got)
	}
	for _, l := range got.ACLLines {
		if l.OK == (l.Role == "PlumbAudit") {
			t.Errorf("line %+v", l)
		}
	}
}

// A right that a parent path propagates counts; one with propagate 0 on a
// parent does not.
func TestViewACLPropagate(t *testing.T) {
	cfg := Config{Pool: "plumb", DiskStorage: "d", ImageStorage: "i", SnippetStorage: "s", Bridge: "vmbr0", Node: "pve"}
	p := fullPerms()
	delete(p, "/storage/local-lvm")
	delete(p, "/storage/local")
	delete(p, "/storage/plumb-snippets")
	p["/storage"] = map[string]int{"Datastore.AllocateSpace": 1, "Datastore.Audit": 1, "Datastore.AllocateTemplate": 0}
	state, lines := checkACL(cfg, InstallExtras{}, p)
	want := map[string]bool{"PlumbDisk": true, "PlumbSnippets": true, "PlumbImages": false}
	for _, l := range lines {
		if w, ok := want[l.Role]; ok && l.OK != w {
			t.Errorf("%s ok = %v, want %v", l.Role, l.OK, w)
		}
	}
	if state != ACLMissing {
		t.Errorf("state = %s", state)
	}
}

// A failed read is unknown with a fixed reason; the view keeps the version
// of the last good call. Only GET calls are sent.
func TestViewACLUnknown(t *testing.T) {
	f := newFakePVE(t)
	f.perms = nil // 403
	d := f.driver(t)
	v := NewViewer(d, "plumb@pve", InstallExtras{})
	got := v.View(context.Background())
	if got.ACL != ACLUnknown || got.ACLError != "Proxmox answered 403 for /access/permissions" || got.ACLLines == nil || len(got.ACLLines) != 0 {
		t.Fatalf("403: %+v", got)
	}
	if got.Version != "9.2.26" {
		t.Errorf("version = %q", got.Version)
	}
	first := got.LastOK
	f.srv.Close()
	v.now = func() time.Time { return time.Now().Add(time.Minute) }
	got = v.View(context.Background())
	if got.ACL != ACLUnknown || got.ACLError != "cannot reach Proxmox" || got.Version != "9.2.26" || !got.LastOK.Equal(first) {
		t.Errorf("down: %+v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.log {
		if !strings.HasPrefix(l, "GET ") {
			t.Errorf("the view sent %s", l)
		}
	}
}

// The cache: a second View within the TTL sends no request.
func TestViewCache(t *testing.T) {
	f := newFakePVE(t)
	v := NewViewer(f.driver(t), "plumb@pve", InstallExtras{})
	v.View(context.Background())
	v.View(context.Background())
	if n := f.requests("GET /version"); n != 1 {
		t.Errorf("GET /version sent %d times, want 1", n)
	}
	// A request that the caller cut is not cached.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v2 := NewViewer(f.driver(t), "plumb@pve", InstallExtras{})
	if got := v2.View(ctx); got.ACL != ACLUnknown {
		t.Fatalf("cut: %+v", got)
	}
	if got := v2.View(context.Background()); got.ACL == ACLUnknown {
		t.Errorf("after a cut request: %+v", got)
	}
}

func TestTokenUser(t *testing.T) {
	for in, want := range map[string]string{"plumb@pve!plumb": "plumb@pve", "plumb@pve": "", "!x": "", "": ""} {
		if got := TokenUser(in); got != want {
			t.Errorf("TokenUser(%q) = %q, want %q", in, got, want)
		}
	}
}

// installRoles must equal ROLE_NAMES, ROLE_PRIVS and role_path of
// install.sh, so the view checks the lines that the installer writes.
func TestInstallRolesMatchInstaller(t *testing.T) {
	raw, err := os.ReadFile("../../../install/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	sh := string(raw)
	names := regexp.MustCompile(`(?m)^ROLE_NAMES=\(([^)]*)\)`).FindStringSubmatch(sh)
	if names == nil {
		t.Fatal("ROLE_NAMES not found")
	}
	var goNames []string
	for _, r := range installRoles {
		goNames = append(goNames, r.Role)
	}
	if strings.Join(strings.Fields(names[1]), " ") != strings.Join(goNames, " ") {
		t.Errorf("roles: install.sh %q, Go %q", names[1], goNames)
	}
	cfg := Config{Pool: "$POOL", DiskStorage: "$DISK_STORAGE", ImageStorage: "$IMAGE_STORAGE", SnippetStorage: "$SNIPPET_STORAGE", Bridge: "$BRIDGE", Node: "$NODE"}
	x := InstallExtras{GuestBackupStorage: "$GUEST_BACKUP_STORAGE"}
	for _, r := range installRoles {
		privs := regexp.MustCompile(`(?m)^\s*\[` + r.Role + `\]="([^"]*)"`).FindStringSubmatch(sh)
		if privs == nil || privs[1] != strings.Join(r.Privs, " ") {
			t.Errorf("%s privs: install.sh %v, Go %q", r.Role, privs, r.Privs)
		}
		path := regexp.MustCompile(`(?m)^\s*` + r.Role + `\) echo "([^"]*)" ;;`).FindStringSubmatch(sh)
		if path == nil || path[1] != r.path(cfg, x) {
			t.Errorf("%s path: install.sh %v, Go %q", r.Role, path, r.path(cfg, x))
		}
	}
}
