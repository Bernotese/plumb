package resource

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
	"golang.org/x/sys/unix"
)

// maxLinkTarget is the longest link target in bytes (PATH_MAX without NUL).
const maxLinkTarget = 4095

// attrKind is an observed-only attribute of symlink: the type of a node
// that is on the link path but is not a symlink ("file", "directory",
// "other"). Diff turns it into a destructive change to "symlink".
const attrKind = "kind"

// symlink is a symbolic link (schema/symlink.json). The link path follows
// the rules of file and directory (D-054, fsx.go): no symlink in a parent
// component. The target is data: the resource reads it with readlinkat and
// writes it with symlinkat; it never opens, stats or chowns through the link.
type symlink struct {
	base
	a symlinkAttrs
}

type symlinkAttrs struct {
	Path   string `json:"path"`
	Target string `json:"target"`
	State  string `json:"state"`
}

func newSymlink(r wire.Resource, env Env) (Resource, error) {
	s := &symlink{base: base{res: r, env: env}, a: symlinkAttrs{State: wire.StatePresent}}
	if err := decode(r, "symlink", &s.a, func() string { return s.a.Path }); err != nil {
		return nil, err
	}
	if _, err := splitPath(s.a.Path); err != nil {
		return nil, fmt.Errorf("resource %s: %w", r.ID, err)
	}
	switch s.a.State {
	case wire.StatePresent:
		if s.a.Target == "" || len(s.a.Target) > maxLinkTarget || strings.ContainsRune(s.a.Target, 0) {
			return nil, fmt.Errorf("resource %s: target must have 1 to %d bytes and hold no NUL", r.ID, maxLinkTarget)
		}
	case wire.StateAbsent:
	default:
		return nil, fmt.Errorf("resource %s: bad state %q", r.ID, s.a.State)
	}
	return s, nil
}

// Observe reads the link with readlinkat. A missing parent directory means
// the link is absent. A node that is not a symlink is observed with its
// kind and no target.
func (s *symlink) Observe(_ context.Context) (State, error) {
	dfd, name, err := openParent(s.env.Root, s.a.Path)
	if isNoParent(err) {
		return absent(), nil
	}
	if err != nil {
		return nil, err
	}
	defer unix.Close(dfd)
	kind, target, err := readLinkAt(dfd, name, s.a.Path)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "":
		return absent(), nil
	case "symlink":
		return State{wire.AttrState: wire.StatePresent, "target": target}, nil
	}
	return State{wire.AttrState: wire.StatePresent, attrKind: kind}, nil
}

// Diff compares the target. A new link and a new target are reversible.
// Removing a link and replacing a node that is not a link are destructive.
func (s *symlink) Diff(observed State) []wire.Change {
	present := observed[wire.AttrState] == wire.StatePresent
	kind, _ := observed[attrKind].(string)
	if s.a.State == wire.StateAbsent {
		if present {
			return s.finish([]wire.Change{change(wire.AttrState, wire.StatePresent, wire.StateAbsent, wire.RiskDestructive)})
		}
		return nil
	}
	switch {
	case !present:
		return s.finish([]wire.Change{
			change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskReversible),
			change("target", nil, s.a.Target, wire.RiskReversible),
		})
	case kind != "":
		return s.finish([]wire.Change{
			change(attrKind, kind, "symlink", wire.RiskDestructive),
			change("target", nil, s.a.Target, wire.RiskDestructive),
		})
	case observed["target"] != s.a.Target:
		return s.finish([]wire.Change{change("target", observed["target"], s.a.Target, wire.RiskReversible)})
	}
	return nil
}

// Apply creates, replaces or removes the link. It creates no parent
// directories. A new link is made under a temp name and renamed over the
// path, so the path never is missing in between. It replaces a regular
// file only when the plan holds the destructive kind change, and it never
// replaces or removes a directory.
func (s *symlink) Apply(_ context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	dfd, name, err := openParent(s.env.Root, s.a.Path)
	if err != nil {
		if isNoParent(err) {
			return fmt.Errorf("parent directory of %s does not exist", s.a.Path)
		}
		return err
	}
	defer unix.Close(dfd)
	kind, target, err := readLinkAt(dfd, name, s.a.Path)
	if err != nil {
		return err
	}
	if s.a.State == wire.StateAbsent {
		switch kind {
		case "":
			return nil
		case "symlink":
			return unix.Unlinkat(dfd, name, 0)
		}
		return fmt.Errorf("%s is a %s, not a symlink; Plumb removes only symlinks", s.a.Path, kind)
	}
	switch kind {
	case "symlink":
		if target == s.a.Target {
			return nil
		}
	case "", "file":
		if kind == "file" && !has(changes, attrKind) {
			return fmt.Errorf("%s is a regular file now; plan again to replace it", s.a.Path)
		}
	default:
		return fmt.Errorf("%s is a %s; Plumb replaces only a symlink or a regular file with a symlink", s.a.Path, kind)
	}
	return symlinkAt(dfd, name, s.a.Path, s.a.Target)
}

// Watch returns the link path.
func (s *symlink) Watch() []string { return []string{s.path(s.a.Path)} }

// readLinkAt returns the kind of dirfd/name ("" when it does not exist,
// "symlink", "file", "directory" or "other") and, for a symlink, its
// target. It never follows the link.
func readLinkAt(dirfd int, name, p string) (kind, target string, err error) {
	var st unix.Stat_t
	err = unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("stat %s: %w", p, err)
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
	case unix.S_IFREG:
		return "file", "", nil
	case unix.S_IFDIR:
		return "directory", "", nil
	default:
		return "other", "", nil
	}
	buf := make([]byte, maxLinkTarget+1)
	n, err := unix.Readlinkat(dirfd, name, buf)
	if errors.Is(err, unix.EINVAL) {
		// Swapped for a non-link after the stat: observe again.
		return "", "", fmt.Errorf("%s changed while it was read; try again", p)
	}
	if err != nil {
		return "", "", fmt.Errorf("read link %s: %w", p, err)
	}
	if n > maxLinkTarget {
		return "", "", fmt.Errorf("link %s has a target longer than %d bytes", p, maxLinkTarget)
	}
	return "symlink", string(buf[:n]), nil
}

// symlinkAt makes dirfd/name a symlink to target: symlinkat under a temp
// name in the same directory, then renameat over name.
func symlinkAt(dirfd int, name, p, target string) error {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	tmp := ".plumb-" + hex.EncodeToString(suffix)
	if err := unix.Symlinkat(target, dirfd, tmp); err != nil {
		return fmt.Errorf("create link %s: %w", p, err)
	}
	if err := unix.Renameat(dirfd, tmp, dirfd, name); err != nil {
		_ = unix.Unlinkat(dirfd, tmp, 0)
		return fmt.Errorf("create link %s: %w", p, err)
	}
	return unix.Fsync(dirfd)
}
