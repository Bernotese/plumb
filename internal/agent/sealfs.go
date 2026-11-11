package agent

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"
)

// File work of seal (D-205, review S6 of T-039). A Block runs as root in
// the build guest and can put a symbolic link or a hard link anywhere, for
// example /var/log/x → /etc/shadow. So every call here walks the guest
// path one component at a time with O_NOFOLLOW from the guest root, works
// on file descriptors after that, never writes through a link, never
// truncates a file with more than one link, and never leaves the file
// system of the directory it started in.

// openGuestDir opens the guest directory p (absolute) under root without
// following a link in any component. A missing directory gives an error
// that wraps unix.ENOENT.
func openGuestDir(root, p string) (int, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open root: %w", err)
	}
	for _, c := range strings.Split(strings.Trim(p, "/"), "/") {
		if c == "" {
			continue
		}
		if c == "." || c == ".." {
			unix.Close(fd)
			return -1, fmt.Errorf("bad path %s", p)
		}
		next, err := unix.Openat(fd, c, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return -1, fmt.Errorf("open %s: %w", p, err)
		}
		fd = next
	}
	return fd, nil
}

// splitGuest returns the parent directory and the last component of p.
func splitGuest(p string) (dir, name string) {
	i := strings.LastIndex(p, "/")
	return p[:i], p[i+1:]
}

// removeGuest removes the guest path p: a file or a link is unlinked (a
// link, never its target), a directory is removed with its contents on
// the same file system. A missing path or parent is no error.
func removeGuest(root, p string) error {
	dir, name := splitGuest(p)
	dfd, err := openGuestDir(root, dir)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	return removeAt(dfd, name, p)
}

// removeAt removes dirfd/name without following a link.
func removeAt(dirfd int, name, p string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", p, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		if err := unix.Unlinkat(dirfd, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("remove %s: %w", p, err)
		}
		return nil
	}
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", p, err)
	}
	err = emptyDirFd(fd, p, st.Dev)
	unix.Close(fd)
	if err != nil {
		return err
	}
	if err := unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("remove %s: %w", p, err)
	}
	return nil
}

// emptyGuestDir removes everything inside the guest directory p and keeps
// p. A directory on another file system (a mount point) is left as it is.
// A missing directory is no error.
func emptyGuestDir(root, p string) error {
	fd, err := openGuestDir(root, p)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	return emptyDirFd(fd, p, st.Dev)
}

// emptyDirFd removes every entry of the directory fd that is on dev.
func emptyDirFd(fd int, p string, dev uint64) error {
	names, err := dirNames(fd)
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	var errs error
	for _, n := range names {
		var st unix.Stat_t
		if err := unix.Fstatat(fd, n, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if !errors.Is(err, unix.ENOENT) {
				errs = errors.Join(errs, fmt.Errorf("stat %s/%s: %w", p, n, err))
			}
			continue
		}
		if st.Dev != dev {
			continue // a mount point: another file system
		}
		errs = errors.Join(errs, removeAt(fd, n, p+"/"+n))
	}
	return errs
}

// dirNames lists a directory fd without changing its offset for others.
func dirNames(fd int) ([]string, error) {
	dup, err := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(dup), "dir")
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err == io.EOF {
		err = nil
	}
	return names, err
}

// rotated matches the names of rotated logs: syslog.1, syslog.2.gz,
// dpkg.log.old, btmp-20261101, cloud-init.log.xz.
var rotated = regexp.MustCompile(`(\.[0-9]+|\.old|-[0-9]{8})(\.(gz|xz|bz2|zst|lz4))?$|\.(gz|xz|bz2|zst|lz4)$`)

// emptyLogs walks the guest directory p (seal step logs, review S6): a
// rotated file is removed, every other regular file is set to length 0.
// A symbolic link is never followed and never written; a file with more
// than one hard link is unlinked here, never truncated (it can be a name
// of a file outside /var/log); the walk stays on the file system of p.
func emptyLogs(root, p string) error {
	fd, err := openGuestDir(root, p)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	return emptyLogsFd(fd, p, st.Dev, 0)
}

