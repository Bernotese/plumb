package agent

// Self-update of the agent (T-091, D-258, D-271, D-272).
//
// The old agent downloads the new binary from its pinned controller,
// checks the SHA-256 of the command, runs the self-test of the new
// binary, keeps itself as <binary>.prev, writes the update marker, starts
// the guard and only then renames the new binary over its own. After the
// controller took the result it exits; systemd (Restart=always) starts the
// new binary.
//
// The guard is the old binary (<binary>.prev guard) in its own unit with a
// timer: the agent's cgroup dies at the restart, and a broken new binary
// must not judge itself. The new agent confirms after its first accepted
// hello; without that the guard puts .prev back (guard.go).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// Default paths of the update (wire.IsAgentPath knows all of them).
const (
	DefaultBinaryPath = "/usr/local/bin/plumb-agent"
	DefaultUnitDir    = "/etc/systemd/system"
)

const (
	updateMarkerFile = "update.json"
	updateLockFile   = "update.lock"

	agentService = "plumb-agent.service"
	guardService = "plumb-agent-guard.service"
	guardTimer   = "plumb-agent-guard.timer"

	// maxBinaryBytes bounds the download (the agent is about 20 MiB).
	maxBinaryBytes = 256 << 20
	// downloadTimeout bounds the whole download.
	downloadTimeout = 10 * time.Minute
	// maxSelfTestOutput bounds what the agent reads from a self-test.
	maxSelfTestOutput = 64 << 10
	// crashLoopRestarts restarts of the agent unit since the first guard
	// tick count as a crash loop (D-271).
	crashLoopRestarts = 3
	// restartWait: the agent exits at the latest this long after an
	// update, also when the controller did not take the result.
	restartWait = 60 * time.Second

	markerVersion = 1
)

// selfTestTimeout bounds the self-test of the new binary (a var for tests).
var selfTestTimeout = 30 * time.Second

// ErrRestart ends Run after an update: the process must exit, so that
// systemd starts the new binary.
var ErrRestart = errors.New("agent binary updated; restart")

var (
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// unitPath: the paths in the guard unit need no systemd quoting.
	unitPath = regexp.MustCompile(`^/[A-Za-z0-9/._-]+$`)
)

// updateState is the state in the marker.
type updateState string

const (
	updatePending  updateState = "pending"  // swapped; the new agent did not confirm yet
	updateReverted updateState = "reverted" // the guard put .prev back
)

// updateMarker is <state>/update.json. Its format is a contract between
// agent versions (D-272): a newer agent keeps reading and writing v 1.
type updateMarker struct {
	V           int         `json:"v"`
	State       updateState `json:"state"`
	FromVersion string      `json:"from_version"`
	FromSHA     string      `json:"from_sha"`
	ToVersion   string      `json:"to_version"` // from the self-test
	ToSHA       string      `json:"to_sha"`
	// Ticks counts the guard runs (D-271: a count, not the wall clock).
	Ticks int `json:"ticks"`
	// BootID and RestartsBase: NRestarts of the agent unit at the first
	// guard tick of this boot.
	BootID       string `json:"boot_id,omitempty"`
	RestartsBase *int   `json:"restarts_base,omitempty"`
	// Reason and At of a revert: wire.RevertCrashLoop or wire.RevertNoHello,
	// RFC 3339 UTC.
	Reason string `json:"reason,omitempty"`
	At     string `json:"at,omitempty"`
}

// revert returns the wire form of a reverted marker.
func (m updateMarker) revert() *wire.UpdateRevert {
	return &wire.UpdateRevert{From: m.FromVersion, To: m.ToVersion, ToSHA256: m.ToSHA, Reason: m.Reason, At: m.At}
}

// binPaths names the agent binary and its update files. All are in one
// directory, so every rename is atomic.
type binPaths struct{ bin string }

func (p binPaths) dir() string  { return filepath.Dir(p.bin) }
func (p binPaths) base() string { return filepath.Base(p.bin) }

