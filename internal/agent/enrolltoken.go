package agent

// The one-shot enrollment token of a repair or an install command
// (D-390, ARCHITECTURE §19). The install script writes it to
// wire.RepairTokenFile (root, 0600) and restarts the agent. At its start
// the agent reads it and deletes the file at once, whatever comes next.
// Only a successful enrollment with it replaces the stored credential: a
// refused token (expired, used, unknown) or a controller that stays out of
// reach for oneShotKeep leaves the old credential as it was (review R1 of
// #290). The token is never logged.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// oneShotKeep is how long the agent tries a one-shot token: the life of
// an enrollment token (link.TokenTTL).
const oneShotKeep = 15 * time.Minute

// maxOneShotBytes bounds the read of the token file.
const maxOneShotBytes = 256

// oneShot is a one-shot token in memory.
type oneShot struct {
	token string
	at    time.Time // when the agent read it
}

// oneShotPath is the token file under Options.Root.
func (a *Agent) oneShotPath() string {
	if a.opt.RepairTokenFile != "" {
		return a.opt.RepairTokenFile
	}
	return filepath.Join(a.opt.Root, wire.RepairTokenFile)
}

// takeOneShot reads and deletes the one-shot token file. A file that is
// not a regular file of the agent's own user with no group or other bits
// is ignored (and deleted too). It returns nil when there is no usable
// token.
func (a *Agent) takeOneShot() *oneShot {
	p := a.oneShotPath()
	tok, err := readOneShot(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if rerr := os.Remove(p); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		a.log.Error("could not delete the one-shot enrollment token file", "path", p, "err", rerr)
	}
	if err != nil {
		a.log.Warn("one-shot enrollment token file ignored", "path", p, "err", err)
		return nil
	}
	a.log.Info("one-shot enrollment token found; enrolling with it", "path", p)
	return &oneShot{token: tok, at: time.Now()}
}

// readOneShot returns the token of the file at p. Its errors never hold
// the content.
func readOneShot(p string) (string, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "", fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !fi.Mode().IsRegular():
		return "", errors.New("not a regular file")
	case fi.Mode().Perm()&0o077 != 0:
		return "", fmt.Errorf("mode %04o: group or others can read it", fi.Mode().Perm())
	case !ok || int(st.Uid) != os.Geteuid():
		return "", errors.New("not owned by the agent's user")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxOneShotBytes+1))
	if err != nil {
		return "", err
	}
	t := strings.TrimSpace(string(b))
	if len(b) > maxOneShotBytes || t == "" ||
		strings.Trim(t, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return "", errors.New("the content is not a token")
	}
	return t, nil
}

// tryOneShot enrolls with the one-shot token. It reports whether the
// credential changed (the caller sends a new hello). A refusal drops the
// token; a failed call keeps it for the next round until oneShotKeep.
// The stored credential is never dropped here.
func (a *Agent) tryOneShot(ctx context.Context) bool {
	o := a.oneShot
	if o == nil {
		return false
	}
	if time.Since(o.at) > oneShotKeep {
		a.log.Warn("one-shot enrollment token not used: the controller was not reached in time; keeping the old credential")
		a.oneShot = nil
		return false
	}
	resp, err := a.client.enroll(ctx, wire.EnrollRequest{Token: o.token, Hostname: a.opt.Hostname, Version: a.opt.Version})
	if err != nil {
		if errors.Is(err, errUnauthorized) || permanent(err) {
			a.log.Warn("one-shot enrollment token refused; keeping the old credential", "err", err)
			a.oneShot = nil
		} else {
			a.log.Warn("enroll with the one-shot token failed; trying again", "err", err)
		}
		return false
	}
	a.oneShot = nil
	if _, err := a.state.enrolled(resp.Credential, resp.Build); err != nil {
		// A credential only in memory is lost at the next restart: fail
		// the enrollment and keep the old credential (review n1 of #295).
		// The controller refuses the old one now, so the agent enrolls
		// again from its token source, as after any 401.
		a.log.Error("could not store the credential of the one-shot enrollment; keeping the old credential", "err", err)
		return false
	}
	a.credMu.Lock()
	a.cred = resp.Credential
	a.credMu.Unlock()
	a.log.Info("enrolled with the one-shot token", "agent_id", resp.AgentID)
	return true
}
