package install

// T-260 (release plan C9): tests for the three survivors of pl-reviewer's
// own mutants of the merged bundle check (T-257): the SHA256SUMS line
// regex without its ^ or $ anchor, and the bundle without its owner check.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func refusedBoth(t *testing.T, h *host, b *bundle, env []string, want string) {
	t.Helper()
	for _, mode := range []string{"--dry-run", "--yes"} {
		code, out, errOut := h.runBundle(b, env, mode, "--plumb-binary", filepath.Join(b.dir, "plumb"))
		if code == 0 || !strings.Contains(errOut, want) || !strings.Contains(errOut, "Nothing was changed") {
			t.Fatalf("%s: %d, want %q\n%s\n%s", mode, code, want, out, errOut)
		}
		if strings.Contains(out, "plumb version:") {
			t.Errorf("%s: the binary was used before the refusal", mode)
		}
	}
	h.noHostChange(t)
}

// A SHA256SUMS line must be exactly "<sha256>  <name>": no text before the
// sum (mutant: no ^) and none after the name (mutant: no $).
func TestBundleSumsLineHasNoExtraText(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"text before the sum":  func(s string) string { return "x" + s },
		"a longer hex before":  func(s string) string { return "a" + s },
		"a name with a suffix": func(s string) string { return strings.Replace(s, "  plumb\n", "  plumb.bak\n", 1) },
		"a trailing space":     func(s string) string { return strings.Replace(s, "  plumb\n", "  plumb \n", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			b := newBundle(t, "v0.1.0")
			b.writeSums(t, edit(b.sums(t, "plumb", "plumb-agent", "install.sh")))
			refusedBoth(t, h, b, nil, "line 1 is not")
		})
	}
}

// A bundle owned by another account is refused even when nobody else may
// write it: its owner can chmod it and write (mutant: no owner check). The
// tests cannot chown, so an id shim reports another uid.
func TestBundleOwnedByAnotherAccount(t *testing.T) {
	h := newHost(t)
	b := newBundle(t, "v0.1.0")
	shim := t.TempDir()
	other := strconv.Itoa(os.Getuid() + 1)
	script := "#!/bin/bash\nif [[ $* == -u ]]; then echo " + other + "; exit 0; fi\nexec /usr/bin/id \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "id"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	path := ""
	for _, e := range h.env {
		if strings.HasPrefix(e, "PATH=") {
			path = strings.TrimPrefix(e, "PATH=")
		}
	}
	refusedBoth(t, h, b, []string{"PATH=" + shim + ":" + path}, "is not owned by this user")
}
