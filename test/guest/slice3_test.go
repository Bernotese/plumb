//go:build guest

// Guest tests of apt_repository, unit and sysctl (T-093).
package guest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// testRepo is a flat apt repository with one package, signed by a fresh
// key and served on 127.0.0.1.
type testRepo struct {
	url string // ends in "/"
	key string // armored public key
}

func gpg(t *testing.T, home string, args ...string) string {
	t.Helper()
	return sh(t, "gpg", append([]string{"--homedir", home, "--batch", "--no-tty", "--pinentry-mode", "loopback", "--passphrase", ""}, args...)...)
}

// newTestRepo builds the repository in a temp dir: repo/Packages,
// repo/InRelease (clear-signed) and the .deb.
func newTestRepo(t *testing.T, pkgName string) testRepo {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "gnupg")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	gpg(t, home, "--quick-gen-key", "Plumb test repo <repo@plumb.invalid>", "ed25519", "sign", "never")
	key := gpg(t, home, "--armor", "--export") + "\n"

	// The package: one file, architecture all.
	root := filepath.Join(dir, "pkg")
	mustWriteFile(t, filepath.Join(root, "DEBIAN/control"), fmt.Sprintf(
		"Package: %s\nVersion: 1.0\nArchitecture: all\nMaintainer: Plumb <t@plumb.invalid>\nDescription: Plumb test package\n", pkgName))
	mustWriteFile(t, filepath.Join(root, "usr/share", pkgName, "hello"), "hello\n")
	repo := filepath.Join(dir, "www/repo")
	deb := filepath.Join(repo, pkgName+"_1.0_all.deb")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	sh(t, "dpkg-deb", "--root-owner-group", "--build", root, deb)
	data, err := os.ReadFile(deb)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	// Filename is relative to the URI of the source.
	packages := fmt.Sprintf("Package: %s\nVersion: 1.0\nArchitecture: all\nMaintainer: Plumb <t@plumb.invalid>\nFilename: repo/%s\nSize: %d\nSHA256: %s\nDescription: Plumb test package\n\n",
		pkgName, filepath.Base(deb), len(data), hex.EncodeToString(sum[:]))
	mustWriteFile(t, filepath.Join(repo, "Packages"), packages)
	psum := sha256.Sum256([]byte(packages))
	release := fmt.Sprintf("Origin: Plumb test\nLabel: Plumb test\nDate: %s\nSHA256:\n %s %d Packages\n",
		time.Now().UTC().Format(time.RFC1123Z), hex.EncodeToString(psum[:]), len(packages))
	mustWriteFile(t, filepath.Join(repo, "Release"), release)
	gpg(t, home, "--clearsign", "--output", filepath.Join(repo, "InRelease"), filepath.Join(repo, "Release"))

	srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Join(dir, "www"))))
	t.Cleanup(srv.Close)
	return testRepo{url: srv.URL + "/", key: key}
}

func mustWriteFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repoRes(name string, r testRepo, key string) wire.Resource {
	return rsrc("apt_repository", name, map[string]any{
		"uris": []any{r.url}, "suites": []any{"repo/"}, "components": []any{}, "key": key,
	})
}

