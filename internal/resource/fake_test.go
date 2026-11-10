package resource

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// guestRoot makes a temp root with passwd and group files. root maps to the
// uid and gid of the test process, so chown works without privileges.
func guestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	uid, gid := os.Getuid(), os.Getgid()
	mustWrite(t, filepath.Join(root, "etc/passwd"), fmt.Sprintf("root:x:%d:%d:root:/root:/bin/bash\n", uid, gid))
	mustWrite(t, filepath.Join(root, "etc/group"), fmt.Sprintf("root:x:%d:\nsudo:x:%d:\nadm:x:%d:\n", gid, gid+100001, gid+100002))
	for _, d := range []string{"home", "srv", "var/lib/dpkg"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// unit is the simulated state of one systemd unit.
type unit struct {
	active, enabled bool
	static          bool
}

// fakeGuest is a fake Runner that simulates dpkg/apt, systemd and the
// shadow tools on a temp root. It records every call and fails a call that
// has an operand without "--" before it.
type fakeGuest struct {
	t    *testing.T
	root string

	mu         sync.Mutex
	calls      [][]string
	packages   map[string]string   // installed package → version
	candidates map[string][]string // installable versions, first is the newest
	units      map[string]*unit
	aptBusy    int // apt-get calls in flight
	aptMax     int // highest aptBusy seen
	aptDelay   time.Duration
	updated    bool // apt-get update ran
	needUpdate bool // install fails until apt-get update ran
}

func newFakeGuest(t *testing.T) *fakeGuest {
	return &fakeGuest{
		t:          t,
		root:       guestRoot(t),
		packages:   map[string]string{},
		candidates: map[string][]string{"nginx": {"1.26.0-2", "1.26.0-1"}, "cron": {"3.0pl1-197"}, "tree": {"2.2.1-1"}},
		units:      map[string]*unit{},
	}
}

func (g *fakeGuest) env() Env { return NewEnv(g.root, g) }

// noOperand lists calls that have no operand and so need no "--".
func noOperand(name string, args []string) bool {
	last := ""
	if len(args) > 0 {
		last = args[len(args)-1]
	}
	return (name == "systemctl" && last == "daemon-reload") || (name == "apt-get" && last == "update")
}

func (g *fakeGuest) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	g.mu.Lock()
	g.calls = append(g.calls, append([]string{name}, args...))
	g.mu.Unlock()
	i := slices.Index(args, "--")
	if !noOperand(name, args) && (i < 0 || i != len(args)-2) {
		g.t.Errorf("call without \"--\" before its one operand: %s %q", name, args)
		return nil, fmt.Errorf("bad call")
	}
	operand := ""
	if i >= 0 {
		operand = args[i+1]
	}
	switch name {
	case "dpkg-query":
		g.mu.Lock()
		defer g.mu.Unlock()
		v, ok := g.packages[operand]
		if !ok {
			return nil, &ExitError{Name: name, Code: 1, Stderr: "dpkg-query: no packages found matching " + operand}
		}
		return []byte("ii |" + v + "\n"), nil
	case "apt-get":
		return g.apt(args, operand)
	case "systemctl":
		return g.systemctl(args, operand)
	case "useradd", "usermod", "userdel":
		return nil, g.shadow(name, args[:i], operand)
	}
	return nil, &ExitError{Name: name, Code: 127, Stderr: "command not found"}
}

func (g *fakeGuest) apt(args []string, operand string) ([]byte, error) {
	g.mu.Lock()
	g.aptBusy++
	g.aptMax = max(g.aptMax, g.aptBusy)
	delay := g.aptDelay
	g.mu.Unlock()
	time.Sleep(delay)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.aptBusy--
	switch {
	case slices.Contains(args, "update"):
		g.updated = true
		return nil, nil
	case slices.Contains(args, "install"):
		if g.needUpdate && !g.updated {
			return nil, &ExitError{Name: "apt-get", Code: 100, Stderr: "E: Unable to locate package " + operand}
		}
		name, version, exact := strings.Cut(operand, "=")
		versions := g.candidates[name]
		if len(versions) == 0 || (exact && !slices.Contains(versions, version)) {
			return nil, &ExitError{Name: "apt-get", Code: 100, Stderr: "E: Version not found"}
		}
		if !exact {
			if _, ok := g.packages[name]; ok {
				return nil, nil // already installed: apt keeps the version
			}
			version = versions[0]
		}
		g.packages[name] = version
		return nil, nil
	case slices.Contains(args, "remove"):
		delete(g.packages, operand)
		return nil, nil
	}
	return nil, &ExitError{Name: "apt-get", Code: 100, Stderr: "bad command"}
}

func (g *fakeGuest) systemctl(args []string, operand string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	verb := args[0]
	if verb == "daemon-reload" {
		return nil, nil
	}
	u := g.units[operand]
	if verb == "show" {
		if u == nil {
			return []byte("LoadState=not-found\nActiveState=inactive\nUnitFileState=\n"), nil
		}
		active, file := "inactive", "disabled"
		if u.active {
			active = "active"
		}
		if u.enabled {
			file = "enabled"
		}
		if u.static {
			file = "static"
		}
		return []byte("LoadState=loaded\nActiveState=" + active + "\nUnitFileState=" + file + "\n"), nil
	}
	if u == nil {
		return nil, &ExitError{Name: "systemctl", Code: 5, Stderr: "Unit " + operand + " not found."}
	}
	switch verb {
	case "start":
		u.active = true
	case "stop":
		u.active = false
	case "restart":
		u.active = true
	case "enable":
		u.enabled = true
	case "disable":
		u.enabled = false
	default:
		return nil, fmt.Errorf("unknown verb %s", verb)
	}
	return nil, nil
}

