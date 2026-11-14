package agent

// The SSH repair line and the repair report (D-383, D-384, ARCHITECTURE
// §19). The controller sends its repair keys with every watch; the agent
// keeps exactly one authorized_keys line per key, removes every other
// plumb-repair- line, and reports the lines, sshd and the host keys.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

const (
	repairFile         = "repair.json"                // the last key set, in the state dir
	authorizedKeysPath = "/root/.ssh/authorized_keys" // under Options.Root
	maxAuthorizedKeys  = 1 << 20
	repairEvery        = 5 * time.Minute // how often a change of sshd or the host keys is looked for
)

// repairState is the repair part of the agent; mu guards it.
type repairState struct {
	mu       sync.Mutex
	watch    *wire.RepairWatch // the last valid key set
	due      bool              // send a report at the next round
	last     *wire.RepairReport
	lastRead time.Time
}

// loadRepair reads the stored key set and writes its lines (at every
// start, D-383).
func (a *Agent) loadRepair() {
	b, err := os.ReadFile(a.state.path(repairFile))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			a.log.Warn("read the repair key set", "err", err)
		}
		return
	}
	var w wire.RepairWatch
	if json.Unmarshal(b, &w) != nil || wire.CheckRepairWatch(w) != nil {
		a.log.Warn("the stored repair key set is not valid; waiting for the next watch")
		return
	}
	a.repair.mu.Lock()
	a.repair.watch = &w
	a.repair.due = true
	a.repair.mu.Unlock()
	if err := writeRepairLines(a.opt.Root, &w); err != nil {
		a.log.Warn("write the repair lines", "err", err)
	}
}

// takeRepairWatch handles WatchBody.Repair. A bad set is refused whole
// (nothing written), so a wrong from never breaks authorized_keys.
func (a *Agent) takeRepairWatch(w *wire.RepairWatch) {
	if w == nil {
		return
	}
	if err := wire.CheckRepairWatch(*w); err != nil {
		a.log.Warn("repair key set refused", "err", err)
		return
	}
	b, _ := json.Marshal(w)
	if err := a.state.write(repairFile, b); err != nil {
		a.log.Warn("store the repair key set", "err", err)
	}
	if err := writeRepairLines(a.opt.Root, w); err != nil {
		a.log.Warn("write the repair lines", "err", err)
	}
	a.repair.mu.Lock()
	a.repair.watch = w
	a.repair.due = true
	a.repair.mu.Unlock()
}

// markRepairDue makes the next round send the report (after a hello).
func (a *Agent) markRepairDue() {
	a.repair.mu.Lock()
	a.repair.due = true
	a.repair.mu.Unlock()
}

// sendRepair sends the repair report when it is due or when it changed
// since the last one; it looks for changes every repairEvery.
func (a *Agent) sendRepair(ctx context.Context, now time.Time) error {
	a.repair.mu.Lock()
	due, last, w := a.repair.due, a.repair.last, a.repair.watch
	look := due || now.Sub(a.repair.lastRead) >= repairEvery
	a.repair.mu.Unlock()
	if !look {
		return nil
	}
	rep := a.repairReport(ctx, w)
	a.repair.mu.Lock()
	a.repair.lastRead = now
	a.repair.mu.Unlock()
	if !due && last != nil && reflect.DeepEqual(*last, rep) {
		return nil
	}
	if err := a.send(ctx, wire.Report{Type: wire.ReportRepair, Repair: &rep}); err != nil {
		if !permanent(err) {
			return err
		}
		// A controller before T-191 does not know the report: no new
		// hello for it, the next try after a change or a hello.
		a.log.Info("repair report refused", "err", err)
	}
	a.repair.mu.Lock()
	a.repair.last, a.repair.due = &rep, false
	a.repair.mu.Unlock()
	return nil
}

// repairReport reads the lines, sshd and the host keys of the guest.
func (a *Agent) repairReport(ctx context.Context, w *wire.RepairWatch) wire.RepairReport {
	fps, types := hostKeys(a.opt.Root)
	rep := wire.RepairReport{Lines: []string{}, HostKeys: fps, HostKeyTypes: types}
	var keysFile bool
	rep.SSHD, keysFile = a.sshdStatus(ctx)
	// A line that sshd does not read is no line (review n1 of #298).
	if w != nil && keysFile {
		rep.Lines = presentLines(a.opt.Root, w)
	}
	return rep
}