// TestAptRepositoryInGuest: apt trusts the repository only through the
// key in Signed-By, the package installs from it, a wrong key fails at
// apt-get update, and nothing lands in trusted.gpg.d.
func TestAptRepositoryInGuest(t *testing.T) {
	const pkgName = "plumb-t093-hello"
	repo := newTestRepo(t, pkgName)
	trustedBefore := sh(t, "sh", "-c", "ls -A /etc/apt/trusted.gpg.d; ls -l /etc/apt/trusted.gpg 2>/dev/null || true")
	t.Cleanup(func() {
		_ = exec.Command("apt-get", "-y", "purge", "--", pkgName).Run()
		for _, n := range []string{"t093", "t093-wrong"} {
			_ = os.Remove("/etc/apt/sources.list.d/plumb-" + n + ".sources")
			_ = os.Remove("/etc/apt/keyrings/plumb-" + n + ".asc")
		}
		_ = exec.Command("apt-get", "update").Run()
	})

	changes := converge(t, repoRes("t093", repo, repo.key))
	if wire.ActionOf(changes) != wire.ActionCreate {
		t.Fatalf("create = %+v", changes)
	}
	srcBytes, err := os.ReadFile("/etc/apt/sources.list.d/plumb-t093.sources")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	if !strings.Contains(src, "Signed-By: /etc/apt/keyrings/plumb-t093.asc\n") || strings.Contains(src, "Trusted") {
		t.Fatalf("sources:\n%s", src)
	}
	if got := sh(t, "apt-cache", "policy", "--", pkgName); !strings.Contains(got, "Candidate: 1.0") {
		t.Fatalf("apt-cache policy:\n%s", got)
	}
	converge(t, rsrc("package", pkgName, nil))
	if got := sh(t, "cat", "/usr/share/"+pkgName+"/hello"); got != "hello" {
		t.Fatalf("package file = %q", got)
	}

	// Drift: a hand edit of the source.
	if err := os.WriteFile("/etc/apt/sources.list.d/plumb-t093.sources", []byte(src+"Trusted: yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rs := build(t, repoRes("t093", repo, repo.key))
	if got := plan(t, rs); len(got) != 1 || got[0].Attr != "sources" {
		t.Fatalf("drift = %+v", got)
	}
	converge(t, repoRes("t093", repo, repo.key))

	// The key of another repository does not sign this one.
	other := newTestRepo(t, "plumb-t093-other")
	wrong := build(t, repoRes("t093-wrong", repo, other.key))
	err = wrong.Apply(t.Context(), plan(t, wrong))
	if err == nil {
		t.Fatal("apt-get update passed with the wrong key")
	}
	t.Logf("wrong key: %v", err)
	converge(t, rsrc("apt_repository", "t093-wrong", map[string]any{"state": "absent"}))

	if after := sh(t, "sh", "-c", "ls -A /etc/apt/trusted.gpg.d; ls -l /etc/apt/trusted.gpg 2>/dev/null || true"); after != trustedBefore {
		t.Fatalf("trusted keys changed:\nbefore %q\nafter  %q", trustedBefore, after)
	}

	// Delete: the files go, the package stays.
	converge(t, rsrc("apt_repository", "t093", map[string]any{"state": "absent"}))
	for _, p := range []string{"/etc/apt/sources.list.d/plumb-t093.sources", "/etc/apt/keyrings/plumb-t093.asc"} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still there", p)
		}
	}
	if !strings.Contains(sh(t, "dpkg-query", "-W", "-f=${db:Status-Abbrev}", "--", pkgName), "ii") {
		t.Fatal("the package went with the repository")
	}
}

