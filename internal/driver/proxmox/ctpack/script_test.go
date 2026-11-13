package ctpack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runScript runs the first-boot script with /usr/local/bin moved to a temp
// dir and fake systemctl, curl and (when failInstall) install commands.
func runScript(t *testing.T, failInstall bool) (bin string, out []byte, err error) {
	t.Helper()
	root := t.TempDir()
	bin = filepath.Join(root, "bin")
	fake := filepath.Join(root, "fake")
	os.MkdirAll(bin, 0o755)
	os.MkdirAll(fake, 0o755)
	w := func(name, body string) {
		if err := os.WriteFile(filepath.Join(fake, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w("systemctl", `[ "$1" = is-active ] && exit 3; exit 0`+"\n")
	w("curl", `while [ $# -gt 0 ]; do [ "$1" = -o ] && { shift; printf 'agent-binary' >"$1"; }; shift; done`+"\n")
	if failInstall {
		// A full disk: half the file is written, then install fails.
		w("install", `eval "dst=\${$#}"; printf 'agent' >"$dst"; echo "install: No space left on device" >&2; exit 1`+"\n")
	}
	script := strings.ReplaceAll(FirstBootScript("https://192.0.2.5:8444"), "/usr/local/bin", bin)
	if strings.Count(script, bin) < 2 {
		t.Fatal("the script does not use /usr/local/bin")
	}
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+fake+":"+os.Getenv("PATH"))
	out, err = cmd.CombinedOutput()
	return bin, out, err
}

// Review S4 of T-038: a failed copy leaves no file under the real name and
// no temp file.
func TestFirstBootScriptFailedCopy(t *testing.T) {
	bin, out, err := runScript(t, true)
	if err == nil {
		t.Fatalf("script passed:\n%s", out)
	}
	ents, _ := os.ReadDir(bin)
	if len(ents) != 0 {
		t.Errorf("files left in /usr/local/bin: %v\n%s", ents, out)
	}
}

func TestFirstBootScriptInstalls(t *testing.T) {
	bin, out, err := runScript(t, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(bin, "plumb-agent"))
	if err != nil || string(b) != "agent-binary" {
		t.Fatalf("agent = %q %v", b, err)
	}
	if st, _ := os.Stat(filepath.Join(bin, "plumb-agent")); st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", st.Mode())
	}
	ents, _ := os.ReadDir(bin)
	if len(ents) != 1 {
		t.Errorf("files in /usr/local/bin: %v", ents)
	}
}

func TestFirstBootScriptShape(t *testing.T) {
	s := FirstBootScript("https://192.0.2.5:8444")
	for _, want := range []string{"url='https://192.0.2.5:8444/agent/v1/binary'", "--cacert", "is-active --quiet plumb-agent.service"} {
		if !strings.Contains(s, want) {
			t.Errorf("script misses %q", want)
		}
	}
	if strings.Contains(s, "@URL@") || strings.Contains(s, "--insecure") || strings.Contains(s, "-k ") {
		t.Error("script")
	}
	sh := exec.Command("sh", "-n")
	sh.Stdin = strings.NewReader(s)
	if out, err := sh.CombinedOutput(); err != nil {
		t.Errorf("not valid sh: %v %s", err, out)
	}
}
