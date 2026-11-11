package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Bernotese/plumb/internal/wire"
)

// Files in Config.StateDir. Both are root-only (0600 in a 0700 directory):
// the credential is a bearer secret, and the desired list can hold
// sensitive values (D-037).
const (
	credentialFile = "credential"
	desiredFile    = "desired.json"
)

// Tests replace these: removeFile to force a cleanup error, agentUID to
// test the owner check without root.
var (
	removeFile = os.Remove
	agentUID   = os.Geteuid
)

// stateDir stores the agent's files.
type stateDir struct{ dir string }

func (s stateDir) path(name string) string { return filepath.Join(s.dir, name) }

// ensure creates the state dir with mode 0700, or checks an existing one:
// a real directory (no symlink) owned by the agent's user with mode 0700.
// It never changes an existing directory, so a wrong state_dir cannot break
// the guest; New refuses to start instead (D-161).
func (s stateDir) ensure() error {
	fi, err := os.Lstat(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(s.dir), 0o755); err != nil {
			return err
		}
		if err := os.Mkdir(s.dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		fi, err = os.Lstat(s.dir)
	}
	if err != nil {
		return err
	}
	uid := agentUID()
	// A symlink or a non-directory cannot be repaired in place: only a new
	// state_dir helps. Owner and mode can be fixed with chown and chmod.
	var problem, fix string
	repair := fmt.Sprintf("chown %d %s && chmod 0700 %s, or set state_dir to a new path", uid, s.dir, s.dir)
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		problem, fix = "is a symlink", "set state_dir to a new path"
	case !fi.IsDir():
		problem, fix = "is not a directory", "set state_dir to a new path"
	case !ok || int(st.Uid) != uid:
		problem, fix = "is not owned by the agent user", repair
	case fi.Mode().Perm() != 0o700:
		problem, fix = fmt.Sprintf("has mode %04o", fi.Mode().Perm()), repair
	default:
		return nil
	}
	return fmt.Errorf("state dir %s %s: it must be a real directory owned by uid %d with mode 0700 (fix: %s)",
		s.dir, problem, uid, fix)
}

func (s stateDir) cleanStaleTemps() error {
	if err := s.ensure(); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	var errs error // one bad file does not stop the cleanup of the others
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".desired.json-") && !strings.HasPrefix(name, ".credential-") {
			continue
		}
		info, err := entry.Info() // Info does not follow symlinks.
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		if info.Mode().IsRegular() {
			if err := removeFile(s.path(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = errors.Join(errs, err)
			}
		}
	}
	return errs
}

// write replaces a file atomically with mode 0600.
func (s stateDir) write(name string, data []byte) error {
	if err := s.ensure(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "."+name+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path(name))
}

// identity is the content of the credential file. The marks of seal
// (D-207, review S1 of T-039) are in the same file as the credential, so
// one atomic write changes them together:
//   - Build: the enrollment answer said build, at a first enrollment from
//     an empty state. Only then may the agent seal.
//   - Watch: the agent got a watch command once. It is never cleared,
//     also not by an enrollment or a restart; a watched agent never seals.
//   - Enrolled: the agent enrolled once. It stays after a 401, so a later
//     enrollment is never "first".
//
// The file is JSON. A file of an older agent holds only the credential
// (plain text); it reads as enrolled, without the build mark.
type identity struct {
	Credential string `json:"credential"`
	Build      bool   `json:"build,omitempty"`
	Watch      bool   `json:"watch,omitempty"`
	Enrolled   bool   `json:"enrolled,omitempty"`
}

// identity reads the credential file. A missing file is the empty state.
func (s stateDir) identity() (identity, bool, error) {
	b, err := os.ReadFile(s.path(credentialFile))
	if errors.Is(err, fs.ErrNotExist) {
		return identity{}, false, nil
	}
	if err != nil {
		return identity{}, true, err
	}
	t := strings.TrimSpace(string(b))
	if !strings.HasPrefix(t, "{") {
		return identity{Credential: t, Enrolled: t != ""}, true, nil
	}
	var id identity
	if err := json.Unmarshal(b, &id); err != nil {
		return identity{}, true, fmt.Errorf("credential file: %w", err)
	}
	return id, true, nil
}

func (s stateDir) saveIdentity(id identity) error {
	b, err := json.Marshal(id)
	if err != nil {
		return err
	}
	return s.write(credentialFile, append(b, '\n'))
}

// credential returns the stored credential, or "" when there is none.
func (s stateDir) credential() (string, error) {
	id, _, err := s.identity()
	return id.Credential, err
}

// saveCredential stores the credential of a normal enrollment: the build
// mark is cleared, the watch mark stays.
func (s stateDir) saveCredential(c string) error {
	_, err := s.enrolled(c, false)
	return err
}

// enrolled stores the credential of an enrollment and returns the stored
// identity. build is taken only from an empty state: no credential file
// (never enrolled, never a watch) and no desired list (review S1 of T-058).
// Every other enrollment clears the build mark. The watch mark stays.
func (s stateDir) enrolled(cred string, build bool) (identity, error) {
	old, exists, err := s.identity()
	if err != nil {
		return identity{}, err
	}
	_, derr := os.Lstat(s.path(desiredFile))
	empty := !exists && errors.Is(derr, fs.ErrNotExist)
	id := identity{Credential: cred, Build: build && empty, Watch: old.Watch, Enrolled: true}
	return id, s.saveIdentity(id)
}

// markWatch sets the watch mark for good and clears the build mark.
func (s stateDir) markWatch() error {
	id, _, err := s.identity()
	if err != nil {
		return err
	}
	if id.Watch && !id.Build {
		return nil
	}
	id.Watch, id.Build = true, false
	return s.saveIdentity(id)
}

// dropCredential forgets the credential and keeps the marks: a 401 must
// not make a watched agent look new.
func (s stateDir) dropCredential() error {
	id, exists, err := s.identity()
	if err != nil || !exists {
		return err
	}
	id.Credential = ""
	return s.saveIdentity(id)
}

// desired returns the stored desired list (empty when there is none).
func (s stateDir) desired() ([]wire.Resource, error) {
	b, err := os.ReadFile(s.path(desiredFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []wire.Resource
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (s stateDir) saveDesired(list []wire.Resource) error {
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return s.write(desiredFile, b)
}
