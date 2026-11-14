package agentinstall

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

const testCA = "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUTEST\n-----END CERTIFICATE-----\n"

func testBundle() Bundle {
	return Bundle{Agent: []byte("#!/bin/sh\necho agent\n"), CA: []byte(testCA),
		URL: "https://192.0.2.10:8444", Token: "AbC-_123xyzAbC-_123xyzAbC-_123xyzAbC-_123x"}
}

func readMembers(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(b))
	out := map[string][]byte{}
	var names []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg || h.Format != tar.FormatUSTAR && h.Format != tar.FormatUnknown {
			t.Fatalf("member %s: type %c format %v", h.Name, h.Typeflag, h.Format)
		}
		body, _ := io.ReadAll(tr)
		out[h.Name] = body
		names = append(names, h.Name)
	}
	if !slices.Equal(names, wire.RepairMembers) {
		t.Fatalf("members %v, want exactly %v", names, wire.RepairMembers)
	}
	return out
}

// TestRepairBundleFixedMembers: the bundle members are exactly the five
// names (ARCHITECTURE §19, "repair is a fixed action set").
func TestRepairBundleFixedMembers(t *testing.T) {
	in := testBundle()
	var buf bytes.Buffer
	if err := WriteBundle(&buf, in); err != nil {
		t.Fatal(err)
	}
	m := readMembers(t, buf.Bytes())
	if !bytes.Equal(m["install.sh"], Script()) || !bytes.Equal(m["plumb-agent"], in.Agent) {
		t.Fatal("install.sh or plumb-agent differs from the input")
	}
	sum := sha256.Sum256(in.Agent)
	if string(m["plumb-agent.sha256"]) != hex.EncodeToString(sum[:])+"  plumb-agent\n" {
		t.Fatalf("sha line %q", m["plumb-agent.sha256"])
	}
	var e wire.RepairEnroll
	if err := json.Unmarshal(m["enroll.json"], &e); err != nil || e != (wire.RepairEnroll{URL: in.URL, Token: in.Token}) {
		t.Fatalf("enroll.json %q: %v", m["enroll.json"], err)
	}
	// The same input gives the same bytes.
	var again bytes.Buffer
	if err := WriteBundle(&again, in); err != nil || !bytes.Equal(again.Bytes(), buf.Bytes()) {
		t.Fatal("the bundle is not deterministic")
	}
}

