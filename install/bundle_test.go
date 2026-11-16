package install

// T-257 (release slice C3): the installer of a release bundle checks the
// bundle's SHA256SUMS before any of its files is used, and the dry run
// shows the check. A build from source (no SHA256SUMS) is not checked and
// says so. An upgrade from a bundle shows the version change and changes
// no right.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bundle is an unpacked release tarball in a folder of its own.
type bundle struct {
	dir     string
	version string
}

// newBundle writes install.sh (stamped with version, as
// scripts/package-release.sh does), a fake plumb that prints version,
// plumb-agent and a matching SHA256SUMS.
func newBundle(t *testing.T, version string) *bundle {
	t.Helper()
	dir := t.TempDir()
	// As unpacked by root: only the owner may write (M1 of #387).
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	stamped := regexp.MustCompile(`(?m)^RELEASE_VERSION=""$`).ReplaceAll(src, []byte(`RELEASE_VERSION="`+version+`"`))
	if bytes.Equal(stamped, src) {
		t.Fatal("install.sh has no RELEASE_VERSION line to stamp")
	}
	b := &bundle{dir: dir, version: version}
	b.write(t, "install.sh", stamped)
	// It marks every run, so a test sees a use before the check.
	b.write(t, "plumb", []byte("#!/bin/sh\n[ -z \"$PLUMB_TEST_USED\" ] || touch \"$PLUMB_TEST_USED\"\necho plumb "+version+"\n"))
	b.write(t, "plumb-agent", []byte("#!/bin/sh\necho plumb-agent "+version+"\n"))
	b.writeSums(t, b.sums(t, "plumb", "plumb-agent", "install.sh"))
	return b
}

func (b *bundle) write(t *testing.T, name string, data []byte) {
	t.Helper()
	p := filepath.Join(b.dir, name)
	if err := os.WriteFile(p, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o755); err != nil { // not the umask's mode
		t.Fatal(err)
	}
}

func (b *bundle) sums(t *testing.T, names ...string) string {
	t.Helper()
	var out strings.Builder
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(b.dir, n))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&out, "%s  %s\n", hex.EncodeToString(sum[:]), n)
	}
	return out.String()
}

func (b *bundle) writeSums(t *testing.T, s string) {
	t.Helper()
	p := filepath.Join(b.dir, "SHA256SUMS")
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
}

// runBundle runs the bundle's install.sh on the fake host.
func (h *host) runBundle(b *bundle, env []string, args ...string) (int, string, string) {
	h.t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(b.dir, "install.sh"), "--snippet-dir", h.snippet, "--image-dir", h.images()}, args...)...)
	cmd.Env = append(append([]string{}, h.env...), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := bounded(h.t, cmd)
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		h.t.Fatal(err)
	}
	return code, out.String(), errOut.String()
}

// noHostChange fails when the run made any call that changes the host.
func (h *host) noHostChange(t *testing.T) {
	t.Helper()
	for _, c := range h.calls() {
		if mutating.MatchString(c) {
			t.Errorf("a refused bundle changed the host: %s", c)
		}
	}
}