// maxLogDepth bounds the walk; a deeper tree is an error, not a loop.
const maxLogDepth = 32

func emptyLogsFd(fd int, p string, dev uint64, depth int) error {
	if depth > maxLogDepth {
		return fmt.Errorf("%s: directories nested too deep", p)
	}
	names, err := dirNames(fd)
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	var errs error
	for _, n := range names {
		sub := p + "/" + n
		var st unix.Stat_t
		if err := unix.Fstatat(fd, n, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if !errors.Is(err, unix.ENOENT) {
				errs = errors.Join(errs, fmt.Errorf("stat %s: %w", sub, err))
			}
			continue
		}
		if st.Dev != dev {
			continue
		}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			dfd, err := unix.Openat(fd, n, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				errs = errors.Join(errs, fmt.Errorf("open %s: %w", sub, err))
				continue
			}
			errs = errors.Join(errs, emptyLogsFd(dfd, sub, dev, depth+1))
			unix.Close(dfd)
		case unix.S_IFREG:
			if rotated.MatchString(n) || st.Nlink > 1 {
				if err := unix.Unlinkat(fd, n, 0); err != nil && !errors.Is(err, unix.ENOENT) {
					errs = errors.Join(errs, fmt.Errorf("remove %s: %w", sub, err))
				}
				continue
			}
			errs = errors.Join(errs, truncateAt(fd, n, sub, dev))
		default:
			// A link, a socket, a fifo or a device: never opened, never written.
		}
	}
	return errs
}

// truncateAt sets dirfd/name to length 0 when it is still a regular file
// with one link on dev; the checks run on the opened fd.
func truncateAt(dirfd int, name, p string, dev uint64) error {
	fd, err := unix.Openat(dirfd, name, unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
		return nil // gone, or became a link: not ours to write
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", p, err)
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Dev != dev {
		return nil
	}
	if err := unix.Ftruncate(fd, 0); err != nil {
		return fmt.Errorf("truncate %s: %w", p, err)
	}
	return nil
}

// writeGuestFile replaces the guest file p with data (mode 0644): a temp
// file in the same directory, then a rename. The directory is made when
// it is missing (mode 0755); no component may be a link.
func writeGuestFile(root, p string, data []byte) error {
	dir, name := splitGuest(p)
	pdir, base := splitGuest(dir)
	pfd, err := openGuestDir(root, pdir)
	if err != nil {
		return err
	}
	defer unix.Close(pfd)
	if err := unix.Mkdirat(pfd, base, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("make %s: %w", dir, err)
	}
	dfd, err := unix.Openat(pfd, base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	defer unix.Close(dfd)
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	tmp := ".plumb-" + hex.EncodeToString(suffix)
	fd, err := unix.Openat(dfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", p, err)
	}
	f := os.NewFile(uintptr(fd), tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(0o644)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = unix.Renameat(dfd, tmp, dfd, name)
	}
	if err != nil {
		_ = unix.Unlinkat(dfd, tmp, 0)
		return fmt.Errorf("write %s: %w", p, err)
	}
	return nil
}

// readGuestTail returns the last n bytes of the regular guest file p, or
// "" when it cannot be read. It never follows a link.
func readGuestTail(root, p string, n int64) string {
	dir, name := splitGuest(p)
	dfd, err := openGuestDir(root, dir)
	if err != nil {
		return ""
	}
	defer unix.Close(dfd)
	fd, err := unix.Openat(dfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return ""
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	off := max(fi.Size()-n, 0)
	b := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(b, off); err != nil && err != io.EOF {
		return ""
	}
	return string(b)
}

// guestEntries lists the names in the guest directory p, or nil.
func guestEntries(root, p string) []string {
	fd, err := openGuestDir(root, p)
	if err != nil {
		return nil
	}
	defer unix.Close(fd)
	names, _ := dirNames(fd)
	return names
}

// guestRegular reports whether the guest path p is a regular file (no link
// in any component, the last one not a link either).
func guestRegular(root, p string) bool {
	dir, name := splitGuest(p)
	fd, err := openGuestDir(root, dir)
	if err != nil {
		return false
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	return unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT == unix.S_IFREG
}
