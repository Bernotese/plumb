package main

// T-255 (release contract): one proposed release version. The file VERSION,
// info.version of api/openapi.yaml and the release script agree; the
// binaries get it by ldflags (package-release.sh checks their output). A
// build that is no release says "dev".

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReleaseVersionAgrees(t *testing.T) {
	want := strings.TrimSpace(repoFile(t, "VERSION"))
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(want) {
		t.Fatalf("VERSION %q is not MAJOR.MINOR.PATCH", want)
	}
	m := regexp.MustCompile(`(?m)^info:\n(?:  .*\n)*?  version: (\S+)$`).FindStringSubmatch(repoFile(t, "api/openapi.yaml"))
	got := ""
	if m != nil {
		got = m[1]
	}
	if got != want {
		t.Fatalf("api/openapi.yaml info.version %q, want %q (the file VERSION)", got, want)
	}
	// A build without ldflags, and a make build that is no release, say dev.
	if version != "dev" {
		t.Errorf("the default version is %q, want dev", version)
	}
	if !strings.Contains(repoFile(t, "Makefile"), "VERSION     ?= dev-") {
		t.Error("make build without VERSION must say dev-<commit>")
	}
}

// The release script refuses a version that is not the one of VERSION,
// before it builds anything.
func TestPackageReleaseRefusesAnotherVersion(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	want := strings.TrimSpace(repoFile(t, "VERSION"))
	for _, v := range []string{"v9.9.9", "v" + want + "1", "v0.0.0-rc.1"} {
		if v == "v"+want {
			continue
		}
		cmd := exec.Command("bash", "scripts/package-release.sh", v, filepath.Join(t.TempDir(), "out"))
		cmd.Dir = filepath.Join("..", "..")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err == nil || !strings.Contains(out.String(), "does not match VERSION") {
			t.Errorf("%s: %v %q", v, err, out.String())
		}
	}
}

// T-255: the workflows pin the exact Go and Node versions, so that a
// release build equals the tested one (plan P3). Go is the toolchain of
// go.mod; every job of both workflows names the same versions.
func TestWorkflowsPinTheToolchain(t *testing.T) {
	m := regexp.MustCompile(`(?m)^toolchain go(\S+)$`).FindStringSubmatch(repoFile(t, "go.mod"))
	if m == nil {
		t.Fatal("go.mod has no toolchain line")
	}
	node := ""
	for _, wf := range []string{".github/workflows/ci.yml", ".github/workflows/release.yml"} {
		s := repoFile(t, wf)
		if strings.Contains(s, "go-version-file") || regexp.MustCompile(`node-version: [0-9]+\s`).MatchString(s) {
			t.Errorf("%s: a Go or Node version is not pinned", wf)
		}
		for _, g := range regexp.MustCompile(`go-version: "([^"]+)"`).FindAllStringSubmatch(s, -1) {
			if g[1] != m[1] {
				t.Errorf("%s: go-version %s, want the go.mod toolchain %s", wf, g[1], m[1])
			}
		}
		for _, n := range regexp.MustCompile(`node-version: "([^"]+)"`).FindAllStringSubmatch(s, -1) {
			if node == "" {
				node = n[1]
			}
			if n[1] != node {
				t.Errorf("%s: node-version %s, want %s in every job", wf, n[1], node)
			}
		}
	}
	if node == "" {
		t.Error("no pinned node-version")
	}
}
