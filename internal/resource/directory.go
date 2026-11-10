package resource

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Bernotese/plumb/internal/wire"
)

// directory is a directory (schema/directory.json).
type directory struct {
	base
	a dirAttrs
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
	mode, err := parseMode(d.a.Mode)
	if err != nil {
		return nil, fmt.Errorf("resource %s: %w", r.ID, err)
	}
	d.a.Mode = mode
	return d, nil
}

// Observe reads the directory. It does not follow a symlink at the path.
func (d *directory) Observe(_ context.Context) (State, error) {
	fi, err := os.Lstat(d.path(d.a.Path))
	if errors.Is(err, fs.ErrNotExist) {
		return absent(), nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory (%s)", d.a.Path, fi.Mode().Type())
	}
	return observeNode(d.env.Root, fi)
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
	p := d.path(d.a.Path)
	if d.a.State == wire.StateAbsent {
		err := os.Remove(p)
		if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			return fmt.Errorf("directory %s is not empty; Plumb removes only empty directories", d.a.Path)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	uid, gid, err := ownerIDs(d.env.Root, d.a.Owner, d.a.Group)
	if err != nil {
		return err
	}
	mode := toFileMode(d.a.Mode)
	if has(changes, wire.AttrState) {
		if fi, err := os.Stat(filepath.Dir(p)); err != nil || !fi.IsDir() {
			return fmt.Errorf("parent directory of %s does not exist", d.a.Path)
		}
		if err := os.Mkdir(p, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		// Set owner and mode explicitly: Mkdir applies the umask.
		changes = append(changes, change("owner", nil, nil, wire.RiskReversible), change("mode", nil, nil, wire.RiskReversible))
	}
	return setNode(p, changes, mode, uid, gid)
}

// Watch returns the directory path.
func (d *directory) Watch() []string { return []string{d.path(d.a.Path)} }
