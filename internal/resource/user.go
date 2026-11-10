package resource

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
)

// user is a local account (schema/user.json). groups and ssh_keys are
// complete lists: anything else is drift.
type user struct {
	base
	a userAttrs
}

type userAttrs struct {
	Name    string   `json:"name"`
	Shell   string   `json:"shell"`
	Home    string   `json:"home"`
	Groups  []string `json:"groups"`
	SSHKeys []string `json:"ssh_keys"`
	State   string   `json:"state"`
}

func newUser(r wire.Resource, env Env) (Resource, error) {
	u := &user{base: base{res: r, env: env}, a: userAttrs{Shell: "/bin/bash", State: wire.StatePresent}}
	if err := decode(r, "user", &u.a, func() string { return u.a.Name }); err != nil {
		return nil, err
	}
	if u.a.Home == "" {
		u.a.Home = "/home/" + u.a.Name
	}
	u.a.Groups = sortedSet(u.a.Groups)
	// The schema forbids this; the agent does not trust the wire.
	for _, k := range u.a.SSHKeys {
		if strings.ContainsAny(k, "\r\n\x00") {
			return nil, fmt.Errorf("resource %s: an ssh key holds a line break", r.ID)
		}
	}
	return u, nil
}

// Observe reads passwd, group and the authorized_keys file of the user.
func (u *user) Observe(_ context.Context) (State, error) {
	acc, ok, err := u.account()
	if err != nil {
		return nil, err
	}
	if !ok {
		return absent(), nil
	}
	groups, err := readGroups(u.env.Root)
	if err != nil {
		return nil, err
	}
	var member []string
	for _, g := range groups {
		for _, m := range g.members {
			if m == u.a.Name {
				member = append(member, g.name)
			}
		}
	}
	keys, err := u.readKeys(acc.home)
	if err != nil {
		return nil, err
	}
	return State{
		wire.AttrState: wire.StatePresent,
		"shell":        acc.shell,
		"home":         acc.home,
		"groups":       sortedSet(member),
		"ssh_keys":     keys,
	}, nil
}

// Diff compares shell, home, groups and ssh_keys. Create is reversible,
// delete is destructive. An update that can take access away is disruptive
// (D-054): it removes an entry from groups or ssh_keys, or it changes shell
// or home. An update that only adds entries is reversible.
func (u *user) Diff(observed State) []wire.Change {
	if u.a.State == wire.StateAbsent {
		if observed[wire.AttrState] == wire.StatePresent {
			return u.finish([]wire.Change{change(wire.AttrState, wire.StatePresent, wire.StateAbsent, wire.RiskDestructive)})
		}
		return nil
	}
	keys := u.a.SSHKeys
	if keys == nil {
		keys = []string{}
	}
	if observed[wire.AttrState] != wire.StatePresent {
		out := []wire.Change{
			change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskReversible),
			change("shell", nil, u.a.Shell, wire.RiskReversible),
			change("home", nil, u.a.Home, wire.RiskReversible),
		}
		if len(u.a.Groups) > 0 {
			out = append(out, change("groups", nil, u.a.Groups, wire.RiskReversible))
		}
		if len(keys) > 0 {
			out = append(out, change("ssh_keys", nil, keys, wire.RiskReversible))
		}
		return u.finish(out)
	}
	var out []wire.Change
	for _, attr := range []string{"shell", "home"} {
		want := map[string]string{"shell": u.a.Shell, "home": u.a.Home}[attr]
		if observed[attr] != want {
			out = append(out, change(attr, observed[attr], want, wire.RiskDisruptive))
		}
	}
	if cur := toStrings(observed["groups"]); !equalStrings(sortedSet(cur), u.a.Groups) {
		out = append(out, change("groups", cur, u.a.Groups, removalRisk(cur, u.a.Groups)))
	}
	// The order of keys does not matter; duplicates do not count.
	if cur := toStrings(observed["ssh_keys"]); !equalStrings(sortedSet(cur), sortedSet(keys)) {
		out = append(out, change("ssh_keys", cur, keys, removalRisk(cur, keys)))
	}
	return u.finish(out)
}

