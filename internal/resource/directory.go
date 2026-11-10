package resource

import (
	"context"
	"errors"
	"fmt"

	"github.com/Bernotese/plumb/internal/wire"
	"golang.org/x/sys/unix"
)

// directory is a directory (schema/directory.json). Its path may hold no
// symlink in any component (D-054, fsx.go).
type directory struct {
	base
	a    dirAttrs
	mode uint32
}

type dirAttrs struct {
	Path  string `json:"path"`
	Mode  string `json:"mode"`
	Owner string `json:"owner"`
	Group string `json:"group"`
	State string `json:"state"`
}

func newDirectory(r wire.Resource, env Env) (Resource, error) {
	d := &directory{base: base{res: r, env: env}, a: dirAttrs{Mode: "0755", Owner: "root", Group: "root", State: wire.StatePresent}}
	if err := decode(r, "directory", &d.a, func() string { return d.a.Path }); err != nil {
		return nil, err
	}
	if _, err := splitPath(d.a.Path); err != nil {
		return nil, fmt.Errorf("resource %s: %w", r.ID, err)
	}
	mode, err := parseMode(d.a.Mode)
	if err != nil {
		return nil, fmt.Errorf("resource %s: %w", r.ID, err)
	}
	d.a.Mode, d.mode = fmt.Sprintf("%04o", mode), mode
	return d, nil
}

// Observe stats the directory through a file descriptor. A missing parent
// directory means the directory is absent.
func (d *directory) Observe(_ context.Context) (State, error) {
	dfd, name, err := openParent(d.env.Root, d.a.Path)
	if isNoParent(err) {
		return absent(), nil
	}
	if err != nil {
		return nil, err
	}
	defer unix.Close(dfd)
	st, ok, err := lstatAt(dfd, name, d.a.Path)
	if err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return absent(), nil
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, fmt.Errorf("%s is not a directory", d.a.Path)
	}
	fd, st, err := openDir(dfd, name, d.a.Path)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	return nodeState(d.env.Root, st)
}

// Diff compares mode, owner and group. Deleting is destructive.
func (d *directory) Diff(observed State) []wire.Change {
	if d.a.State == wire.StateAbsent {
		if observed[wire.AttrState] == wire.StatePresent {
			return d.finish([]wire.Change{change(wire.AttrState, wire.StatePresent, wire.StateAbsent, wire.RiskDestructive)})
		}
		return nil
	}
	want := State{"mode": d.a.Mode, "owner": d.a.Owner, "group": d.a.Group}
	return d.finish(diffNode(observed, want, []string{"mode", "owner", "group"}))
}

// Apply creates, updates or removes the directory. It creates no parent
// directories and removes only an empty directory.
func (d *directory) Apply(_ context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	dfd, name, err := openParent(d.env.Root, d.a.Path)
	if err != nil {
		if isNoParent(err) {
			return fmt.Errorf("parent directory of %s does not exist", d.a.Path)
		}
		return err
	}
	defer unix.Close(dfd)
	if d.a.State == wire.StateAbsent {
		if _, ok, err := lstatAt(dfd, name, d.a.Path); err != nil || !ok {
			return err
		}
		err := unix.Unlinkat(dfd, name, unix.AT_REMOVEDIR)
		if errors.Is(err, unix.ENOTEMPTY) || errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("directory %s is not empty; Plumb removes only empty directories", d.a.Path)
		}
		return err
	}
	uid, gid, err := ownerIDs(d.env.Root, d.a.Owner, d.a.Group)
	if err != nil {
		return err
	}
	chown := has(changes, "owner") || has(changes, "group")
	if has(changes, wire.AttrState) {
		if err := unix.Mkdirat(dfd, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("create %s: %w", d.a.Path, err)
		}
		chown = true // Mkdirat applies the umask and the agent's ids
	}
	fd, _, err := openDir(dfd, name, d.a.Path)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return setOwnerMode(fd, chown, uid, gid, d.mode)
}

// Watch returns the directory path.
func (d *directory) Watch() []string { return []string{d.path(d.a.Path)} }
