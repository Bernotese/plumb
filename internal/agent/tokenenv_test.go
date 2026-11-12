package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

const envTok = "tok-secret-1"

func environFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "environ")
	if err := os.WriteFile(p, []byte(body), 0o400); err != nil {
		t.Fatal(err)
	}
	return p
}

// T-078, D-231: the token of a container comes from the environment of
// PID 1, a NUL list.
func TestEnvToken(t *testing.T) {
	const key = "PLUMB_ENROLL_TOKEN"
	ok := []struct{ name, body, want string }{
		{"only", key + "=" + envTok + "\x00", envTok},
		{"no trailing NUL", key + "=" + envTok, envTok},
		{"among others", "container=lxc\x00PATH=/bin\x00" + key + "=" + envTok + "\x00TERM=linux\x00", envTok},
		{"empty entries", "\x00\x00" + key + "=" + envTok + "\x00\x00", envTok},
		{"value with =", key + "=a=b\x00", "a=b"},
		{"first wins", key + "=" + envTok + "\x00" + key + "=other\x00", envTok},
		{"longer key first", key + "_OLD=x\x00" + key + "=" + envTok, envTok},
		{"spaces trimmed", key + "= " + envTok + "\n\x00", envTok},
	}
	for _, c := range ok {
		got, err := envToken(environFile(t, c.body), key)
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v", c.name, got, err)
		}
	}
	bad := []struct{ name, body, text string }{
		{"missing", "container=lxc\x00PATH=/bin\x00", "no enrollment token yet"},
		{"empty file", "", "no enrollment token yet"},
		{"empty value", key + "=\x00", "no enrollment token yet"},
		{"empty first, value later", key + "=\x00" + key + "=" + envTok, "no enrollment token yet"},
		{"key without =", key + "\x00", "no enrollment token yet"},
		{"other key only", key + "_OLD=" + envTok + "\x00", "no enrollment token yet"},
		{"lower case key", strings.ToLower(key) + "=" + envTok, "no enrollment token yet"},
		{"too long", key + "=" + envTok + "\x00X=" + strings.Repeat("a", maxEnviron), "larger than 64 KiB"},
	}
	for _, c := range bad {
		_, err := envToken(environFile(t, c.body), key)
		if err == nil || !strings.Contains(err.Error(), c.text) {
			t.Errorf("%s: err %v, want %q", c.name, err, c.text)
			continue
		}
		if strings.Contains(err.Error(), envTok) {
			t.Errorf("%s: the error holds the token", c.name)
		}
	}
	// Exactly 64 KiB is read.
	body := key + "=" + envTok + "\x00X="
	body += strings.Repeat("a", maxEnviron-len(body))
	if got, err := envToken(environFile(t, body), key); err != nil || got != envTok {
		t.Errorf("64 KiB: %q %v", got, err)
	}
	if _, err := envToken(filepath.Join(t.TempDir(), "nope"), key); err == nil {
		t.Error("missing file")
	}
}

// Keep the token at the end so a truncated read cannot pass the size check.
func TestEnvTokenBoundaries(t *testing.T) {
	const key = "PLUMB_ENROLL_TOKEN"
	const secret = "qa-private-token"
	entry := key + "=" + secret
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"last entry without NUL", "OTHER=private-other\x00" + entry, false},
		{"empty without NUL", key + "=", true},
		{"whitespace first duplicate", key + "= \t\n\x00" + entry, true},
		{"first duplicate without final NUL", entry + "\x00" + key + "=second-private-token", false},
		{"exact cap without NUL", "X=" + strings.Repeat("x", maxEnviron-len(entry)-3) + "\x00" + entry, false},
		{"one byte over cap", "X=" + strings.Repeat("x", maxEnviron-len(entry)-2) + "\x00" + entry, true},
		{"token before oversized tail", entry + "\x00X=" + strings.Repeat("x", maxEnviron), true},
		{"missing with private values", "OTHER=private-other\x00" + key + "_OLD=" + secret, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := envToken(environFile(t, tc.body), key)
			if tc.wantError {
				if err == nil {
					t.Fatal("expected an error")
				}
				if got != "" {
					t.Error("error returned a token")
				}
				for _, value := range []string{secret, "private-other", "second-private-token"} {
					if strings.Contains(err.Error(), value) {
						t.Error("error contains a private environment value")
					}
				}
			} else if err != nil || got != secret {
				t.Error("expected the first token without an error")
			}
		})
	}
}

func TestConfigTokenEnv(t *testing.T) {
	ca := newCA(t)
	base := Config{URL: "https://10.0.0.5:8444", CA: ca.pem}
	c := base
	c.TokenEnv = "PLUMB_ENROLL_TOKEN"
	if err := c.check(); err != nil || c.TokenFile != "" {
		t.Errorf("token_env: %v, token_file %q", err, c.TokenFile)
	}
	for _, name := range []string{"1X", "A-B", "A B", "A=B", strings.Repeat("A", 129)} {
		c := base
		c.TokenEnv = name
		if err := c.check(); err == nil {
			t.Errorf("token_env %q accepted", name)
		}
	}
	c = base
	c.TokenEnv, c.TokenFile = "X", "/x"
	if err := c.check(); err == nil || !strings.Contains(err.Error(), "exclude") {
		t.Errorf("both: %v", err)
	}
	c = base
	if err := c.check(); err != nil || c.TokenFile != DefaultTokenFile {
		t.Errorf("default: %v %q", err, c.TokenFile)
	}
}

// A container agent enrolls with the token of PID 1, retries while it is
// missing, and does not touch cloud-init.
func TestEnrollWithTokenEnv(t *testing.T) {
	env := environFile(t, "container=lxc\x00")
	os.Chmod(env, 0o600)
	old := pid1Environ
	pid1Environ = env
	t.Cleanup(func() { pid1Environ = old })

	ca := newCA(t)
	ctl := newController(t, ca, envTok)
	ta := startAgentWith(t, ctl, ca, "unused", func(o *Options) {
		o.Config.TokenFile, o.Config.TokenEnv = "", "PLUMB_ENROLL_TOKEN"
	})
	time.Sleep(100 * time.Millisecond)
	if n := len(ctl.reportsOf(wire.ReportHello)); n != 0 {
		t.Fatal("hello without a token")
	}
	if err := os.WriteFile(env, []byte("container=lxc\x00PLUMB_ENROLL_TOKEN="+envTok+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
	ctl.mu.Lock()
	enrolled := append([]string(nil), ctl.enrolled...)
	ctl.mu.Unlock()
	if len(enrolled) != 1 || enrolled[0] != envTok {
		t.Fatalf("enrolled with %v", enrolled)
	}
	if _, err := os.Stat(filepath.Join(ta.root, "etc/cloud")); err == nil {
		t.Error("a container agent made /etc/cloud")
	}
}
