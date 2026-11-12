package resource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
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
// With state absent, a node that is not a link means the link is absent:
// no change, because Apply removes only symlinks.
func (s *symlink) Diff(observed State) []wire.Change {
	present := observed[wire.AttrState] == wire.StatePresent
	kind, _ := observed[attrKind].(string)
	if s.a.State == wire.StateAbsent {
		if present && kind == "" {
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
		if kind == "symlink" {
			return removeLinkAt(dfd, name, s.a.Path)
		}
		return nil // nothing or not a link: the link is absent (Diff agrees)
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
	return symlinkAt(dfd, name, s.a.Path, s.a.Target, kind)
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

// tempLinkName is the temp name of the link name in its directory. It is
// fixed per name, so the next Apply removes a link that a crash left
// between symlinkat and renameat; one path is one resource (D-054), so no
// other apply uses the same temp name. Like every Plumb temp name it starts
// with a dot, which include globs such as "sites-enabled/*" skip.
func tempLinkName(name string) string {
	sum := sha256.Sum256([]byte(name))
	return ".plumb-link-" + hex.EncodeToString(sum[:8])
}

// Leaf races (D-163). Apply checks the node on the path and then changes
// it, so a writer of the directory could swap the node in between. Each
// change is made so that the kernel acts on the node that was checked, or
// the node that is moved aside is checked after the move and put back:
//   - over nothing: renameat2(RENAME_NOREPLACE), EEXIST → "plan again";
//   - over a link or a planned file: renameat2(RENAME_EXCHANGE), then the
//     old node (now under the temp name) must have the checked kind, else
//     it is exchanged back;
//   - remove: the link is renamed to the temp name (RENAME_NOREPLACE) and
//     removed only when it is still a symlink, else it is renamed back.
// So a node that was swapped in is never removed; at worst it stays under
// the temp name when a second swap blocks the undo, and Apply then names it.

// renameat2 is unix.Renameat2; a test replaces it.
var renameat2 = unix.Renameat2

// rename2 renames dirfd/from to dirfd/to with flags. EINVAL means that the
// file system has no RENAME_NOREPLACE/RENAME_EXCHANGE (ZFS before 2.2):
// there is no fallback, because a plain rename has the leaf races of D-163
// (D-228). p is the path of the link, for the text.
func rename2(dirfd int, from, to string, flags uint, p string) error {
	err := renameat2(dirfd, from, dirfd, to, flags)
	if errors.Is(err, unix.EINVAL) {
		return fmt.Errorf("the file system of %s does not support atomic rename (RENAME_NOREPLACE/RENAME_EXCHANGE); on ZFS it needs ZFS 2.2 or newer", path.Dir(p))
	}
	return err
}

// prepTemp removes a stale temp link of name (a crash between two steps);
// any other node under the temp name is an error and stays.
func prepTemp(dirfd int, tmp, p string) error {
	kind, _, err := readLinkAt(dirfd, tmp, p)
	switch {
	case err != nil:
		return err
	case kind == "symlink":
		if err := unix.Unlinkat(dirfd, tmp, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("remove stale temp link of %s: %w", p, err)
		}
	case kind != "":
		return fmt.Errorf("temp name %s next to %s is a %s; check and remove it", tmp, p, kind)
	}
	return nil
}

// symlinkAt makes dirfd/name a symlink to target. was is the kind that
// Apply checked on name: "" (nothing), "symlink" or "file".
func symlinkAt(dirfd int, name, p, target, was string) error {
	tmp := tempLinkName(name)
	if err := prepTemp(dirfd, tmp, p); err != nil {
		return err
	}
	if err := unix.Symlinkat(target, dirfd, tmp); err != nil {
		return fmt.Errorf("create link %s: %w", p, err)
	}
	if was == "" {
		if err := rename2(dirfd, tmp, name, unix.RENAME_NOREPLACE, p); err != nil {
			unlinkTempLink(dirfd, tmp)
			if errors.Is(err, unix.EEXIST) {
				return fmt.Errorf("%s changed while it was applied; plan again", p)
			}
			return fmt.Errorf("create link %s: %w", p, err)
		}
		return unix.Fsync(dirfd)
	}
	if err := rename2(dirfd, tmp, name, unix.RENAME_EXCHANGE, p); err != nil {
		unlinkTempLink(dirfd, tmp)
		if errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("%s changed while it was applied; plan again", p)
		}
		return fmt.Errorf("replace link %s: %w", p, err)
	}
	// The old node is now under tmp: it must be what Apply checked.
	if err := removeIfKind(dirfd, tmp, name, p, was); err != nil {
		return err
	}
	return unix.Fsync(dirfd)
}

// unlinkTempLink removes dirfd/tmp only when it is a symlink: a node that
// a writer of the directory swapped in under the temp name stays.
func unlinkTempLink(dirfd int, tmp string) {
	if kind, _, err := readLinkAt(dirfd, tmp, tmp); err == nil && kind == "symlink" {
		_ = unix.Unlinkat(dirfd, tmp, 0)
	}
}

// removeLinkAt removes the symlink dirfd/name: it moves the node aside and
// removes it only when it is still a symlink.
func removeLinkAt(dirfd int, name, p string) error {
	tmp := tempLinkName(name)
	if err := prepTemp(dirfd, tmp, p); err != nil {
		return err
	}
	if err := rename2(dirfd, name, tmp, unix.RENAME_NOREPLACE, p); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil // gone already
		}
		return fmt.Errorf("remove link %s: %w", p, err)
	}
	if kind, _, err := readLinkAt(dirfd, tmp, p); err != nil || kind != "symlink" {
		if uerr := rename2(dirfd, tmp, name, unix.RENAME_NOREPLACE, p); uerr != nil {
			return fmt.Errorf("%s changed while it was removed; the new node is kept as %s", p, tmp)
		}
		return fmt.Errorf("%s changed while it was removed; plan again", p)
	}
	if err := unix.Unlinkat(dirfd, tmp, 0); err != nil {
		return fmt.Errorf("remove link %s: %w", p, err)
	}
	return unix.Fsync(dirfd)
}

// removeIfKind removes dirfd/tmp, the old node after an exchange, when it
// has the kind want. Otherwise it exchanges it back with name.
func removeIfKind(dirfd int, tmp, name, p, want string) error {
	kind, _, err := readLinkAt(dirfd, tmp, p)
	if err == nil && kind == want {
		if err := unix.Unlinkat(dirfd, tmp, 0); err != nil {
			return fmt.Errorf("remove old node of %s: %w", p, err)
		}
		return nil
	}
	if uerr := rename2(dirfd, tmp, name, unix.RENAME_EXCHANGE, p); uerr != nil {
		return fmt.Errorf("%s changed while it was applied; the old node is kept as %s", p, tmp)
	}
	unlinkTempLink(dirfd, tmp) // our new link, back under tmp
	return fmt.Errorf("%s changed while it was applied; plan again", p)
}