// TestUnitInGuest: a service and its timer as units; a service resource
// enables and starts the timer; drift and delete.
func TestUnitInGuest(t *testing.T) {
	const svc, timer = "plumb-t093.service", "plumb-t093.timer"
	t.Cleanup(func() {
		_ = exec.Command("systemctl", "disable", "--now", "--", timer).Run()
		_ = os.Remove("/etc/systemd/system/" + svc)
		_ = os.Remove("/etc/systemd/system/" + timer)
		_ = exec.Command("systemctl", "daemon-reload").Run()
	})
	svcUnit := "[Unit]\nDescription=Plumb T-093 test\n\n[Service]\nType=oneshot\nExecStart=/usr/bin/touch /run/plumb-t093.ran\n"
	timerUnit := "[Unit]\nDescription=Plumb T-093 timer\n\n[Timer]\nOnActiveSec=1s\nOnUnitActiveSec=1h\nAccuracySec=1ms\n\n[Install]\nWantedBy=timers.target\n"

	converge(t, rsrc("unit", svc, map[string]any{"content": svcUnit}))
	converge(t, rsrc("unit", timer, map[string]any{"content": timerUnit}))
	if got := sh(t, "systemctl", "show", "--property=LoadState", "--", timer); got != "LoadState=loaded" {
		t.Fatalf("timer %s", got)
	}
	converge(t, rsrc("service", timer, map[string]any{"restart_on": []any{"unit:" + timer}}))
	if got := sh(t, "systemctl", "is-active", "--", timer); got != "active" {
		t.Fatalf("timer is %s", got)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat("/run/plumb-t093.ran"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the timer did not start the service")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Drift: a hand edit. Apply reloads systemd, so systemd sees the new file.
	if err := os.WriteFile("/etc/systemd/system/"+svc, []byte(strings.Replace(svcUnit, "T-093 test", "edited", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	rs := build(t, rsrc("unit", svc, map[string]any{"content": svcUnit}))
	if got := plan(t, rs); len(got) != 1 || got[0].Attr != "content" {
		t.Fatalf("drift = %+v", got)
	}
	converge(t, rsrc("unit", svc, map[string]any{"content": svcUnit}))
	if got := sh(t, "systemctl", "show", "--property=Description", "--", svc); got != "Description=Plumb T-093 test" {
		t.Fatalf("after revert: %s", got)
	}

	// Delete: stop and disable with service, then remove the units.
	converge(t, rsrc("service", timer, map[string]any{"state": "stopped", "enabled": false}))
	changes := converge(t, rsrc("unit", timer, map[string]any{"state": "absent"}))
	if wire.ActionOf(changes) != wire.ActionDelete || changes[0].Risk != wire.RiskDestructive {
		t.Fatalf("delete = %+v", changes)
	}
	converge(t, rsrc("unit", svc, map[string]any{"state": "absent"}))
	if got := sh(t, "systemctl", "show", "--property=LoadState", "--", timer); got != "LoadState=not-found" {
		t.Fatalf("after delete: %s", got)
	}
}

// TestSysctlInGuest uses a key of the network namespace of the container,
// so the test never changes the kernel of the CI host.
func TestSysctlInGuest(t *testing.T) {
	const name = "net.ipv4.ip_forward"
	const proc = "/proc/sys/net/ipv4/ip_forward"
	const conf = "/etc/sysctl.d/60-plumb-net.ipv4.ip_forward.conf"
	before := sh(t, "cat", proc)
	t.Cleanup(func() {
		_ = os.Remove(conf)
		_ = os.WriteFile(proc, []byte(before+"\n"), 0o644)
	})
	if err := os.WriteFile(proc, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	changes := converge(t, rsrc("sysctl", name, map[string]any{"value": float64(1)}))
	if wire.ActionOf(changes) != wire.ActionCreate || changes[len(changes)-1].Risk != wire.RiskDisruptive {
		t.Fatalf("create = %+v", changes)
	}
	if got := sh(t, "sysctl", "-n", name); got != "1" {
		t.Fatalf("running value %s", got)
	}

	// The file works for sysctl(8) at boot.
	if err := os.WriteFile(proc, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, "sysctl", "-p", conf)
	if got := sh(t, "sysctl", "-n", name); got != "1" {
		t.Fatalf("after sysctl -p: %s", got)
	}

	// Drift of the running value.
	if err := os.WriteFile(proc, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rs := build(t, rsrc("sysctl", name, map[string]any{"value": "1"}))
	if got := plan(t, rs); len(got) != 1 || got[0].Attr != "value" || got[0].From != "0" {
		t.Fatalf("drift = %+v", got)
	}
	converge(t, rsrc("sysctl", name, map[string]any{"value": "1"}))

	// Absent removes the file, keeps the value.
	converge(t, rsrc("sysctl", name, map[string]any{"state": "absent"}))
	if _, err := os.Lstat(conf); !os.IsNotExist(err) {
		t.Fatal("file still there")
	}
	if got := sh(t, "sysctl", "-n", name); got != "1" {
		t.Fatalf("value after absent: %s", got)
	}
}
