package resource

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Helpers for files whose path the resource type sets itself
// (apt_repository, unit, sysctl). They use the same safe path handling as
// the file resource (fsx.go): no symlink in any component, one hard link.

// managedFile is the observed state of such a file.
type managedFile struct {
	exists  bool
	content string
	mode    uint32
	uid     int
	gid     int
}

// readManaged reads the guest file p. A missing file or parent is
// "not exists", not an error.
func readManaged(root, p string) (managedFile, error) {
	dfd, name, err := openParent(root, p)
	if isNoParent(err) {
		return managedFile{}, nil
	}
	if err != nil {
		return managedFile{}, err
	}
	defer unix.Close(dfd)
	if _, ok, err := lstatAt(dfd, name, p); err != nil || !ok {
		return managedFile{}, err
	}
	fd, st, err := openRegular(dfd, name, p, unix.O_RDONLY)
	if err != nil {
		return managedFile{}, err
	}
	fh := os.NewFile(uintptr(fd), p)
	defer fh.Close()
	if st.Size > maxFileSize {
		return managedFile{}, fmt.Errorf("%s is larger than %d bytes", p, maxFileSize)
	}
	data, err := io.ReadAll(io.LimitReader(fh, maxFileSize+1))
	if err != nil {
		return managedFile{}, err
	}
	return managedFile{exists: true, content: string(data), mode: st.Mode & 0o7777, uid: int(st.Uid), gid: int(st.Gid)}, nil
}

// writeManaged writes the guest file p atomically, owned by root. The
// parent directory must exist.
func writeManaged(root, p string, data []byte, mode uint32) error {
	uid, gid, err := ownerIDs(root, "root", "root")
	if err != nil {
		return err
	}
	dfd, name, err := openParent(root, p)
	if err != nil {
		if isNoParent(err) {
			return fmt.Errorf("parent directory of %s does not exist", p)
		}
		return err
	}
	defer unix.Close(dfd)
	return writeAt(dfd, name, p, data, mode, uid, gid)
}

// removeManaged removes the guest file p. A missing file is no error.
func removeManaged(root, p string) error {
	dfd, name, err := openParent(root, p)
	if isNoParent(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	st, ok, err := lstatAt(dfd, name, p)
	if err != nil || !ok {
		return err
	}
	if err := checkRegular(st, p); err != nil {
		return err
	}
	if err := unix.Unlinkat(dfd, name, 0); err != nil {
		return err
	}
	return unix.Fsync(dfd)
}