// sshdStatus runs `sshd -T`: root login with a forced command must be
// allowed (yes, prohibit-password, without-password, forced-commands-only).
// keysFile reports whether sshd reads root's .ssh/authorized_keys.
func (a *Agent) sshdStatus(ctx context.Context) (st wire.RepairSSHD, keysFile bool) {
	bin := ""
	for _, p := range []string{"/usr/sbin/sshd", "/sbin/sshd"} {
		if fi, err := os.Stat(filepath.Join(a.opt.Root, p)); err == nil && fi.Mode().IsRegular() {
			bin = p
			break
		}
	}
	if bin == "" {
		return wire.RepairSSHD{Status: wire.SSHDAbsent}, false
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := a.opt.Runner.Run(cctx, bin, "-T")
	if err != nil {
		return wire.RepairSSHD{Status: wire.SSHDAbsent}, false
	}
	return parseSSHDT(out)
}

// parseSSHDT reads permitrootlogin, the first port and authorizedkeysfile
// of `sshd -T`. keysFile: one of its entries is root's
// .ssh/authorized_keys (relative to the home, %h/… or /root/…).
func parseSSHDT(out []byte) (st wire.RepairSSHD, keysFile bool) {
	st = wire.RepairSSHD{Status: wire.SSHDAbsent}
	root, port := "", 0
	akfSeen := false
	sc := bufio.NewScanner(io.LimitReader(bytes.NewReader(out), 1<<20))
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if !ok {
			continue
		}
		switch strings.ToLower(k) {
		case "permitrootlogin":
			if root == "" {
				root = strings.TrimSpace(v)
			}
		case "authorizedkeysfile":
			akfSeen = true
			for _, f := range strings.Fields(v) {
				switch f {
				case ".ssh/authorized_keys", "%h/.ssh/authorized_keys", "/root/.ssh/authorized_keys", "~/.ssh/authorized_keys":
					keysFile = true
				}
			}
		case "port":
			if port == 0 {
				if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && p > 0 && p < 65536 {
					port = p
				}
			}
		}
	}
	if !akfSeen {
		keysFile = true // the sshd default is .ssh/authorized_keys
	}
	if port == 0 || root == "" {
		return st, false
	}
	st.Port = port
	switch root {
	case "yes", "prohibit-password", "without-password", "forced-commands-only":
		st.Status = wire.SSHDOk
	default:
		st.Status = wire.SSHDRootRefused
	}
	return st, keysFile
}

// hostKeyFingerprints are the SHA256 fingerprints of the host keys, at
// most wire.MaxRepairHostKeys, sorted.
func hostKeyFingerprints(root string) []string {
	fps, _ := hostKeys(root)
	return fps
}

// hostKeys returns the SHA256 fingerprints of the host keys, at most
// wire.MaxRepairHostKeys, sorted, and the key type of each one whose
// type is in wire.HostKeyTypeList (T-207).
func hostKeys(root string) ([]string, map[string]string) {
	files, _ := filepath.Glob(filepath.Join(root, "/etc/ssh/ssh_host_*_key.pub"))
	sort.Strings(files)
	out := []string{}
	types := map[string]string{}
	for _, f := range files {
		b, err := readSmall(f, 16<<10)
		if err != nil {
			continue
		}
		fp, typ, ok := pubKey(string(b))
		if !ok || slices.Contains(out, fp) {
			continue
		}
		out = append(out, fp)
		if wire.ValidHostKeyType(typ) {
			types[fp] = typ
		}
		if len(out) == wire.MaxRepairHostKeys {
			break
		}
	}
	sort.Strings(out)
	if len(types) == 0 {
		types = nil
	}
	return out, types
}

// pubKeyFingerprint is OpenSSH's SHA256 fingerprint of "type base64 …".
func pubKeyFingerprint(line string) (string, bool) {
	fp, _, ok := pubKey(line)
	return fp, ok
}

// pubKey returns the SHA256 fingerprint of "type base64 …" and the key
// type that the blob names itself (its first SSH string); the text type
// in front is not trusted.
func pubKey(line string) (fp, typ string, ok bool) {
	f := strings.Fields(line)
	if len(f) < 2 {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil || len(raw) == 0 {
		return "", "", false
	}
	sum := sha256.Sum256(raw)
	fp = "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	if len(raw) >= 4 {
		if n := binary.BigEndian.Uint32(raw); n <= 64 && int(n) <= len(raw)-4 {
			typ = string(raw[4 : 4+n])
		}
	}
	return fp, typ, wire.ValidHostKeyFP(fp)
}

func readSmall(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err == nil && int64(len(b)) > max {
		err = fmt.Errorf("%s is larger than %d bytes", path, max)
	}
	return b, err
}

// isRepairLine reports whether an authorized_keys line is a Plumb repair
// line: its last field starts with the prefix.
func isRepairLine(l string) bool {
	f := strings.Fields(l)
	return len(f) > 0 && strings.HasPrefix(f[len(f)-1], wire.RepairLinePrefix)
}

// presentLines lists the key ids whose exact line is in authorized_keys.
func presentLines(root string, w *wire.RepairWatch) []string {
	b, err := readSmall(filepath.Join(root, authorizedKeysPath), maxAuthorizedKeys)
	if err != nil {
		return []string{}
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	out := []string{}
	for _, k := range w.Keys {
		if have[wire.RepairLine(k, w.From)] {
			out = append(out, k.ID)
		}
	}
	return out
}

// writeRepairLines keeps every line of /root/.ssh/authorized_keys that is
// not a repair line and adds one line per key. The dir is made 0700 and
// the file 0600; the file is replaced by a rename, never followed.
func writeRepairLines(root string, w *wire.RepairWatch) error {
	path := filepath.Join(root, authorizedKeysPath)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	var keep []string
	if fi, err := os.Lstat(path); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file; not touched", path)
		}
		b, err := readSmall(path, maxAuthorizedKeys)
		if err != nil {
			return err
		}
		for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if l != "" && !isRepairLine(l) {
				keep = append(keep, l)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, k := range w.Keys {
		keep = append(keep, wire.RepairLine(k, w.From))
	}
	content := ""
	if len(keep) > 0 {
		content = strings.Join(keep, "\n") + "\n"
	}
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		return nil
	}
	tmp, err := os.CreateTemp(dir, ".authorized_keys-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
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

// removeRepairLines removes every repair line (unbind, T-193).
func removeRepairLines(root string) error {
	path := filepath.Join(root, authorizedKeysPath)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return writeRepairLines(root, &wire.RepairWatch{})
}
