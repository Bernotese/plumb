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