// shadow simulates useradd, usermod and userdel on the passwd and group
// files. Every user gets the uid of the test process, so chown works.
func (g *fakeGuest) shadow(name string, opts []string, login string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	opt := func(flag string) (string, bool) {
		if i := slices.Index(opts, flag); i >= 0 && i+1 < len(opts) {
			return opts[i+1], true
		}
		return "", false
	}
	passwd := filepath.Join(g.root, "etc/passwd")
	data, _ := os.ReadFile(passwd)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	idx := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, login+":") })
	switch name {
	case "useradd":
		if idx >= 0 {
			return &ExitError{Name: name, Code: 9, Stderr: "user exists"}
		}
		home, _ := opt("--home-dir")
		shell, _ := opt("--shell")
		lines = append(lines, fmt.Sprintf("%s:x:%d:%d::%s:%s", login, os.Getuid(), os.Getgid(), home, shell))
		if slices.Contains(opts, "--create-home") {
			if err := os.MkdirAll(filepath.Join(g.root, home), 0o755); err != nil {
				return err
			}
		}
		if groups, ok := opt("--groups"); ok {
			g.setGroups(login, strings.Split(groups, ","))
		}
	case "usermod":
		if idx < 0 {
			return &ExitError{Name: name, Code: 6, Stderr: "user does not exist"}
		}
		f := strings.Split(lines[idx], ":")
		if v, ok := opt("--shell"); ok {
			f[6] = v
		}
		if v, ok := opt("--home"); ok {
			f[5] = v
		}
		lines[idx] = strings.Join(f, ":")
		if v, ok := opt("--groups"); ok {
			var groups []string
			if v != "" {
				groups = strings.Split(v, ",")
			}
			g.setGroups(login, groups)
		}
	case "userdel":
		if idx < 0 {
			return &ExitError{Name: name, Code: 6, Stderr: "user does not exist"}
		}
		lines = slices.Delete(lines, idx, idx+1)
		g.setGroups(login, nil)
	}
	return os.WriteFile(passwd, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// setGroups makes login a member of exactly these groups.
func (g *fakeGuest) setGroups(login string, groups []string) {
	path := filepath.Join(g.root, "etc/group")
	data, _ := os.ReadFile(path)
	var out []string
	for _, l := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		f := strings.Split(l, ":")
		var members []string
		for _, m := range strings.Split(f[3], ",") {
			if m != "" && m != login {
				members = append(members, m)
			}
		}
		if slices.Contains(groups, f[0]) {
			members = append(members, login)
		}
		f[3] = strings.Join(members, ",")
		out = append(out, strings.Join(f, ":"))
	}
	_ = os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

// count returns how often a call with these leading words was made.
func (g *fakeGuest) count(words ...string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, c := range g.calls {
		if len(c) >= len(words) && slices.Equal(c[:len(words)], words) {
			n++
		}
	}
	return n
}

// countArg returns how often a call of name contained arg.
func (g *fakeGuest) countArg(name, arg string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, c := range g.calls {
		if c[0] == name && slices.Contains(c[1:], arg) {
			n++
		}
	}
	return n
}

// res builds a rendered resource; the key attribute forms the id.
func res(typ, key string, attrs map[string]any, sensitive ...string) wire.Resource {
	t, _ := Lookup(typ)
	a := map[string]any{t.Key: key}
	for k, v := range attrs {
		a[k] = v
	}
	return wire.Resource{ID: wire.ID(typ, key), Type: typ, Block: "test", Attrs: a, Sensitive: sensitive}
}

func build(t *testing.T, r wire.Resource, env Env) Resource {
	t.Helper()
	rs, err := New(r, env)
	if err != nil {
		t.Fatalf("New(%s): %v", r.ID, err)
	}
	return rs
}

// plan observes and diffs. Every change must have a valid risk.
func plan(t *testing.T, rs Resource) []wire.Change {
	t.Helper()
	obs, err := rs.Observe(context.Background())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	changes := rs.Diff(obs)
	for _, c := range changes {
		if !c.Risk.Valid() {
			t.Errorf("change %+v has no valid risk", c)
		}
	}
	return changes
}

// converge applies the plan and checks that a second round has no change.
// It returns the changes of the first round.
func converge(t *testing.T, rs Resource) []wire.Change {
	t.Helper()
	changes := plan(t, rs)
	if err := rs.Apply(context.Background(), changes); err != nil {
		t.Fatalf("Apply(%+v): %v", changes, err)
	}
	if again := plan(t, rs); len(again) != 0 {
		t.Fatalf("second round has changes: %+v", again)
	}
	if err := rs.Apply(context.Background(), nil); err != nil {
		t.Fatalf("Apply(nil): %v", err)
	}
	return changes
}

func attrsOf(changes []wire.Change) string {
	var out []string
	for _, c := range changes {
		out = append(out, c.Attr+"="+string(c.Risk))
	}
	return strings.Join(out, ",")
}
