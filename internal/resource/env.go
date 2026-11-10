package resource

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Bernotese/plumb/internal/wire"
)

// NewEnv returns an Env for the guest file system at root. All resources
// built with the same Env share one package-manager lock, so build one Env
// per guest.
func NewEnv(root string, run Runner) Env {
	return Env{Root: root, Run: run, PackageLock: new(sync.Mutex)}
}

// New builds the resource for a rendered resource of a known, implemented type.
func New(r wire.Resource, env Env) (Resource, error) {
	t, ok := Lookup(r.Type)
	if !ok || t.New == nil {
		return nil, fmt.Errorf("resource %s: unknown type %q", r.ID, r.Type)
	}
	if env.Run == nil || env.PackageLock == nil || env.Root == "" {
		return nil, fmt.Errorf("resource %s: incomplete Env, use NewEnv", r.ID)
	}
	return t.New(r, env)
}

// ExecRunner runs real commands. Output is stdout. A command that exits
// non-zero returns an *ExitError with the start of stderr.
type ExecRunner struct{}

// Run implements Runner. It never uses a shell.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, &ExitError{Name: name, Code: ee.ExitCode(), Stderr: clip(strings.TrimSpace(stderr.String()), 2048)}
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// ExitError is a command that ran and exited with a non-zero code.
type ExitError struct {
	Name   string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("%s: exit status %d", e.Name, e.Code)
	}
	return fmt.Sprintf("%s: exit status %d: %s", e.Name, e.Code, e.Stderr)
}

// exitCode returns the exit code of a failed command, or -1.
func exitCode(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

// base holds what every resource type needs.
type base struct {
	res wire.Resource
	env Env
}

// path maps a guest path to the host path under Env.Root.
func (b base) path(p string) string { return filepath.Join(b.env.Root, p) }

// run runs a command through the injected runner.
func (b base) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return b.env.Run.Run(ctx, name, args...)
}

// finish applies the sensitive rule (D-037, D-047) to the result of a Diff.
// The comparison happened locally with the clear values; the result keeps
// only the attribute name, the sensitive flag and the risk.
func (b base) finish(changes []wire.Change) []wire.Change {
	if len(changes) == 0 {
		return nil
	}
	return wire.Redact(b.res.Sensitive, changes)
}

// decode checks the id of a rendered resource and decodes its attributes
// into v, which holds the schema defaults. key returns the key attribute.
func decode(r wire.Resource, typ string, v any, key func() string) error {
	if r.Type != typ {
		return fmt.Errorf("resource %s: type %q is not %q", r.ID, r.Type, typ)
	}
	b, err := json.Marshal(r.Attrs)
	if err != nil {
		return fmt.Errorf("resource %s: %w", r.ID, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("resource %s: attributes: %w", r.ID, err)
	}
	if want := wire.ID(typ, key()); r.ID != want {
		return fmt.Errorf("resource %s: id does not match the attributes (%s)", r.ID, want)
	}
	return nil
}

// change builds a change; a nil from or to means "not there".
func change(attr string, from, to any, risk wire.Risk) wire.Change {
	return wire.Change{Attr: attr, From: from, To: to, Risk: risk}
}

// has reports whether the changes touch the attribute.
func has(changes []wire.Change, attr string) bool {
	for _, c := range changes {
		if c.Attr == attr {
			return true
		}
	}
	return false
}

// absent is the observed state of a resource that is not on the guest.
func absent() State { return State{wire.AttrState: wire.StateAbsent} }

// sortedSet returns a sorted copy without duplicates; never nil.
func sortedSet(list []string) []string {
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// account is one line of /etc/passwd.
type account struct {
	name     string
	uid, gid int
	home     string
	shell    string
}

// group is one line of /etc/group.
type group struct {
	name    string
	gid     int
	members []string
}

// readPasswd parses <root>/etc/passwd.
func readPasswd(root string) ([]account, error) {
	var out []account
	err := readColonFile(filepath.Join(root, "etc/passwd"), 7, func(f []string) error {
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil {
			return fmt.Errorf("bad ids for %q", f[0])
		}
		out = append(out, account{name: f[0], uid: uid, gid: gid, home: f[5], shell: f[6]})
		return nil
	})
	return out, err
}

// readGroups parses <root>/etc/group.
func readGroups(root string) ([]group, error) {
	var out []group
	err := readColonFile(filepath.Join(root, "etc/group"), 4, func(f []string) error {
		gid, err := strconv.Atoi(f[2])
		if err != nil {
			return fmt.Errorf("bad gid for %q", f[0])
		}
		var members []string
		if f[3] != "" {
			members = strings.Split(f[3], ",")
		}
		out = append(out, group{name: f[0], gid: gid, members: members})
		return nil
	})
	return out, err
}

func readColonFile(path string, fields int, fn func([]string) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, ":")
		if len(f) < fields {
			return fmt.Errorf("%s: short line %q", path, f[0])
		}
		if err := fn(f); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return sc.Err()
}

// ownerIDs resolves an owner and a group name in the guest.
func ownerIDs(root, owner, grp string) (uid, gid int, err error) {
	users, err := readPasswd(root)
	if err != nil {
		return 0, 0, err
	}
	groups, err := readGroups(root)
	if err != nil {
		return 0, 0, err
	}
	uid, gid = -1, -1
	for _, a := range users {
		if a.name == owner {
			uid = a.uid
		}
	}
	for _, g := range groups {
		if g.name == grp {
			gid = g.gid
		}
	}
	if uid < 0 {
		return 0, 0, fmt.Errorf("user %q does not exist", owner)
	}
	if gid < 0 {
		return 0, 0, fmt.Errorf("group %q does not exist", grp)
	}
	return uid, gid, nil
}

// ownerNames maps ids to names in the guest. An unknown id stays a number.
func ownerNames(root string, uid, gid int) (owner, grp string, err error) {
	owner, grp = strconv.Itoa(uid), strconv.Itoa(gid)
	users, err := readPasswd(root)
	if err != nil {
		return "", "", err
	}
	groups, err := readGroups(root)
	if err != nil {
		return "", "", err
	}
	for _, a := range users {
		if a.uid == uid {
			owner = a.name
			break
		}
	}
	for _, g := range groups {
		if g.gid == gid {
			grp = g.name
			break
		}
	}
	return owner, grp, nil
}

// toStrings converts a list attribute ([]string, or []any from JSON) to
// []string. Other values give nil.
func toStrings(v any) []string {
	switch v := v.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
