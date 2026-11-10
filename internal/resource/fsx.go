package resource

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

// Safe path handling for file and directory (D-054). A guest user who owns
// a directory on a managed path must not be able to point the root agent at
// another file. So a path may hold no symlink in any component. The path is
// resolved one component at a time with O_NOFOLLOW from the Env.Root fd,
// and after that every call works on a file descriptor; no call takes the
// path a second time.

// errNoParent means that a parent directory of the path does not exist.
var errNoParent = errors.New("parent directory does not exist")

// splitPath checks a guest path again (the agent does not trust the wire)
// and returns its components.
func splitPath(p string) ([]string, error) {
	if !strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return nil, fmt.Errorf("path %q must be absolute and hold no NUL", p)
	}
	parts := strings.Split(p[1:], "/")
	for _, c := range parts {
		if c == "" || c == "." || c == ".." {
			return nil, fmt.Errorf("path %q has an empty, '.' or '..' component", p)
		}
	}
	return parts, nil
}

// openParent opens the parent directory of the guest path p without
// following a symlink in any component. It returns the directory fd (the
// caller closes it) and the last component. A missing parent gives an
// error that wraps errNoParent.
func openParent(root, p string) (int, string, error) {
	parts, err := splitPath(p)
	if err != nil {
		return -1, "", err
	}
	// Env.Root is set by the agent, not by the guest: it may be followed.
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", fmt.Errorf("open root: %w", err)
	}
	walked := ""
	for _, c := range parts[:len(parts)-1] {
		walked += "/" + c
		next, err := unix.Openat(fd, c, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			err = componentError(fd, c, walked, err)
			unix.Close(fd)
			return -1, "", err
		}
		unix.Close(fd)
		fd = next
	}
	return fd, parts[len(parts)-1], nil
}

// componentError explains why a component could not be opened. It names
// the component but never the link target: a guest user controls that text.
func componentError(dirfd int, name, walked string, err error) error {
	var st unix.Stat_t
	if serr := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); serr == nil {
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFLNK:
			return fmt.Errorf("path component %s is a symlink; use the real path", walked)
		case unix.S_IFDIR:
		default:
			return fmt.Errorf("path component %s is not a directory", walked)
		}
	}
	if errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("%s: %w", walked, errNoParent)
	}
	return fmt.Errorf("open %s: %w", walked, err)
}

// lstatAt stats the last component without following it. ok is false when
// it does not exist. A symlink is an error.
func lstatAt(dirfd int, name, p string) (st unix.Stat_t, ok bool, err error) {
	err = unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return st, false, nil
	}
	if err != nil {
		return st, false, fmt.Errorf("stat %s: %w", p, err)
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return st, false, fmt.Errorf("path component %s is a symlink; use the real path", p)
	}
	return st, true, nil
}

// openRegular opens an existing regular file with one link. flags are
// added to O_NOFOLLOW|O_NONBLOCK|O_CLOEXEC.
func openRegular(dirfd int, name, p string, flags int) (int, unix.Stat_t, error) {
	var st unix.Stat_t
	fd, err := unix.Openat(dirfd, name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ELOOP) {
		return -1, st, fmt.Errorf("path component %s is a symlink; use the real path", p)
	}
	if err != nil {
		return -1, st, fmt.Errorf("open %s: %w", p, err)
	}
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return -1, st, err
	}
	if err := checkRegular(st, p); err != nil {
		unix.Close(fd)
		return -1, st, err
	}
	return fd, st, nil
}

// checkRegular refuses anything but a regular file with one link: a hard
// link could point root at a file outside the managed directory.
func checkRegular(st unix.Stat_t, p string) error {
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("%s is not a regular file", p)
	}
	if st.Nlink > 1 {
		return fmt.Errorf("%s has %d hard links; Plumb manages only files with one link", p, st.Nlink)
	}
	return nil
}

// openDir opens an existing directory without following a symlink.
func openDir(dirfd int, name, p string) (int, unix.Stat_t, error) {
	var st unix.Stat_t
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, st, componentError(dirfd, name, p, err)
	}
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return -1, st, err
	}
	return fd, st, nil
}

// setOwnerMode sets owner and group (if asked), then the mode, on an fd.
// The mode comes after chown: chown clears the setuid and setgid bits.
func setOwnerMode(fd int, chown bool, uid, gid int, mode uint32) error {
	if chown {
		if err := unix.Fchown(fd, uid, gid); err != nil {
			return err
		}
	}
	return unix.Fchmod(fd, mode)
}

// writeAt replaces dirfd/name with a new file: a temp file in the same
// directory (O_EXCL, O_NOFOLLOW), then renameat. An existing entry must be
// a regular file with one link.
func writeAt(dirfd int, name, p string, data []byte, mode uint32, uid, gid int) error {
	if st, ok, err := lstatAt(dirfd, name, p); err != nil {
		return err
	} else if ok {
		if err := checkRegular(st, p); err != nil {
			return err
		}
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	tmp := ".plumb-" + hex.EncodeToString(suffix)
	fd, err := unix.Openat(dirfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", p, err)
	}
	ok := false
	defer func() {
		if !ok {
			if fd >= 0 {
				unix.Close(fd)
			}
			_ = unix.Unlinkat(dirfd, tmp, 0)
		}
	}()
	for rest := data; len(rest) > 0; {
		n, err := unix.Write(fd, rest)
		if err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
		rest = rest[n:]
	}
	if err := setOwnerMode(fd, true, uid, gid, mode); err != nil {
		return err
	}
	if err := unix.Fsync(fd); err != nil {
		return err
	}
	err = unix.Close(fd)
	fd = -1 // Linux releases the descriptor even if Close returns an error.
	if err != nil {
		return err
	}
	if err := unix.Renameat(dirfd, tmp, dirfd, name); err != nil {
		return err
	}
	ok = true
	return unix.Fsync(dirfd) // make the rename durable
}

// nodeState is the observed mode, owner and group from a stat.
func nodeState(root string, st unix.Stat_t) (State, error) {
	owner, grp, err := ownerNames(root, int(st.Uid), int(st.Gid))
	if err != nil {
		return nil, err
	}
	return State{
		"state": "present",
		"mode":  fmt.Sprintf("%04o", st.Mode&0o7777),
		"owner": owner,
		"group": grp,
	}, nil
}

// isNoParent reports whether err means a missing parent directory.
func isNoParent(err error) bool { return errors.Is(err, errNoParent) }
