package agent

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
)

// Files in Config.StateDir. Both are root-only (0600 in a 0700 directory):
// the credential is a bearer secret, and the desired list can hold
// sensitive values (D-037).
const (
	credentialFile = "credential"
	desiredFile    = "desired.json"
)

// stateDir stores the agent's files.
type stateDir struct{ dir string }

func (s stateDir) path(name string) string { return filepath.Join(s.dir, name) }

// write replaces a file atomically with mode 0600.
func (s stateDir) write(name string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
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

// credential returns the stored credential, or "" when there is none.
func (s stateDir) credential() (string, error) {
	b, err := os.ReadFile(s.path(credentialFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

func (s stateDir) saveCredential(c string) error { return s.write(credentialFile, []byte(c+"\n")) }

func (s stateDir) dropCredential() error {
	err := os.Remove(s.path(credentialFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
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
