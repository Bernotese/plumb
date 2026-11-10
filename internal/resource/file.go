package resource

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/Bernotese/plumb/internal/wire"
)

// maxFileSize is the largest file that the file resource reads or writes.
const maxFileSize = 4 << 20

// file is a regular file with fixed content (schema/file.json).
type file struct {
	base
	a fileAttrs
}

type fileAttrs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode"`
	Owner   string `json:"owner"`
	Group   string `json:"group"`
	State   string `json:"state"`
}

func newFile(r wire.Resource, env Env) (Resource, error) {
	f := &file{base: base{res: r, env: env}, a: fileAttrs{Mode: "0644", Owner: "root", Group: "root", State: wire.StatePresent}}
	if err := decode(r, "file", &f.a, func() string { return f.a.Path }); err != nil {
		return nil, err
	}
	mode, err := parseMode(f.a.Mode)
	if err != nil {
		return nil, fmt.Errorf("resource %s: %w", r.ID, err)
	}
	f.a.Mode = mode
	if len(f.a.Content) > maxFileSize {
		return nil, fmt.Errorf("resource %s: content is larger than %d bytes", r.ID, maxFileSize)
	}
	return f, nil
}

// Observe reads the file. It does not follow a symlink at the path.
func (f *file) Observe(_ context.Context) (State, error) {
	p := f.path(f.a.Path)
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return absent(), nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (%s)", f.a.Path, fi.Mode().Type())
	}
	if fi.Size() > maxFileSize {
		return nil, fmt.Errorf("%s is larger than %d bytes", f.a.Path, maxFileSize)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	st, err := observeNode(f.env.Root, fi)
	if err != nil {
		return nil, err
	}
	st["content"] = string(data)
	return st, nil
}

// Diff compares content, mode, owner and group. Deleting a file is
// destructive; every other change is reversible.
func (f *file) Diff(observed State) []wire.Change {
	if f.a.State == wire.StateAbsent {
		if observed[wire.AttrState] == wire.StatePresent {
			return f.finish([]wire.Change{change(wire.AttrState, wire.StatePresent, wire.StateAbsent, wire.RiskDestructive)})
		}
		return nil
	}
	want := State{"content": f.a.Content, "mode": f.a.Mode, "owner": f.a.Owner, "group": f.a.Group}
	return f.finish(diffNode(observed, want, []string{"content", "mode", "owner", "group"}))
}

// Apply writes the file atomically (temp file and rename in the same
// directory) when it is new or its content changed. The parent directory
// must exist: manage it with a directory resource.
func (f *file) Apply(_ context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	p := f.path(f.a.Path)
	if f.a.State == wire.StateAbsent {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	uid, gid, err := ownerIDs(f.env.Root, f.a.Owner, f.a.Group)
	if err != nil {
		return err
	}
	mode := toFileMode(f.a.Mode)
	if has(changes, wire.AttrState) || has(changes, "content") {
		return writeAtomic(p, []byte(f.a.Content), mode, uid, gid)
	}
	return setNode(p, changes, mode, uid, gid)
}

// Watch returns the file path.
func (f *file) Watch() []string { return []string{f.path(f.a.Path)} }

// writeAtomic replaces path with a new file. A symlink at path is replaced,
// not followed.
func writeAtomic(path string, data []byte, mode os.FileMode, uid, gid int) error {
	dir := filepath.Dir(path)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("parent directory %s does not exist", dir)
	}
	tmp, err := os.CreateTemp(dir, ".plumb-*")
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chown(uid, gid); err != nil {
		return err
	}
	// Chmod after Chown: chown clears the setuid and setgid bits.
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	ok = true
	// Make the rename durable.
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// observeNode reads mode, owner and group of a file or directory.
func observeNode(root string, fi fs.FileInfo) (State, error) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("%s: no owner information", fi.Name())
	}
	owner, grp, err := ownerNames(root, int(st.Uid), int(st.Gid))
	if err != nil {
		return nil, err
	}
	return State{
		wire.AttrState: wire.StatePresent,
		"mode":         formatMode(fi.Mode()),
		"owner":        owner,
		"group":        grp,
	}, nil
}

// diffNode returns the changes for a file or directory that must be present.
func diffNode(observed, want State, attrs []string) []wire.Change {
	var out []wire.Change
	created := observed[wire.AttrState] != wire.StatePresent
	if created {
		out = append(out, change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskReversible))
	}
	for _, attr := range attrs {
		if created {
			out = append(out, change(attr, nil, want[attr], wire.RiskReversible))
		} else if observed[attr] != want[attr] {
			out = append(out, change(attr, observed[attr], want[attr], wire.RiskReversible))
		}
	}
	return out
}

// setNode applies mode, owner and group changes to an existing path.
func setNode(path string, changes []wire.Change, mode os.FileMode, uid, gid int) error {
	if has(changes, "owner") || has(changes, "group") {
		if err := os.Lchown(path, uid, gid); err != nil {
			return err
		}
	}
	// Always set the mode after an owner change: chown clears setuid/setgid.
	if has(changes, "mode") || has(changes, "owner") || has(changes, "group") {
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if fi.Mode().Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", path)
		}
		return os.Chmod(path, mode)
	}
	return nil
}

// parseMode normalizes an octal mode string to four digits.
func parseMode(s string) (string, error) {
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil || n > 0o7777 {
		return "", fmt.Errorf("bad mode %q", s)
	}
	return fmt.Sprintf("%04o", n), nil
}

// toFileMode converts a normalized octal mode to an os.FileMode.
func toFileMode(s string) os.FileMode {
	n, _ := strconv.ParseUint(s, 8, 32)
	m := os.FileMode(n & 0o777)
	if n&0o4000 != 0 {
		m |= os.ModeSetuid
	}
	if n&0o2000 != 0 {
		m |= os.ModeSetgid
	}
	if n&0o1000 != 0 {
		m |= os.ModeSticky
	}
	return m
}

// formatMode converts an os.FileMode to four octal digits.
func formatMode(m os.FileMode) string {
	n := uint32(m.Perm())
	if m&os.ModeSetuid != 0 {
		n |= 0o4000
	}
	if m&os.ModeSetgid != 0 {
		n |= 0o2000
	}
	if m&os.ModeSticky != 0 {
		n |= 0o1000
	}
	return fmt.Sprintf("%04o", n)
}
