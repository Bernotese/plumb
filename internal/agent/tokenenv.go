package agent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// pid1Environ is the environment of PID 1: root only (0400, host C3).
// systemd does not pass the `env` variables of a container to services,
// so the agent reads them here. A test changes it.
var pid1Environ = "/proc/1/environ"

// maxEnviron is the most the agent reads of pid1Environ.
const maxEnviron = 64 << 10

// readToken reads the enrollment token from TokenEnv or TokenFile. An
// error never holds the token or any other value of the environment.
func (a *Agent) readToken() (string, error) {
	if a.cfg.TokenEnv != "" {
		return envToken(pid1Environ, a.cfg.TokenEnv)
	}
	b, err := os.ReadFile(a.cfg.TokenFile)
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", errors.New("token source is empty")
	}
	return token, nil
}

// envToken returns the value of key in a NUL-separated environment file.
// A missing or empty key is "no token yet": the agent retries, as with an
// empty SMBIOS serial. The first entry of the key wins, as for getenv.
func envToken(path, key string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxEnviron+1))
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	if len(b) > maxEnviron {
		return "", fmt.Errorf("read token: the environment of PID 1 is larger than %d KiB", maxEnviron>>10)
	}
	prefix := []byte(key + "=")
	for _, kv := range bytes.Split(b, []byte{0}) {
		if v, ok := bytes.CutPrefix(kv, prefix); ok {
			if token := strings.TrimSpace(string(v)); token != "" {
				return token, nil
			}
			break
		}
	}
	return "", fmt.Errorf("no enrollment token yet: %s is not set in the environment of PID 1", key)
}