func TestRepairBundleRefusesBadInput(t *testing.T) {
	for name, mod := range map[string]func(*Bundle){
		"no agent":     func(b *Bundle) { b.Agent = nil },
		"http":         func(b *Bundle) { b.URL = "http://192.0.2.10:8444" },
		"path":         func(b *Bundle) { b.URL = "https://192.0.2.10:8444/x" },
		"quote in url": func(b *Bundle) { b.URL = `https://a"b:8444` },
		"token space":  func(b *Bundle) { b.Token = "abc def" },
		"token quote":  func(b *Bundle) { b.Token = `abc"` },
		"empty token":  func(b *Bundle) { b.Token = "" },
		"ca not pem":   func(b *Bundle) { b.CA = []byte("x") },
		"ca quote":     func(b *Bundle) { b.CA = []byte(testCA + `"`) },
		"too big":      func(b *Bundle) { b.Agent = make([]byte, wire.MaxRepairBundle) },
	} {
		b := testBundle()
		mod(&b)
		if err := WriteBundle(io.Discard, b); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func need(t *testing.T, tools ...string) {
	t.Helper()
	for _, x := range tools {
		if _, err := exec.LookPath(x); err != nil {
			t.Skipf("%s not installed", x)
		}
	}
}

// sandbox rewrites the absolute paths of the script into root, and
// puts fake systemctl and curl first in PATH. The fakes log their
// arguments.
func sandbox(t *testing.T, s string) (string, string, []string) {
	t.Helper()
	root := t.TempDir()
	fake := filepath.Join(root, "fakebin")
	for _, d := range []string{fake, "etc/plumb-agent", "usr/local/bin", "etc/systemd/system"} {
		if err := os.MkdirAll(filepath.Join(root, strings.TrimPrefix(d, root)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(root, "calls.log")
	for _, tool := range []string{"systemctl", "curl"} {
		body := "#!/bin/sh\necho \"" + tool + " $*\" >>" + log + "\n"
		if tool == "systemctl" { // FAKE_FAIL=restart fails "systemctl restart"
			body += "if [ \"${FAKE_FAIL:-}\" = \"$1\" ]; then exit 1; fi\n"
		}
		if tool == "curl" { // write the "binary" to the -o file
			body += "while [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then echo fetched >\"$2\"; fi; shift; done\n"
		}
		if err := os.WriteFile(filepath.Join(fake, tool), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s = strings.ReplaceAll(s, "PATH=/usr/sbin:/usr/bin:/sbin:/bin", "PATH="+fake+":/usr/sbin:/usr/bin:/sbin:/bin")
	s = strings.ReplaceAll(s, "/etc/", root+"/etc/")
	s = strings.ReplaceAll(s, "/usr/local/bin/", root+"/usr/local/bin/")
	return s, root, []string{"PATH=" + os.Getenv("PATH")}
}

func calls(t *testing.T, root string) string {
	b, _ := os.ReadFile(filepath.Join(root, "calls.log"))
	return string(b)
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// TestRepairForcedCommandInstalls runs the forced command of the repair
// line on a real bundle (GNU tar, sha256sum), with the paths moved into
// a temp dir: the agent, the CA, agent.json and the one-shot token land
// with their modes, and the unit is unmasked, enabled and restarted.
func TestRepairForcedCommandInstalls(t *testing.T) {
	need(t, "tar", "sha256sum", "head", "awk", "sed")
	s, root, env := sandbox(t, string(script))
	old := script
	script = []byte(s)
	defer func() { script = old }()
	// An LXC agent.json keeps its token_env and the other keys of the
	// agent config (review n2 of #295).
	if err := os.WriteFile(filepath.Join(root, "etc/plumb-agent/agent.json"),
		[]byte(`{"url":"https://old:1","ca":"x","token_env":"PLUMB_ENROLL_TOKEN", "state_dir": "/srv/plumb-state","full_check":"5m0s"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := testBundle()
	var buf bytes.Buffer
	if err := WriteBundle(&buf, in); err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(root, "run")
	forced := strings.ReplaceAll(wire.RepairForcedCommand, "/run/plumb-repair", run)
	cmd := exec.Command("sh", "-c", forced)
	cmd.Stdin, cmd.Env = &buf, env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("forced command: %v\n%s", err, out)
	}
	if strings.Contains(string(out), in.Token) {
		t.Fatal("the output holds the token")
	}
	etc := filepath.Join(root, "etc/plumb-agent")
	tok, err := os.ReadFile(filepath.Join(etc, "enroll.token"))
	if err != nil || string(tok) != in.Token+"\n" || mode(t, filepath.Join(etc, "enroll.token")) != 0o600 {
		t.Fatalf("enroll.token %q %v", tok, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "usr/local/bin/plumb-agent")); !bytes.Equal(b, in.Agent) {
		t.Fatal("agent binary not installed")
	}
	if mode(t, filepath.Join(root, "usr/local/bin/plumb-agent")) != 0o755 || mode(t, filepath.Join(etc, "ca.pem")) != 0o644 {
		t.Fatal("modes")
	}
	var cfg struct {
		URL       string `json:"url"`
		CA        string `json:"ca"`
		TokenEnv  string `json:"token_env"`
		StateDir  string `json:"state_dir"`
		FullCheck string `json:"full_check"`
	}
	b, _ := os.ReadFile(filepath.Join(etc, "agent.json"))
	if err := json.Unmarshal(b, &cfg); err != nil || cfg.URL != in.URL || cfg.CA != testCA || cfg.TokenEnv != "PLUMB_ENROLL_TOKEN" ||
		cfg.StateDir != "/srv/plumb-state" || cfg.FullCheck != "5m0s" {
		t.Fatalf("agent.json %s: %v", b, err)
	}
	if strings.Contains(string(b), "token_file") || strings.Contains(string(b), in.Token) {
		t.Fatalf("agent.json names a token: %s", b)
	}
	want := "systemctl unmask plumb-agent.service\nsystemctl daemon-reload\nsystemctl enable plumb-agent.service\nsystemctl restart plumb-agent.service\n"
	if c := calls(t, root); c != want {
		t.Fatalf("calls:\n%s", c)
	}
	if _, err := os.Stat(run); !os.IsNotExist(err) {
		t.Fatal("the bundle dir stays")
	}
}

// TestRepairForcedCommandRefusesBadBinary: a binary that does not match
// its sha line never runs install.sh.
func TestRepairForcedCommandRefusesBadBinary(t *testing.T) {
	need(t, "tar", "sha256sum", "head")
	s, root, env := sandbox(t, string(script))
	old := script
	script = []byte(s)
	defer func() { script = old }()
	var buf bytes.Buffer
	if err := WriteBundle(&buf, testBundle()); err != nil {
		t.Fatal(err)
	}
	// Flip one byte of the agent body inside the archive.
	b := buf.Bytes()
	i := bytes.Index(b, []byte("echo agent"))
	b[i] = 'E'
	cmd := exec.Command("sh", "-c", strings.ReplaceAll(wire.RepairForcedCommand, "/run/plumb-repair", filepath.Join(root, "run")))
	cmd.Stdin, cmd.Env = bytes.NewReader(b), env
	if err := cmd.Run(); err == nil {
		t.Fatal("a wrong sha passed")
	}
	if calls(t, root) != "" {
		t.Fatal("install.sh ran")
	}
	if _, err := os.Stat(filepath.Join(root, "etc/plumb-agent/enroll.token")); !os.IsNotExist(err) {
		t.Fatal("token written")
	}
}

// TestInstallCommandMode: with PLUMB_URL the script fetches the binary
// with the CA and keeps the token file the command wrote.
func TestInstallCommandMode(t *testing.T) {
	need(t, "awk", "sed", "mktemp")
	s, root, env := sandbox(t, string(script))
	etc := filepath.Join(root, "etc/plumb-agent")
	os.WriteFile(filepath.Join(etc, "ca.pem"), []byte(testCA), 0o600)
	// A long token with '-' and '_': a short needle like "tok" can occur
	// by chance in a random mktemp name in the calls log (T-213 family).
	const token = "plumb_enroll-secret-for-the-mode-test"
	os.WriteFile(filepath.Join(etc, "enroll.token"), []byte(token+"\n"), 0o600)
	cmd := exec.Command("sh", "-c", s)
	cmd.Dir = t.TempDir()
	cmd.Env = append(env, "PLUMB_URL=https://192.0.2.10:8444")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	c := calls(t, root)
	if !strings.Contains(c, "curl -fsS --proto =https") || !strings.Contains(c, "--cacert "+etc+"/ca.pem") ||
		!strings.Contains(c, "https://192.0.2.10:8444/agent/v1/binary") || !strings.Contains(c, "systemctl restart plumb-agent.service") {
		t.Fatalf("calls:\n%s", c)
	}
	if strings.Contains(c, token) {
		t.Fatal("the token is on a command line")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "usr/local/bin/plumb-agent")); string(b) != "fetched\n" {
		t.Fatalf("binary %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(etc, "agent.json")); strings.Contains(string(b), "token_env") {
		t.Fatalf("agent.json %s", b)
	}
}

func TestInstallScriptRefusesBadURL(t *testing.T) {
	s, _, env := sandbox(t, string(script))
	for _, u := range []string{"http://x:1", "https://x:1/y", "https://x y:1", "https://x';id;'"} {
		cmd := exec.Command("sh", "-c", s)
		cmd.Env = append(env, "PLUMB_URL="+u)
		if err := cmd.Run(); err == nil {
			t.Errorf("%q passed", u)
		}
	}
}

// TestInstallScriptFailureRemovesToken (review F1 of #295): when the
// script fails after the token file was written (here: the restart), the
// file goes; in both modes.
func TestInstallScriptFailureRemovesToken(t *testing.T) {
	need(t, "tar", "sha256sum", "head", "awk", "sed", "mktemp")
	for _, mode := range []string{"repair", "install-command"} {
		t.Run(mode, func(t *testing.T) {
			s, root, env := sandbox(t, string(script))
			etc := filepath.Join(root, "etc/plumb-agent")
			env = append(env, "FAKE_FAIL=restart")
			var cmd *exec.Cmd
			if mode == "repair" {
				old := script
				script = []byte(s)
				defer func() { script = old }()
				var buf bytes.Buffer
				if err := WriteBundle(&buf, testBundle()); err != nil {
					t.Fatal(err)
				}
				cmd = exec.Command("sh", "-c", strings.ReplaceAll(wire.RepairForcedCommand, "/run/plumb-repair", filepath.Join(root, "run")))
				cmd.Stdin = &buf
			} else {
				os.WriteFile(filepath.Join(etc, "ca.pem"), []byte(testCA), 0o600)
				os.WriteFile(filepath.Join(etc, "enroll.token"), []byte("tok\n"), 0o600)
				cmd = exec.Command("sh", "-c", s)
				cmd.Dir = t.TempDir()
				env = append(env, "PLUMB_URL=https://192.0.2.10:8444")
			}
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("a failed restart passed:\n%s", out)
			}
			if !strings.Contains(calls(t, root), "systemctl restart") {
				t.Fatal("the script stopped before the restart")
			}
			if _, err := os.Stat(filepath.Join(etc, "enroll.token")); !os.IsNotExist(err) {
				t.Fatalf("enroll.token stays after a failure: %v", err)
			}
		})
	}
}

// TestInstallScriptTruncatedRunsNothing: a script that a broken download
// cut at any line does nothing (the body runs only from the last line).
func TestInstallScriptTruncatedRunsNothing(t *testing.T) {
	need(t, "awk", "sed", "mktemp")
	full, _, _ := sandbox(t, string(script))
	lines := strings.SplitAfter(full, "\n")
	for cut := 1; cut < len(lines)-1; cut++ {
		part := strings.Join(lines[:cut], "")
		if strings.TrimSpace(part) == strings.TrimSpace(full) {
			continue
		}
		s, root, env := sandbox(t, string(script))
		part = strings.Join(strings.SplitAfter(s, "\n")[:cut], "")
		etc := filepath.Join(root, "etc/plumb-agent")
		os.WriteFile(filepath.Join(etc, "ca.pem"), []byte(testCA), 0o600)
		os.WriteFile(filepath.Join(etc, "enroll.token"), []byte("tok\n"), 0o600)
		cmd := exec.Command("sh", "-c", part)
		cmd.Dir = t.TempDir()
		cmd.Env = append(env, "PLUMB_URL=https://192.0.2.10:8444")
		cmd.Run()
		if c := calls(t, root); c != "" {
			t.Fatalf("cut at line %d ran:\n%s", cut, c)
		}
		if _, err := os.Stat(filepath.Join(root, "usr/local/bin/plumb-agent")); err == nil {
			t.Fatalf("cut at line %d installed the binary", cut)
		}
	}
}
