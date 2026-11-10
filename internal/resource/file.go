package resource

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/Bernotese/plumb/internal/wire"
	"golang.org/x/sys/unix"
)

// maxFileSize is the largest file that the file resource reads or writes.
const maxFileSize = 4 << 20

// file is a regular file with fixed content (schema/file.json). Its path
// may hold no symlink in any component (D-054, fsx.go).
type file struct {
	base
	a    fileAttrs
	mode uint32
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
	if _, err := splitPath(f.a.Path); err != nil {
		return nil, fmt.Errorf("resource %s: %w", r.ID, err)
	}
	mode, err := parseMode(f.a.Mode)
	if err != nil {
		return nil, fmt.Errorf("resource %s: %w", r.ID, err)
	}
	f.a.Mode, f.mode = fmt.Sprintf("%04o", mode), mode
	if len(f.a.Content) > maxFileSize {
		return nil, fmt.Errorf("resource %s: content is larger than %d bytes", r.ID, maxFileSize)
	}
	return f, nil
}

// Observe reads the file through a file descriptor. A missing parent
// directory means the file is absent.
func (f *file) Observe(_ context.Context) (State, error) {
	dfd, name, err := openParent(f.env.Root, f.a.Path)
	if isNoParent(err) {
		return absent(), nil
	}
	if err != nil {
		return nil, err
	}
	defer unix.Close(dfd)
	if _, ok, err := lstatAt(dfd, name, f.a.Path); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return absent(), nil
	}
	fd, st, err := openRegular(dfd, name, f.a.Path, unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	fh := os.NewFile(uintptr(fd), f.a.Path)
	defer fh.Close()
	if st.Size > maxFileSize {
		return nil, fmt.Errorf("%s is larger than %d bytes", f.a.Path, maxFileSize)
	}
	data, err := io.ReadAll(io.LimitReader(fh, maxFileSize+1))
	if err != nil {
		return nil, err
	}
	state, err := nodeState(f.env.Root, st)
	if err != nil {
		return nil, err
	}
	state["content"] = string(data)
	return state, nil
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

// Apply writes the file atomically (temp file and renameat in the same
// directory) when it is new or its content changed. The parent directory
// must exist: manage it with a directory resource.
func (f *file) Apply(_ context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	dfd, name, err := openParent(f.env.Root, f.a.Path)
	if err != nil {
		if isNoParent(err) {
			return fmt.Errorf("parent directory of %s does not exist", f.a.Path)
		}
		return err
	}
	defer unix.Close(dfd)
	if f.a.State == wire.StateAbsent {
		st, ok, err := lstatAt(dfd, name, f.a.Path)
		if err != nil || !ok {
			return err
		}
		if err := checkRegular(st, f.a.Path); err != nil {
			return err
		}
		return unix.Unlinkat(dfd, name, 0)
	}
	uid, gid, err := ownerIDs(f.env.Root, f.a.Owner, f.a.Group)
	if err != nil {
		return err
	}
	if has(changes, wire.AttrState) || has(changes, "content") {
		return writeAt(dfd, name, f.a.Path, []byte(f.a.Content), f.mode, uid, gid)
	}
	fd, _, err := openRegular(dfd, name, f.a.Path, unix.O_RDONLY)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return setOwnerMode(fd, has(changes, "owner") || has(changes, "group"), uid, gid, f.mode)
}

// Watch returns the file path.
func (f *file) Watch() []string { return []string{f.path(f.a.Path)} }

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

// parseMode parses an octal mode string (3 or 4 digits).
func parseMode(s string) (uint32, error) {
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil || n > 0o7777 {
		return 0, fmt.Errorf("bad mode %q", s)
	}
	return uint32(n), nil
}