// Apply runs useradd, usermod or userdel and writes authorized_keys.
// userdel keeps the home directory. A home change runs "usermod --home"
// without --move-home: the files stay in the old directory. Plumb never
// deletes an account with uid 0 or manages its keys.
func (u *user) Apply(ctx context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	u.env.AccountLock.Lock()
	defer u.env.AccountLock.Unlock()
	name := u.a.Name
	acc, exists, err := u.account()
	if err != nil {
		return err
	}
	if exists && acc.uid == 0 && (u.a.State == wire.StateAbsent || has(changes, "ssh_keys")) {
		return fmt.Errorf("user %s has uid 0; Plumb does not delete it or manage its ssh keys", name)
	}
	if u.a.State == wire.StateAbsent {
		_, err := u.run(ctx, "userdel", "--", name)
		return err
	}
	if has(changes, wire.AttrState) {
		args := []string{"--create-home", "--home-dir", u.a.Home, "--shell", u.a.Shell}
		if len(u.a.Groups) > 0 {
			args = append(args, "--groups", strings.Join(u.a.Groups, ","))
		}
		if _, err := u.run(ctx, "useradd", append(args, "--", name)...); err != nil {
			return err
		}
	} else {
		if has(changes, "shell") {
			if _, err := u.run(ctx, "usermod", "--shell", u.a.Shell, "--", name); err != nil {
				return err
			}
		}
		if has(changes, "home") {
			if _, err := u.run(ctx, "usermod", "--home", u.a.Home, "--", name); err != nil {
				return err
			}
		}
		if has(changes, "groups") {
			if _, err := u.run(ctx, "usermod", "--groups", strings.Join(u.a.Groups, ","), "--", name); err != nil {
				return err
			}
		}
	}
	if has(changes, "ssh_keys") {
		return u.writeKeys()
	}
	return nil
}

// Watch returns passwd, group and the authorized_keys file.
func (u *user) Watch() []string {
	return []string{u.path("/etc/passwd"), u.path("/etc/group"), u.path(u.a.Home + "/.ssh/authorized_keys")}
}

func (u *user) account() (account, bool, error) {
	users, err := readPasswd(u.env.Root)
	if err != nil {
		return account{}, false, err
	}
	for _, a := range users {
		if a.name == u.a.Name {
			return a, true, nil
		}
	}
	return account{}, false, nil
}

// openHome opens the home directory as an os.Root, so a symlink that the
// user planted cannot point the agent (root) at a file outside the home.
func (u *user) openHome(home string) (*os.Root, error) {
	return os.OpenRoot(u.path(home))
}

func (u *user) readKeys(home string) ([]string, error) {
	keys := []string{}
	r, err := u.openHome(home)
	if errors.Is(err, fs.ErrNotExist) {
		return keys, nil
	}
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := r.ReadFile(".ssh/authorized_keys")
	if errors.Is(err, fs.ErrNotExist) {
		return keys, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read authorized_keys of %s: %w", u.a.Name, err)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			keys = append(keys, line)
		}
	}
	return keys, sc.Err()
}

// writeKeys writes ~/.ssh/authorized_keys (0600) in ~/.ssh (0700), both
// owned by the user. An empty list removes the file.
func (u *user) writeKeys() error {
	acc, ok, err := u.account()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("user %s does not exist", u.a.Name)
	}
	r, err := u.openHome(acc.home)
	if err != nil {
		return fmt.Errorf("home of %s: %w", u.a.Name, err)
	}
	defer r.Close()
	if len(u.a.SSHKeys) == 0 {
		if err := r.Remove(".ssh/authorized_keys"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := r.Mkdir(".ssh", 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := r.Lchown(".ssh", acc.uid, acc.gid); err != nil {
		return err
	}
	if err := r.Chmod(".ssh", 0o700); err != nil {
		return err
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	tmp := ".ssh/.plumb-" + hex.EncodeToString(suffix)
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	content := strings.Join(u.a.SSHKeys, "\n") + "\n"
	_, err = f.WriteString(content)
	if err == nil {
		err = f.Chown(acc.uid, acc.gid)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = r.Rename(tmp, ".ssh/authorized_keys")
	}
	if err != nil {
		_ = r.Remove(tmp)
	}
	return err
}

// removalRisk is disruptive when want lacks an entry of cur, else reversible.
func removalRisk(cur, want []string) wire.Risk {
	for _, c := range cur {
		found := false
		for _, w := range want {
			if c == w {
				found = true
				break
			}
		}
		if !found {
			return wire.RiskDisruptive
		}
	}
	return wire.RiskReversible
}