// prev is the binary before the last update: /usr/local/bin/plumb-agent.prev.
func (p binPaths) prev() string { return p.bin + ".prev" }

// temp is the download: /usr/local/bin/.plumb-agent.update.
func (p binPaths) temp() string { return filepath.Join(p.dir(), "."+p.base()+".update") }

// prevTemp is the new .prev before its rename: /usr/local/bin/.plumb-agent.prev-new.
func (p binPaths) prevTemp() string { return filepath.Join(p.dir(), "."+p.base()+".prev-new") }

// updater does the file and unit work of an update, for the agent and for
// the guard.
type updater struct {
	state   stateDir
	bin     binPaths
	unitDir string
	config  string // the agent config path, for the self-test and the guard unit
	run     resource.Runner
	log     *slog.Logger
}

// lock takes the update lock (D-271): confirm and revert never overlap.
func (u *updater) lock() (func(), error) {
	f, err := os.OpenFile(u.state.path(updateLockFile), os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("update lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("update lock: %w", err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, nil
}

// marker reads the marker; ok is false when there is none.
func (u *updater) marker() (m updateMarker, ok bool, err error) {
	b, err := os.ReadFile(u.state.path(updateMarkerFile))
	if errors.Is(err, fs.ErrNotExist) {
		return m, false, nil
	}
	if err != nil {
		return m, false, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, false, fmt.Errorf("update marker: %w", err)
	}
	return m, true, nil
}

func (u *updater) saveMarker(m updateMarker) error {
	m.V = markerVersion
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return u.state.write(updateMarkerFile, append(b, '\n'))
}

func (u *updater) dropMarker() error {
	err := os.Remove(u.state.path(updateMarkerFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// fileSHA256 hashes a regular file; a link or another type is refused.
func fileSHA256(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	return hashFile(path)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fetchFunc writes the new binary to w.
type fetchFunc func(ctx context.Context, w io.Writer) error

// update installs the binary with SHA-256 want (D-272 order). It returns
// false when the agent already runs it. On an error nothing is changed:
// the old binary stays, no marker, no guard.
func (u *updater) update(ctx context.Context, fetch fetchFunc, want, fromVersion string) (bool, error) {
	if !sha256Hex.MatchString(want) {
		return false, fmt.Errorf("sha256 must be 64 lower-case hex digits")
	}
	if !unitPath.MatchString(u.bin.prev()) || !unitPath.MatchString(u.config) {
		return false, fmt.Errorf("the agent paths %q, %q cannot go into a unit file", u.bin.prev(), u.config)
	}
	unlock, err := u.lock()
	if err != nil {
		return false, err
	}
	defer unlock()
	if m, ok, err := u.marker(); err != nil {
		return false, err
	} else if ok && m.State == updatePending {
		return false, errors.New("an update is in progress; wait for its end")
	} else if ok {
		// A revert is reported until the next update command (wire.Hello.Reverted).
		if err := u.dropMarker(); err != nil {
			return false, err
		}
	}
	cur, err := fileSHA256(u.bin.bin)
	if err != nil {
		return false, fmt.Errorf("current binary: %w", err)
	}
	if cur == want {
		return false, nil
	}

	tmp := u.bin.temp()
	done := false
	defer func() {
		if !done {
			_ = os.Remove(tmp)
		}
	}()
	if err := download(ctx, fetch, tmp, want); err != nil {
		return false, err
	}
	toVersion, err := selfTest(ctx, tmp, u.config)
	if err != nil {
		return false, err
	}

	// Keep the current binary as .prev before anything else (D-272): from
	// the marker on, the guard can always put .prev back.
	if err := linkOver(u.bin.bin, u.bin.prevTemp(), u.bin.prev()); err != nil {
		return false, fmt.Errorf("keep the current binary: %w", err)
	}
	m := updateMarker{State: updatePending, FromVersion: fromVersion, FromSHA: cur, ToVersion: toVersion, ToSHA: want}
	if err := u.saveMarker(m); err != nil {
		return false, fmt.Errorf("update marker: %w", err)
	}
	undo := func() {
		if err := u.removeGuard(context.WithoutCancel(ctx)); err != nil {
			u.log.Warn("remove update guard", "err", err)
		}
		if err := u.dropMarker(); err != nil {
			u.log.Warn("remove update marker", "err", err)
		}
	}
	if err := u.installGuard(ctx); err != nil {
		undo()
		return false, fmt.Errorf("start the update guard: %w", err)
	}
	if err := os.Rename(tmp, u.bin.bin); err != nil {
		undo()
		return false, fmt.Errorf("swap the binary: %w", err)
	}
	done = true
	if err := syncDir(u.bin.dir()); err != nil {
		u.log.Warn("sync binary dir", "err", err)
	}
	return true, nil
}

// download writes the new binary to tmp (0700 while it is written, then
// 0755) and checks its hash.
func download(ctx context.Context, fetch fetchFunc, tmp, want string) error {
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o700)
	if err != nil {
		return err
	}
	h := sha256.New()
	lw := &limitWriter{w: io.MultiWriter(f, h), n: maxBinaryBytes}
	ferr := fetch(ctx, lw)
	if ferr == nil {
		ferr = f.Chmod(0o755) // as plumb-agent-install gives it
	}
	if ferr == nil {
		ferr = f.Sync()
	}
	if cerr := f.Close(); ferr == nil {
		ferr = cerr
	}
	if ferr != nil {
		return fmt.Errorf("download the agent binary: %w", ferr)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("the downloaded binary has sha256 %s, the command wants %s; nothing was changed", got, want)
	}
	return nil
}

// limitWriter fails after n bytes.
type limitWriter struct {
	w io.Writer
	n int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.n {
		return 0, fmt.Errorf("the agent binary is larger than %d bytes", maxBinaryBytes)
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}

// SelfTestReport is what "plumb-agent self-test" prints.
type SelfTestReport struct {
	Version string `json:"version"`
}

// SelfTest is "plumb-agent self-test" in the new binary: it loads the
// config and reads the state dir, writes nothing and uses no network, and
// prints a SelfTestReport.
func SelfTest(configPath, version string, w io.Writer) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	if _, err := os.ReadDir(cfg.StateDir); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	return json.NewEncoder(w).Encode(SelfTestReport{Version: version})
}

// selfTest runs "<bin> self-test" with an empty environment and a timeout,
// and returns the version of the new binary.
func selfTest(ctx context.Context, bin, config string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, selfTestTimeout)
	defer cancel()
	var out, errOut capBuffer
	out.max, errOut.max = maxSelfTestOutput, 512
	cmd := exec.CommandContext(ctx, bin, "self-test", "-config", config)
	cmd.Env = []string{}
	cmd.Dir = "/"
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if ctx.Err() != nil {
			msg = "no answer within " + selfTestTimeout.String()
		}
		return "", fmt.Errorf("self-test of the new binary failed (%v): %s; nothing was changed", err, firstLine(msg))
	}
	var rep SelfTestReport
	if out.full || json.Unmarshal(out.Bytes(), &rep) != nil || rep.Version == "" {
		return "", errors.New("self-test of the new binary gave no valid report; nothing was changed")
	}
	return rep.Version, nil
}

// capBuffer keeps the first max bytes and drops the rest.
type capBuffer struct {
	bytes.Buffer
	max  int
	full bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.Len(); len(p) > room {
		c.full = true
		c.Buffer.Write(p[:max(room, 0)])
		return len(p), nil
	}
	return c.Buffer.Write(p)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// linkOver makes dst a hard link of src, through tmp and a rename.
func linkOver(src, tmp, dst string) error {
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Link(src, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(dst))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// The guard units (D-271). The timer is enabled, so it survives a reboot.
func (u *updater) guardUnits() map[string]string {
	sec := int(guardTick / time.Second)
	return map[string]string{
		guardService: fmt.Sprintf(`[Unit]
Description=Plumb agent update guard

[Service]
Type=oneshot
ExecStart=%s guard -config %s
`, u.bin.prev(), u.config),
		guardTimer: fmt.Sprintf(`[Unit]
Description=Plumb agent update guard

[Timer]
OnActiveSec=%ds
OnUnitActiveSec=%ds
AccuracySec=1s

[Install]
WantedBy=timers.target
`, sec, sec),
	}
}

func (u *updater) installGuard(ctx context.Context) error {
	for name, body := range u.guardUnits() {
		if err := writeFileAtomic(filepath.Join(u.unitDir, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	if _, err := u.run.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, err := u.run.Run(ctx, "systemctl", "enable", "--now", guardTimer)
	return err
}

// removeGuard stops and removes the guard units. It tries every step.
func (u *updater) removeGuard(ctx context.Context) error {
	var errs error
	if _, err := os.Lstat(filepath.Join(u.unitDir, guardTimer)); err == nil {
		if _, err := u.run.Run(ctx, "systemctl", "disable", "--now", guardTimer); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	removed := false
	for _, name := range []string{guardTimer, guardService} {
		err := os.Remove(filepath.Join(u.unitDir, name))
		switch {
		case err == nil:
			removed = true
		case !errors.Is(err, fs.ErrNotExist):
			errs = errors.Join(errs, err)
		}
	}
	if removed {
		if _, err := u.run.Run(ctx, "systemctl", "daemon-reload"); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}

// writeFileAtomic writes a root-owned file through a temp file and a rename.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if err := tmp.Chmod(mode); err != nil {
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
	return os.Rename(tmp.Name(), path)
}

// confirm runs in the agent after an accepted hello. A pending update of
// this binary is confirmed: the marker goes, then the guard. A pending
// marker of another binary stays for the guard. It returns nothing more:
// a reverted marker stays until the next update command (reverted).
func (u *updater) confirm(ctx context.Context, ownSHA string) error {
	if _, err := os.Lstat(u.state.path(updateMarkerFile)); errors.Is(err, fs.ErrNotExist) {
		return nil // the usual case: no lock needed
	}
	unlock, err := u.lock()
	if err != nil {
		return err
	}
	defer unlock()
	m, ok, err := u.marker()
	if err != nil || !ok || m.State != updatePending || ownSHA != m.ToSHA {
		return err
	}
	// The marker first: without it the guard removes itself.
	if err := u.dropMarker(); err != nil {
		return err
	}
	u.log.Info("agent update confirmed", "from", m.FromVersion, "to", m.ToVersion)
	if err := u.removeGuard(ctx); err != nil {
		u.log.Warn("remove update guard; it removes itself", "err", err)
	}
	return nil
}

// reverted returns the revert to report in Hello, or nil.
func (u *updater) reverted() *wire.UpdateRevert {
	m, ok, err := u.marker()
	if err != nil {
		u.log.Warn("read update marker", "err", err)
		return nil
	}
	if !ok || m.State != updateReverted {
		return nil
	}
	return m.revert()
}

// Capabilities is the capability list of this agent build (D-259).
func Capabilities() wire.Capabilities {
	return wire.Capabilities{Commands: wire.AgentCommands(), Resources: resource.Capabilities()}
}

// selfUpdate runs an update command. The Item is applied after the swap
// (the agent then exits), unchanged when it runs that binary already, and
// failed when a step before the swap failed.
func (a *Agent) selfUpdate(ctx context.Context, cmdID string, body wire.UpdateBody) wire.Item {
	item := wire.Item{Resource: wire.AgentBinaryID}
	id := wire.Resource{ID: wire.AgentBinaryID}
	changed, err := a.upd.update(ctx, a.client.download, body.SHA256, a.opt.Version)
	switch {
	case err != nil:
		item.Status, item.Output = wire.StatusFailed, resource.SafeOutput(id, err.Error())
		a.log.Warn("agent update failed", "err", err)
	case !changed:
		item.Status = wire.StatusUnchanged
	default:
		item.Status = wire.StatusApplied
		a.restartCmd, a.restartBy = cmdID, time.Now().Add(restartWait)
		a.log.Info("agent binary swapped", "from", a.opt.Version)
	}
	return item
}