func TestBundleCheckPasses(t *testing.T) {
	h := newHost(t)
	b := newBundle(t, "v0.1.0")
	code, out, errOut := h.runBundle(b, nil, "--dry-run", "--plumb-binary", filepath.Join(b.dir, "plumb"))
	if code != 0 {
		t.Fatalf("dry run: %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "Bundle check of release v0.1.0: SHA256SUMS matches plumb, plumb-agent and install.sh") ||
		!strings.Contains(out, "not who made them") {
		t.Errorf("the dry run does not show the check:\n%s", out)
	}
	if !strings.Contains(out, "plumb version: none -> v0.1.0") {
		t.Errorf("no version line:\n%s", out)
	}
}

// A build from source (no SHA256SUMS, no stamp): nothing to check, and the
// run says so. The other installer tests run this way.
func TestBundleCheckSourceBuild(t *testing.T) {
	h := newHost(t)
	code, out, errOut := h.run(nil, "--dry-run", "--plumb-binary", h.plumb)
	if code != 0 || !strings.Contains(out, "NOTE: no SHA256SUMS next to install.sh: no release bundle") {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
}

func TestBundleCheckRefuses(t *testing.T) {
	good := func(t *testing.T, b *bundle) string { return b.sums(t, "plumb", "plumb-agent", "install.sh") }
	for name, tc := range map[string]struct {
		change func(t *testing.T, b *bundle) // makes the bundle bad
		binary func(b *bundle) string        // --plumb-binary; "" = the bundle's plumb
		want   string
	}{
		"damaged plumb": {change: func(t *testing.T, b *bundle) {
			b.write(t, "plumb", []byte("#!/bin/sh\n[ -z \"$PLUMB_TEST_USED\" ] || touch \"$PLUMB_TEST_USED\"\necho evil\n"))
		}, want: "plumb does not match SHA256SUMS"},
		"damaged install.sh": {change: func(t *testing.T, b *bundle) {
			f, _ := os.OpenFile(filepath.Join(b.dir, "install.sh"), os.O_APPEND|os.O_WRONLY, 0)
			f.WriteString("# changed\n")
			f.Close()
		}, want: "install.sh does not match SHA256SUMS"},
		"damaged plumb-agent": {change: func(t *testing.T, b *bundle) {
			b.write(t, "plumb-agent", []byte("x"))
		}, want: "plumb-agent does not match SHA256SUMS"},
		"missing SHA256SUMS": {change: func(t *testing.T, b *bundle) {
			os.Remove(filepath.Join(b.dir, "SHA256SUMS"))
		}, want: "this installer is of release v0.1.0, but"},
		"missing plumb-agent": {change: func(t *testing.T, b *bundle) {
			os.Remove(filepath.Join(b.dir, "plumb-agent"))
		}, want: "plumb-agent of the bundle"},
		"plumb-agent not listed": {change: func(t *testing.T, b *bundle) {
			b.writeSums(t, b.sums(t, "plumb", "install.sh"))
		}, want: "does not list plumb-agent"},
		"a plumb outside the bundle": {binary: func(b *bundle) string {
			p := filepath.Join(filepath.Dir(b.dir), "other-plumb")
			data, _ := os.ReadFile(filepath.Join(b.dir, "plumb"))
			os.WriteFile(p, data, 0o755) // the same bytes, but not the listed file
			return p
		}, want: "is not the plumb of this bundle"},
		"a symlink as --plumb-binary": {binary: func(b *bundle) string {
			p := filepath.Join(filepath.Dir(b.dir), "link-plumb")
			os.Symlink(filepath.Join(b.dir, "plumb"), p)
			return p
		}, want: "is not the plumb of this bundle"},
		"plumb is a symlink in the bundle": {change: func(t *testing.T, b *bundle) {
			sums := good(t, b)
			real := filepath.Join(filepath.Dir(b.dir), "real-plumb")
			os.Rename(filepath.Join(b.dir, "plumb"), real)
			os.Symlink(real, filepath.Join(b.dir, "plumb"))
			b.writeSums(t, sums)
		}, want: "plumb of the bundle"},
		"SHA256SUMS is a symlink": {change: func(t *testing.T, b *bundle) {
			real := filepath.Join(filepath.Dir(b.dir), "real-sums")
			os.WriteFile(real, []byte(good(t, b)), 0o644)
			os.Remove(filepath.Join(b.dir, "SHA256SUMS"))
			os.Symlink(real, filepath.Join(b.dir, "SHA256SUMS"))
		}, want: "SHA256SUMS of the bundle"},
		"an absolute path": {change: func(t *testing.T, b *bundle) {
			b.writeSums(t, strings.Replace(good(t, b), "  plumb\n", "  /tmp/plumb\n", 1))
		}, want: "line 1 is not"},
		"a dot-dot path": {change: func(t *testing.T, b *bundle) {
			b.writeSums(t, strings.Replace(good(t, b), "  plumb\n", "  ../plumb\n", 1))
		}, want: "line 1 is not"},
		"a ./ path": {change: func(t *testing.T, b *bundle) {
			b.writeSums(t, strings.Replace(good(t, b), "  plumb\n", "  ./plumb\n", 1))
		}, want: "line 1 is not"},
		"the binary mark": {change: func(t *testing.T, b *bundle) {
			b.writeSums(t, strings.Replace(good(t, b), "  plumb\n", " *plumb\n", 1))
		}, want: "line 1 is not"},
		"an unknown file": {change: func(t *testing.T, b *bundle) {
			b.writeSums(t, good(t, b)+strings.Repeat("0", 64)+"  evil\n")
		}, want: "line 4 is not"},
		"two lines for one file": {change: func(t *testing.T, b *bundle) {
			s := good(t, b)
			b.writeSums(t, s+strings.SplitAfter(s, "\n")[0])
		}, want: "lists plumb twice"},
		"two lines, the second one wrong": {change: func(t *testing.T, b *bundle) {
			b.writeSums(t, good(t, b)+strings.Repeat("0", 64)+"  plumb\n")
		}, want: "lists plumb twice"},
		"CRLF": {change: func(t *testing.T, b *bundle) {
			b.writeSums(t, strings.ReplaceAll(good(t, b), "\n", "\r\n"))
		}, want: "carriage return"},
		"an empty line": {change: func(t *testing.T, b *bundle) {
			b.writeSums(t, "\n"+good(t, b))
		}, want: "line 1 is not"},
		"upper-case hex": {change: func(t *testing.T, b *bundle) {
			s := good(t, b)
			b.writeSums(t, strings.ToUpper(s[:64])+s[64:])
		}, want: "line 1 is not"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			b := newBundle(t, "v0.1.0")
			if tc.change != nil {
				tc.change(t, b)
			}
			bin := filepath.Join(b.dir, "plumb")
			if tc.binary != nil {
				bin = tc.binary(b)
			}
			used := filepath.Join(t.TempDir(), "used")
			for _, mode := range [][]string{{"--dry-run"}, {"--yes"}} {
				code, out, errOut := h.runBundle(b, []string{"PLUMB_TEST_USED=" + used}, append(mode, "--plumb-binary", bin)...)
				if code == 0 || !strings.Contains(errOut, tc.want) || !strings.Contains(errOut, "Nothing was changed") {
					t.Fatalf("%v: %d, want %q\n%s\n%s", mode, code, tc.want, out, errOut)
				}
				if strings.Contains(out, "plumb version:") {
					t.Errorf("%v: the binary was used before the check failed:\n%s", mode, out)
				}
				if _, err := os.Stat(used); err == nil {
					t.Errorf("%v: a plumb ran before the check failed", mode)
				}
			}
			h.noHostChange(t)
		})
	}
}

// An upgrade from a bundle keeps the install (no new right, the key and
// the database stay) and shows old -> new; a downgrade between releases
// gets a warning, and the run goes on as before (the installer compares
// nothing and asks nothing).
func TestBundleUpgrade(t *testing.T) {
	h := newHost(t)
	root := []string{"PLUMB_INSTALL_ASSUME_ROOT=1"}
	first := newBundle(t, "v0.1.0")
	if code, out, errOut := h.runBundle(first, root, "--yes", "--plumb-binary", filepath.Join(first.dir, "plumb")); code != 0 {
		t.Fatalf("first install: %d\n%s\n%s", code, out, errOut)
	}
	keys, _ := filepath.Glob(filepath.Join(h.state, "key.*"))
	if len(keys) != 1 {
		t.Fatalf("no key after the install: %v", keys)
	}
	os.Remove(filepath.Join(h.state, "calls"))

	next := newBundle(t, "v0.1.1")
	code, out, errOut := h.runBundle(next, root, "--yes", "--plumb-binary", filepath.Join(next.dir, "plumb"))
	if code != 0 {
		t.Fatalf("upgrade: %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "plumb version: v0.1.0 -> v0.1.1") || strings.Contains(out, "downgrade") {
		t.Errorf("no version change line:\n%s", out)
	}
	for _, c := range h.calls() {
		if strings.HasPrefix(c, "pveum") && mutating.MatchString(c) {
			t.Errorf("the upgrade changed a right: %s", c)
		}
		if strings.HasPrefix(c, "pct create") || strings.HasPrefix(c, "pct destroy") {
			t.Errorf("the upgrade made the LXC again: %s", c)
		}
	}
	if k, _ := filepath.Glob(filepath.Join(h.state, "key.*")); len(k) != 1 || k[0] != keys[0] {
		t.Errorf("the key changed: %v -> %v", keys, k)
	}
	if got := fileSum(t, filepath.Join(h.state, "push", "_usr_local_bin_plumb")); got != fileSum(t, filepath.Join(next.dir, "plumb")) {
		t.Error("the new plumb is not installed")
	}

	back := newBundle(t, "v0.1.0")
	code, out, errOut = h.runBundle(back, root, "--dry-run", "--plumb-binary", filepath.Join(back.dir, "plumb"))
	if code != 0 || !strings.Contains(out, "plumb version: v0.1.1 -> v0.1.0") ||
		!strings.Contains(out, "WARNING: v0.1.0 is older than the installed v0.1.1 (a downgrade)") {
		t.Errorf("downgrade: %d\n%s\n%s", code, out, errOut)
	}
}

// M1 of #387: a plumb swapped in the bundle folder after the check never
// runs and is never pushed: the run uses the checked private copy.
func TestBundleSwapInTheFolder(t *testing.T) {
	h := newHost(t)
	b := newBundle(t, "v0.1.0")
	plumb := filepath.Join(b.dir, "plumb")
	proven := fileSum(t, plumb)
	evil := filepath.Join(t.TempDir(), "evil-ran")
	swap := "printf '#!/bin/sh\\ntouch " + evil + "\\necho plumb v9.9.9\\n' > " + plumb
	code, out, errOut := h.runBundle(b, []string{"PLUMB_INSTALL_ASSUME_ROOT=1", "FAKE_VERSION_HOOK=" + swap}, "--yes", "--plumb-binary", plumb)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	if fileSum(t, plumb) == proven {
		t.Fatal("the hook did not swap the bundle's plumb")
	}
	if _, err := os.Stat(evil); err == nil {
		t.Error("the swapped plumb ran on the host")
	}
	if !strings.Contains(out, "plumb version: none -> v0.1.0") {
		t.Errorf("the version line is not of the proven plumb:\n%s", out)
	}
	if got := fileSum(t, filepath.Join(h.state, "push", "_usr_local_bin_plumb")); got != proven {
		t.Error("the pushed plumb is not the proven one")
	}
}

// A change of the private copy itself (after the check, before it runs)
// is refused: plumb_unchanged guards every run and the push.
func TestBundleSwapTheCopy(t *testing.T) {
	h := newHost(t)
	b := newBundle(t, "v0.1.0")
	tmp := t.TempDir()
	evil := filepath.Join(t.TempDir(), "evil-ran")
	swap := "for f in " + tmp + "/*/plumb; do printf '#!/bin/sh\\ntouch " + evil + "\\n' > \"$f\"; done"
	code, out, errOut := h.runBundle(b, []string{"PLUMB_INSTALL_ASSUME_ROOT=1", "TMPDIR=" + tmp, "FAKE_VERSION_HOOK=" + swap},
		"--yes", "--plumb-binary", filepath.Join(b.dir, "plumb"))
	if code == 0 || !strings.Contains(errOut, "changed after the bundle check; it is refused") {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(evil); err == nil {
		t.Error("the changed copy ran on the host")
	}
	if _, err := os.Stat(filepath.Join(h.state, "push", "_usr_local_bin_plumb")); err == nil {
		t.Error("a plumb was installed")
	}
	if left, _ := filepath.Glob(filepath.Join(tmp, "tmp.*")); len(left) != 0 {
		t.Errorf("the private copy is left after the run: %v", left)
	}
}

// M1 of #387: a bundle that another account can write is refused.
func TestBundleRights(t *testing.T) {
	for name, mode := range map[string]struct {
		file string
		perm os.FileMode
	}{
		"the folder is world-writable": {"", 0o777},
		"the folder is group-writable": {"", 0o775},
		"plumb is group-writable":      {"plumb", 0o775},
		"SHA256SUMS is world-writable": {"SHA256SUMS", 0o666},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			b := newBundle(t, "v0.1.0")
			if err := os.Chmod(filepath.Join(b.dir, mode.file), mode.perm); err != nil {
				t.Fatal(err)
			}
			code, out, errOut := h.runBundle(b, nil, "--dry-run", "--plumb-binary", filepath.Join(b.dir, "plumb"))
			if code == 0 || !strings.Contains(errOut, "is not owned by this user or is writable by others") || !strings.Contains(errOut, "Nothing was changed") {
				t.Fatalf("%d\n%s\n%s", code, out, errOut)
			}
			h.noHostChange(t)
		})
	}
}

// A real bundle of scripts/package-release.sh passes the check (T-257). It
// runs only with PLUMB_TEST_BUNDLE=<the folder of an unpacked tarball>; the
// release plan's host run (C7) proves it on Proxmox.
func TestBundleRealTarball(t *testing.T) {
	dir := os.Getenv("PLUMB_TEST_BUNDLE")
	if dir == "" {
		t.Skip("PLUMB_TEST_BUNDLE is not set")
	}
	h := newHost(t)
	b := &bundle{dir: dir}
	code, out, errOut := h.runBundle(b, nil, "--dry-run", "--plumb-binary", filepath.Join(dir, "plumb"))
	if code != 0 || !regexp.MustCompile(`Bundle check of release v[0-9.]+\S*: SHA256SUMS matches plumb, plumb-agent and install.sh`).MatchString(out) {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	if !regexp.MustCompile(`plumb version: none -> v[0-9.]+`).MatchString(out) {
		t.Errorf("no version line:\n%s", out)
	}
}

// L-P of #387: the bundle's path is renamed and replaced by a
// self-consistent fake right after the installer entered the folder (a
// parent folder that another account can write). The installer reads only
// the folder it entered: the fake never runs, and the proven plumb is
// pushed. A plain path read (the mutant) would take the fake.
func TestBundlePathSwappedAfterEntering(t *testing.T) {
	h := newHost(t)
	b := newBundle(t, "v0.1.0")
	evil := filepath.Join(t.TempDir(), "evil-ran")
	// A fake bundle: an evil plumb, and SHA256SUMS that matches it.
	fake := newBundle(t, "v0.1.0")
	fake.write(t, "plumb", []byte("#!/bin/sh\ntouch "+evil+"\necho plumb v6.6.6\n"))
	fake.writeSums(t, fake.sums(t, "plumb", "plumb-agent", "install.sh"))
	hook := "mv " + b.dir + " " + b.dir + ".orig && mv " + fake.dir + " " + b.dir
	code, out, errOut := h.runBundle(b, []string{"PLUMB_INSTALL_ASSUME_ROOT=1", "PLUMB_INSTALL_BUNDLE_HOOK=" + hook},
		"--yes", "--plumb-binary", filepath.Join(b.dir, "plumb"))
	if _, err := os.Stat(b.dir + ".orig"); err != nil {
		t.Fatalf("the hook did not swap the folder: %v", err)
	}
	// The given --plumb-binary path now names the fake's plumb: refused,
	// because it is not the file of the folder that was checked.
	if code == 0 || !strings.Contains(errOut, "is not the plumb of this bundle") {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(evil); err == nil {
		t.Error("the fake plumb ran")
	}
	if _, err := os.Stat(filepath.Join(h.state, "push", "_usr_local_bin_plumb")); err == nil {
		t.Error("a plumb was installed")
	}
}

// The same swap, with --plumb-binary given as a hard link to the bundle's
// plumb (the same file, so the inode check passes): the run must still
// use only the folder it entered, so the proven plumb is pushed and the
// fake never runs. A read by the full path (the mutant) takes the fake.
func TestBundlePathSwappedHardLink(t *testing.T) {
	h := newHost(t)
	b := newBundle(t, "v0.1.0")
	proven := fileSum(t, filepath.Join(b.dir, "plumb"))
	link := filepath.Join(t.TempDir(), "plumb")
	if err := os.Link(filepath.Join(b.dir, "plumb"), link); err != nil {
		t.Skip("no hard link here: ", err)
	}
	evil := filepath.Join(t.TempDir(), "evil-ran")
	fake := newBundle(t, "v0.1.0")
	fake.write(t, "plumb", []byte("#!/bin/sh\ntouch "+evil+"\necho plumb v6.6.6\n"))
	fake.writeSums(t, fake.sums(t, "plumb", "plumb-agent", "install.sh"))
	hook := "mv " + b.dir + " " + b.dir + ".orig && mv " + fake.dir + " " + b.dir
	code, out, errOut := h.runBundle(b, []string{"PLUMB_INSTALL_ASSUME_ROOT=1", "PLUMB_INSTALL_BUNDLE_HOOK=" + hook}, "--yes", "--plumb-binary", link)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(evil); err == nil {
		t.Error("the fake plumb ran")
	}
	if !strings.Contains(out, "plumb version: none -> v0.1.0") {
		t.Errorf("not the proven plumb:\n%s", out)
	}
	if got := fileSum(t, filepath.Join(h.state, "push", "_usr_local_bin_plumb")); got != proven {
		t.Error("the pushed plumb is not the proven one")
	}
}
