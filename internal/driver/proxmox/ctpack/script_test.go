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
	return runScriptWith(t, scriptCase{failInstall: failInstall})
}

// scriptCase: active makes the fake "systemctl is-active" say active (the
// unit's moment after its fork, T-278); binary puts an agent binary in
// place before the script runs; curls counts the downloads in a file.
type scriptCase struct {
	failInstall, active, binary bool
}

func runScriptWith(t *testing.T, c scriptCase) (bin string, out []byte, err error) {
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
	isActive := "3"
	if c.active {
		isActive = "0"
	}
	w("systemctl", `[ "$1" = is-active ] && exit `+isActive+"; exit 0\n")
	w("curl", `echo x >>"`+filepath.Join(root, "curls")+`"; while [ $# -gt 0 ]; do [ "$1" = -o ] && { shift; printf 'agent-binary' >"$1"; }; shift; done`+"\n")
	if c.binary {
		if err := os.WriteFile(filepath.Join(bin, "plumb-agent"), []byte("old-binary"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if c.failInstall {
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

// T-278 (host finding of T-277): at boot plumb-agent.service is "active"
// for a moment after its fork although its binary is missing. The script
// must not take that for an installed agent: it downloads the binary.
// Today's units failed here (the script exited 0 with no binary).
func TestFirstBootScriptUnitActiveWithoutBinary(t *testing.T) {
	bin, out, err := runScriptWith(t, scriptCase{active: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, err := os.ReadFile(filepath.Join(bin, "plumb-agent")); err != nil || string(b) != "agent-binary" {
		t.Fatalf("the script skipped the download while the unit looked active: agent = %q %v\n%s", b, err, out)
	}
}

// The other side of T-278: an installed agent that runs is left alone (no
// download at every boot); an installed agent that does not run gets the
// controller's binary again, as before.
func TestFirstBootScriptInstalledAgent(t *testing.T) {
	for _, c := range []struct {
		active    bool
		want      string
		downloads int
	}{{true, "old-binary", 0}, {false, "agent-binary", 1}} {
		bin, out, err := runScriptWith(t, scriptCase{active: c.active, binary: true})
		if err != nil {
			t.Fatalf("active=%v: %v\n%s", c.active, err, out)
		}
		b, _ := os.ReadFile(filepath.Join(bin, "plumb-agent"))
		curls, _ := os.ReadFile(filepath.Join(filepath.Dir(bin), "curls"))
		if string(b) != c.want || strings.Count(string(curls), "x") != c.downloads {
			t.Errorf("active=%v: agent %q after %d downloads, want %q after %d", c.active, b, strings.Count(string(curls), "x"), c.want, c.downloads)
		}
	}
}
