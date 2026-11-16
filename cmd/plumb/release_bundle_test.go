package main

// T-261 (release plan C4): scripts/check-release-bundle.sh refuses every
// way a release tarball can be wrong. The tarballs here are made by hand,
// so a test can hold a link, a folder or an extra member.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

type member struct {
	name string
	data []byte
	typ  byte // tar.TypeReg if 0
	link string
}

func writeTarball(t *testing.T, ms []member) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range ms {
		typ := m.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: m.name, Mode: 0o755, Typeflag: typ, Size: int64(len(m.data)), Linkname: m.link}
		if typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			tw.Write(m.data)
		}
	}
	tw.Close()
	gz.Close()
	p := filepath.Join(t.TempDir(), "plumb-v0.1.0-linux-amd64.tar.gz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func sumsOf(files map[string][]byte, names ...string) []byte {
	var b strings.Builder
	for _, n := range names {
		s := sha256.Sum256(files[n])
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(s[:]), n)
	}
	return []byte(b.String())
}

// goodFiles are the files of a right bundle: the repo's installer stamped
// with v0.1.0, two plain binaries, and their sums.
func goodFiles(t *testing.T) map[string][]byte {
	t.Helper()
	inst := []byte(repoFile(t, "install/install.sh"))
	stamped := regexp.MustCompile(`(?m)^RELEASE_VERSION=""$`).ReplaceAll(inst, []byte(`RELEASE_VERSION="v0.1.0"`))
	if bytes.Equal(stamped, inst) {
		t.Fatal("install.sh has no RELEASE_VERSION line")
	}
	f := map[string][]byte{"plumb": []byte("plumb binary\n"), "plumb-agent": []byte("agent binary\n"), "install.sh": stamped}
	f["SHA256SUMS"] = sumsOf(f, "plumb", "plumb-agent", "install.sh")
	return f
}

func members(f map[string][]byte) []member {
	var ms []member
	for _, n := range []string{"plumb", "plumb-agent", "install.sh", "SHA256SUMS"} {
		ms = append(ms, member{name: n, data: f[n]})
	}
	return ms
}

func runCheck(t *testing.T, tarball, webDist string) (int, string) {
	t.Helper()
	cmd := exec.Command("bash", "scripts/check-release-bundle.sh", tarball, webDist)
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	} else if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

func testKeyLine(t *testing.T) string {
	t.Helper()
	for _, l := range strings.Split(repoFile(t, "internal/trust/testdata/f5/test-key.asc"), "\n") {
		if regexp.MustCompile(`^[A-Za-z0-9+/=]{40,}$`).MatchString(strings.TrimSpace(l)) {
			return strings.TrimSpace(l)
		}
	}
	t.Fatal("no body line in the test key")
	return ""
}

func TestCheckReleaseBundle(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	noDist := filepath.Join(t.TempDir(), "no-dist")
	if code, out := runCheck(t, writeTarball(t, members(goodFiles(t))), noDist); code != 0 || !strings.Contains(out, `check-release-bundle: ok (RELEASE_VERSION="v0.1.0"`) {
		t.Fatalf("a right bundle: %d %s", code, out)
	}
	for name, tc := range map[string]struct {
		make func(t *testing.T) []member
		dist func(t *testing.T) string
		want string
	}{
		"an extra member": {make: func(t *testing.T) []member {
			return append(members(goodFiles(t)), member{name: "evil", data: []byte("x")})
		}, want: "not exactly plumb"},
		"a missing member": {make: func(t *testing.T) []member { return members(goodFiles(t))[:3] }, want: "not exactly plumb"},
		"a member in a folder": {make: func(t *testing.T) []member {
			ms := members(goodFiles(t))
			ms[0].name = "x/plumb"
			return ms
		}, want: "not exactly plumb"},
		"a link member": {make: func(t *testing.T) []member {
			ms := members(goodFiles(t))
			ms[1] = member{name: "plumb-agent", typ: tar.TypeSymlink, link: "/usr/bin/true"}
			return ms
		}, want: "not a regular file"},
		"CRLF sums": {make: func(t *testing.T) []member {
			f := goodFiles(t)
			f["SHA256SUMS"] = bytes.ReplaceAll(f["SHA256SUMS"], []byte("\n"), []byte("\r\n"))
			return members(f)
		}, want: "carriage return"},
		"a line twice": {make: func(t *testing.T) []member {
			f := goodFiles(t)
			f["SHA256SUMS"] = append(f["SHA256SUMS"], sumsOf(f, "plumb")...)
			return members(f)
		}, want: "lists plumb twice"},
		"a sum missing": {make: func(t *testing.T) []member {
			f := goodFiles(t)
			f["SHA256SUMS"] = sumsOf(f, "plumb", "install.sh")
			return members(f)
		}, want: "has 2 lines, not 3"},
		"a path in the sums": {make: func(t *testing.T) []member {
			f := goodFiles(t)
			f["SHA256SUMS"] = bytes.Replace(f["SHA256SUMS"], []byte("  plumb\n"), []byte("  ./plumb\n"), 1)
			return members(f)
		}, want: "is not \"<sha256>  <file>\""},
		"a wrong sum": {make: func(t *testing.T) []member {
			f := goodFiles(t)
			f["plumb"] = []byte("changed\n")
			return members(f)
		}, want: "does not match the files"},
		"not stamped": {make: func(t *testing.T) []member {
			f := goodFiles(t)
			f["install.sh"] = []byte(repoFile(t, "install/install.sh"))
			f["SHA256SUMS"] = sumsOf(f, "plumb", "plumb-agent", "install.sh")
			return members(f)
		}, want: "is not stamped"},
		"install.sh changed beyond the stamp": {make: func(t *testing.T) []member {
			f := goodFiles(t)
			f["install.sh"] = append(f["install.sh"], []byte("bash -c \"$PLUMB_INSTALL_HOOK\"\n")...)
			f["SHA256SUMS"] = sumsOf(f, "plumb", "plumb-agent", "install.sh")
			return members(f)
		}, want: "differs from install/install.sh beyond the stamp"},
		"the test key in plumb": {make: func(t *testing.T) []member {
			f := goodFiles(t)
			f["plumb"] = []byte("x" + testKeyLine(t) + "\n")
			f["SHA256SUMS"] = sumsOf(f, "plumb", "plumb-agent", "install.sh")
			return members(f)
		}, want: "holds the D-369 test key"},
		"a source map in plumb-agent": {make: func(t *testing.T) []member {
			f := goodFiles(t)
			f["plumb-agent"] = []byte("//# sourceMappingURL=app.js.map\n")
			f["SHA256SUMS"] = sumsOf(f, "plumb", "plumb-agent", "install.sh")
			return members(f)
		}, want: "holds a sourceMappingURL"},
		"a .map file in web/dist": {make: func(t *testing.T) []member { return members(goodFiles(t)) }, dist: func(t *testing.T) string {
			d := t.TempDir()
			os.MkdirAll(filepath.Join(d, "assets"), 0o755)
			os.WriteFile(filepath.Join(d, "assets", "index.js.map"), []byte("{}"), 0o644)
			return d
		}, want: ".map file"},
	} {
		t.Run(name, func(t *testing.T) {
			dist := noDist
			if tc.dist != nil {
				dist = tc.dist(t)
			}
			if code, out := runCheck(t, writeTarball(t, tc.make(t)), dist); code == 0 || !strings.Contains(out, tc.want) {
				t.Errorf("%d, want %q: %s", code, tc.want, out)
			}
		})
	}
}
