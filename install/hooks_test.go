package install

// B1 of #387: the shipped installer runs as root, and sudo -E, env_keep or
// a wrapper can hand it the caller's environment. So install.sh reads only
// the environment variables of a fixed list (each one only points the run
// at test files or changes a wait; none runs caller text), and it runs no
// text from outside (no eval, no "bash -c"/"sh -c" of an outside value, no
// source). A new hook makes this test fail: add it to the list only with a
// review of what it can do in a root run.

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// envAllowed are the environment variables that install.sh may read.
var envAllowed = map[string]bool{
	"PLUMB_INSTALL_ASSUME_ROOT":   true, // skip the root check: a no-op for root
	"PLUMB_INSTALL_ASSUME_TTY":    true, // skip the terminal check; plumb key init still refuses
	"PLUMB_INSTALL_PVE_CA":        true, // a file instead of /etc/pve/pve-root-ca.pem
	"PLUMB_INSTALL_STATE":         true, // a file instead of /etc/plumb/install.state
	"PLUMB_INSTALL_LXC_DIR":       true, // a folder instead of /var/lib/lxc
	"PLUMB_INSTALL_RUNNING_TRIES": true, // a number
	"PLUMB_INSTALL_RUNNING_WAIT":  true, // seconds
	"PLUMB_INSTALL_STAY_WAIT":     true, // seconds
	"PLUMB_INSTALL_RESOLV":        true, // a file instead of /etc/resolv.conf
	"PLUMB_INSTALL_SBIN":          true, // a folder instead of /usr/local/sbin
	"PLUMB_INSTALL_UNIT_DIR":      true, // a folder instead of /etc/systemd/system
	"PLUMB_INSTALL_COPY_CONF":     true, // a file instead of /etc/plumb/backup-copy.conf
	"PLUMB_INSTALL_PVE_MNT":       true, // a folder instead of /mnt/pve
}

func TestInstallerReadsOnlyListedEnvironment(t *testing.T) {
	b, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	// A name that the script sets at the top level, in a function (local,
	// read, for, printf -v) or as an assignment is its own variable.
	assigned := map[string]bool{}
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)(?:^|\s)([A-Z][A-Z0-9_]*)(?:\[[^]]*\])?\+?=`),
		regexp.MustCompile(`(?m)\b(?:read\s+(?:-[a-z]+\s+)*|for\s+|printf\s+-v\s+"?)([A-Z][A-Z0-9_]*)\b`),
		regexp.MustCompile(`(?m)^\s*(?:local|declare)\s+(?:-[a-zA-Z]+\s+)*([A-Z][A-Z0-9_]*(?:\s+[A-Z][A-Z0-9_]*)*)\s*$`),
	} {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			for _, n := range strings.Fields(m[1]) {
				assigned[n] = true
			}
		}
	}
	// Variables of bash and of the system that the script may read.
	for _, n := range []string{"EUID", "PWD", "OLDPWD", "BASH_SOURCE", "BASH_REMATCH", "HOME", "PATH", "PIPESTATUS", "LINENO", "SECONDS", "RANDOM", "IFS"} {
		assigned[n] = true
	}
	var unlisted []string
	seen := map[string]bool{}
	// Every $NAME or ${NAME...} outside of single-quoted perl or awk code.
	code := shellCode(src)
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)(?:^|\s)([A-Z][A-Z0-9_]*)(?:\[[^]]*\])?\+?=`),
	} {
		for _, m := range re.FindAllStringSubmatch(code, -1) {
			assigned[m[1]] = true
		}
	}
	for _, m := range regexp.MustCompile(`\$\{?([A-Z][A-Z0-9_]*)`).FindAllStringSubmatch(code, -1) {
		n := m[1]
		if assigned[n] || envAllowed[n] || seen[n] {
			continue
		}
		seen[n] = true
		unlisted = append(unlisted, n)
	}
	sort.Strings(unlisted)
	if len(unlisted) > 0 {
		t.Errorf("install.sh reads environment variables that are not on the list (B1 of #387): %v", unlisted)
	}
	for n := range envAllowed {
		if !strings.Contains(src, n) {
			t.Errorf("%s is on the list, but install.sh does not read it: take it off", n)
		}
	}
}

func TestInstallerRunsNoOutsideText(t *testing.T) {
	b, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "#") {
			continue
		}
		bad := regexp.MustCompile(`(^|[;&|(]\s*)(eval|source)\s|(^|[;&|(]\s*)\.\s+["$]|\b(bash|sh)\s+-c\s+"?\$`).MatchString(code)
		// The one "sh -c" runs the script that install.sh builds itself,
		// inside the LXC (set_env_lines).
		if bad && !strings.Contains(code, `pct exec "$LXC_ID" -- sh -c "$script"`) {
			t.Errorf("install.sh line %d runs text from outside: %s", i+1, code)
		}
	}
}

// shellCode returns the parts of a bash script where $NAME is a shell
// variable: it drops comments, single-quoted text (perl, awk and sed
// programs, the unit file) and the bodies of quoted here-docs (<<'EOF').
// Double-quoted text and unquoted here-docs stay: they expand variables.
func shellCode(s string) string {
	var out strings.Builder
	lines := strings.Split(s, "\n")
	hereEnd := "" // the end word of a quoted here-doc being skipped
	inSingle := false
	heredoc := regexp.MustCompile(`<<-?'([A-Za-z_]+)'`)
	for _, line := range lines {
		if hereEnd != "" {
			if strings.TrimSpace(line) == hereEnd {
				hereEnd = ""
			}
			out.WriteByte('\n')
			continue
		}
		inDouble := false
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch {
			case inSingle:
				if c == '\'' {
					inSingle = false
				}
				continue
			case inDouble:
				if c == '\\' && i+1 < len(line) {
					out.WriteByte(c)
					out.WriteByte(line[i+1])
					i++
					continue
				}
				if c == '"' {
					inDouble = false
				}
			case c == '\\' && i+1 < len(line):
				out.WriteByte(c)
				out.WriteByte(line[i+1])
				i++
				continue
			case c == '\'':
				inSingle = true
				continue
			case c == '"':
				inDouble = true
			case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
				i = len(line) // a comment to the line end
				continue
			}
			out.WriteByte(c)
		}
		if !inSingle {
			if m := heredoc.FindStringSubmatch(line); m != nil {
				hereEnd = m[1]
			}
		}
		out.WriteByte('\n')
	}
	return out.String()
}
