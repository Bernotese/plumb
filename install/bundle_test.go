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
	b.write(t, "plumb", []byte("#!/bin/sh\necho plumb "+version+"\n"))
	b.write(t, "plumb-agent", []byte("#!/bin/sh\necho plumb-agent "+version+"\n"))
	b.writeSums(t, b.sums(t, "plumb", "plumb-agent", "install.sh"))
	return b
}

func (b *bundle) write(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(b.dir, name), data, 0o755); err != nil {
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
	if err := os.WriteFile(filepath.Join(b.dir, "SHA256SUMS"), []byte(s), 0o644); err != nil {
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
			b.write(t, "plumb", []byte("#!/bin/sh\necho evil\n"))
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
		}, want: "is not a regular file"},
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
			for _, mode := range [][]string{{"--dry-run"}, {"--yes"}} {
				code, out, errOut := h.runBundle(b, nil, append(mode, "--plumb-binary", bin)...)
				if code == 0 || !strings.Contains(errOut, tc.want) || !strings.Contains(errOut, "Nothing was changed") {
					t.Fatalf("%v: %d, want %q\n%s\n%s", mode, code, tc.want, out, errOut)
				}
				if strings.Contains(out, "plumb version:") {
					t.Errorf("%v: the binary was used before the check failed:\n%s", mode, out)
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

// A plumb that is swapped after the check is never pushed (T-257).
func TestBundleSwapAfterCheck(t *testing.T) {
	h := newHost(t)
	b := newBundle(t, "v0.1.0")
	plumb := filepath.Join(b.dir, "plumb")
	swap := "printf '#!/bin/sh\\necho swapped\\n' > " + plumb
	code, out, errOut := h.runBundle(b, []string{"PLUMB_INSTALL_ASSUME_ROOT=1", "FAKE_VERSION_HOOK=" + swap}, "--yes", "--plumb-binary", plumb)
	if code == 0 || !strings.Contains(errOut, "changed after the bundle check; it is refused") {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	for _, c := range h.calls() {
		if strings.HasPrefix(c, "pct exec") && strings.Contains(c, "mv -f /usr/local/bin/.plumb.plumb-new /usr/local/bin/plumb") {
			t.Errorf("the swapped plumb was put in place: %s", c)
		}
	}
	if _, err := os.Stat(filepath.Join(h.state, "push", "_usr_local_bin_plumb")); err == nil {
		t.Error("a plumb was installed")
	}
}
