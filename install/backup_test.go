// Tests of the backup parts of install/install.sh (T-114; D-289, D-290,
// D-302 … D-304, D-312 … D-314) against testdata/fakepve.
package install

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const testInstallID = "0f6c2a8e-1b7d-4c55-9a51-3c1f4a2b9d10"

var reUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// backupHost is a fake host with the test hooks of the copy timer, a
// storage "backups" (dir) and a plumb binary that knows "backup verify".
type backupHost struct {
	*host
	sbin, units, conf, store string
}

func newBackupHost(t *testing.T) *backupHost {
	t.Helper()
	h := newHost(t)
	dir := filepath.Dir(h.state)
	b := &backupHost{host: h, sbin: filepath.Join(dir, "sbin"), units: filepath.Join(dir, "units"),
		conf: filepath.Join(dir, "etc", "backup-copy.conf"), store: filepath.Join(dir, "backups")}
	os.MkdirAll(b.store, 0o755)
	h.env = append(h.env, "PLUMB_INSTALL_SBIN="+b.sbin, "PLUMB_INSTALL_UNIT_DIR="+b.units,
		"PLUMB_INSTALL_COPY_CONF="+b.conf, "PLUMB_INSTALL_PVE_MNT="+filepath.Join(dir, "mnt"))
	b.addStorage("backups", "dir", b.store)
	// plumb backup verify FILE: exit FAKE_VERIFY_EXIT, log the file.
	os.WriteFile(h.plumb, []byte("#!/bin/sh\nif [ \"$1 $2\" = \"backup verify\" ]; then echo \"$3\" >>\"$FAKE_STATE/verify\"; exit ${FAKE_VERIFY_EXIT:-0}; fi\necho plumb\n"), 0o755)
	return b
}

func (b *backupHost) addStorage(id, typ, path string) {
	for k, v := range map[string]string{"storage": "", "type": typ, "path": path, "content": "backup"} {
		os.WriteFile(filepath.Join(b.state, k+"."+id), []byte(v+"\n"), 0o644)
	}
}

// lxcFile writes a file into the fake LXC 105.
func (b *backupHost) lxcFile(path string, data []byte) {
	f := filepath.Join(b.state, "ct", "105", path)
	os.MkdirAll(filepath.Dir(f), 0o755)
	os.Remove(f) // never write through a link of an earlier case
	if err := os.WriteFile(f, data, 0o600); err != nil {
		b.t.Fatal(err)
	}
}

// backupFile is a backup file of format 1: the header and some bytes.
func backupFile(installID, node, pool, url string) []byte {
	h, _ := json.Marshal(map[string]any{"v": 1, "created_at": "2026-11-13T03:00:00Z", "install_id": installID,
		"node": node, "pool": pool, "agent_url": url, "kdf": "hkdf-sha256", "cipher": "aes-256-gcm"})
	out := []byte("PLUMBBK\x00")
	out = binary.BigEndian.AppendUint32(out, uint32(len(h)))
	out = append(out, h...)
	return append(out, []byte("ciphertext of the chunks")...)
}

// serveBackup puts a backup and the pointer into the fake LXC.
func (b *backupHost) serveBackup(name string, data []byte) string {
	b.lxcFile("/var/lib/plumb/backups/"+name, data)
	sum := sha256.Sum256(data)
	p, _ := json.Marshal(map[string]any{"name": name, "size": len(data), "sha256": hex.EncodeToString(sum[:])})
	b.lxcFile("/var/lib/plumb/backups/latest", p)
	return hex.EncodeToString(sum[:])
}

func (b *backupHost) stateFile() map[string]string {
	raw, _ := os.ReadFile(b.stateF)
	out := map[string]string{}
	for _, l := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			out[k] = v
		}
	}
	return out
}

func (b *backupHost) install(args ...string) {
	b.t.Helper()
	if code, out, errOut := b.run(asRoot, append([]string{"--yes", "--plumb-binary", b.plumb}, args...)...); code != 0 {
		b.t.Fatalf("install: %d\n%s\n%s", code, out, errOut)
	}
}

// runCopy runs the installed copy script once, like its timer.
func (b *backupHost) runCopy(env ...string) (int, string, string) {
	b.t.Helper()
	cmd := exec.Command("bash", filepath.Join(b.sbin, "plumb-backup-copy"), "--conf", b.conf)
	cmd.Env = append(append([]string{}, b.env...), env...)
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		b.t.Fatal(err)
	}
	return code, out.String(), errOut.String()
}

func (b *backupHost) copyStatus() map[string]any {
	raw, err := os.ReadFile(filepath.Join(b.state, "push", "_var_lib_plumb-copy_status.json"))
	if err != nil {
		return nil
	}
	var st map[string]any
	json.Unmarshal(raw, &st)
	return st
}

// D-304: every install has an install id in the state and in plumb.env,
// and the backup settings of serve; a repair run keeps the id.
func TestInstallID(t *testing.T) {
	b := newBackupHost(t)
	b.install("--backup-time", "04:15", "--backup-keep", "9")
	id := b.stateFile()["install_id"]
	if !reUUID.MatchString(id) {
		t.Fatalf("install_id %q", id)
	}
	env, _ := os.ReadFile(filepath.Join(b.state, "push", "_etc_plumb_plumb.env"))
	for _, l := range []string{"PLUMB_INSTALL_ID=" + id, "PLUMB_BACKUP_TIME=04:15", "PLUMB_BACKUP_KEEP=9"} {
		if !strings.Contains(string(env), l+"\n") {
			t.Errorf("plumb.env lacks %s:\n%s", l, env)
		}
	}
	b.resetCalls()
	b.install("--backup-time", "05:00")
	if got := b.stateFile()["install_id"]; got != id {
		t.Errorf("repair changed the install id: %s → %s", id, got)
	}
	if index(b.calls(), "pct exec 105 -- sh -c test -f /etc/plumb/plumb.env || exit 0; cp -p") < 0 {
		t.Errorf("a new backup time does not reach plumb.env:\n%s", strings.Join(b.calls(), "\n"))
	}
	for _, bad := range [][]string{{"--backup-time", "24:00"}, {"--backup-keep", "0"}, {"--backup-max-size", "1T"},
		{"--backup-storage", "a b"}, {"--keep-guests"}, {"--backup-to", "/x"}} {
		if code, _, _ := b.run(asRoot, append([]string{"--yes", "--plumb-binary", b.plumb}, bad...)...); code == 0 {
			t.Errorf("%v accepted", bad)
		}
	}
}

// D-289, D-304: --backup-storage installs the copy script, its config and
// the timer, and the root-owned status directory in the LXC; "none"
// removes them; --remove removes them.
func TestCopyTimer(t *testing.T) {
	b := newBackupHost(t)
	b.install("--backup-storage", "backups", "--backup-time", "23:45", "--backup-copy-keep", "3", "--backup-max-size", "2G")
	for _, f := range []string{filepath.Join(b.sbin, "plumb-backup-copy"), b.conf, filepath.Join(b.units, "plumb-backup-copy.service"), filepath.Join(b.units, "plumb-backup-copy.timer")} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	timer, _ := os.ReadFile(filepath.Join(b.units, "plumb-backup-copy.timer"))
	if !strings.Contains(string(timer), "OnCalendar=*-*-* 00:15:00 UTC\nPersistent=true") {
		t.Errorf("timer (backup + 30 min over midnight):\n%s", timer)
	}
	conf, _ := os.ReadFile(b.conf)
	for _, l := range []string{"lxc_id=105", "storage=backups", "storage_type=dir", "storage_path=" + b.store, "keep=3", fmt.Sprintf("max_bytes=%d", 2<<30)} {
		if !strings.Contains(string(conf), l+"\n") {
			t.Errorf("config lacks %s:\n%s", l, conf)
		}
	}
	if fi, _ := os.Stat(b.conf); fi.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v", fi.Mode())
	}
	calls := b.calls()
	for _, c := range []string{"pct exec 105 -- install -d -o root -g root -m 0755 /var/lib/plumb-copy", "systemctl enable --now plumb-backup-copy.timer"} {
		if index(calls, c) < 0 {
			t.Errorf("no %q", c)
		}
	}
	if b.stateFile()["backup_storage"] != "backups" {
		t.Errorf("state %v", b.stateFile())
	}
	// A repair run keeps the storage of the state.
	b.install()
	if b.stateFile()["backup_storage"] != "backups" {
		t.Errorf("repair dropped the storage: %v", b.stateFile())
	}
	// A repair run while that storage fails its checks only warns; the
	// timer stays.
	code, out, errOut := b.run(append([]string{"FAKE_STORAGE_INACTIVE=backups"}, asRoot...), "--yes", "--plumb-binary", b.plumb)
	if code != 0 || !strings.Contains(out, "fails its checks now; the copy timer is kept") || b.stateFile()["backup_storage"] != "backups" {
		t.Errorf("repair with an inactive copy storage: %d %s %s %v", code, out, errOut, b.stateFile())
	}
	if _, err := os.Stat(b.conf); err != nil {
		t.Error("the repair removed the timer config")
	}
	b.install("--backup-storage", "none")
	if _, err := os.Stat(b.conf); err == nil || b.stateFile()["backup_storage"] != "" {
		t.Errorf("none kept the timer: %v", b.stateFile())
	}
	b.install("--backup-storage", "backups")
	b.resetCalls()
	if code, out, errOut := b.runRemove("PLUMB_INSTALL_SBIN="+b.sbin, "PLUMB_INSTALL_UNIT_DIR="+b.units, "PLUMB_INSTALL_COPY_CONF="+b.conf); code != 0 {
		t.Fatalf("remove: %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(b.units, "plumb-backup-copy.timer")); err == nil || index(b.calls(), "systemctl disable --now plumb-backup-copy.timer") < 0 {
		t.Error("remove kept the timer")
	}
}

// D-289 N3: a storage of another type, an inactive storage and a share
// that is not mounted are refused before any change.
func TestCopyStorageRefused(t *testing.T) {
	b := newBackupHost(t)
	b.addStorage("pbs", "pbs", "")
	b.addStorage("share", "nfs", "")
	for _, tc := range []struct {
		storage, env, want string
	}{
		{"pbs", "", "has the type pbs"},
		{"missing", "", "does not exist"},
		{"backups", "FAKE_STORAGE_INACTIVE=backups", "not active"},
		{"share", "", "not mounted"},
		{"local-lvm", "", "a storage of Plumb itself"},
	} {
		b.resetCalls()
		env := append([]string{}, asRoot...)
		if tc.env != "" {
			env = append(env, tc.env)
		}
		code, _, errOut := b.run(env, "--yes", "--plumb-binary", b.plumb, "--backup-storage", tc.storage)
		if tc.storage == "missing" && strings.Contains(errOut, "malformed JSON") {
			t.Errorf("missing storage emitted Perl diagnostic: %s", errOut)
		}
		if code == 0 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%s: %d %s", tc.storage, code, errOut)
		}
		for _, c := range b.calls() {
			if mutating.MatchString(c) {
				t.Errorf("%s: changed the host: %s", tc.storage, c)
			}
		}
	}
	// A mounted share passes.
	code, _, errOut := b.run(append([]string{"FAKE_MOUNTED=1"}, asRoot...), "--yes", "--plumb-binary", b.plumb, "--backup-storage", "share")
	if code != 0 {
		t.Errorf("mounted share: %s", errOut)
	}
}

// The config directory stays private, even if writing the config fails.
func TestCopyConfigDirectoryPrivate(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%v", fail), func(t *testing.T) {
			b := newBackupHost(t)
			// Fail after install -d has run, before the config is written.
			if fail {
				wrapper := filepath.Join(b.bin, "install")
				fake, err := os.Readlink(wrapper)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(wrapper); err != nil {
					t.Fatal(err)
				}
				script := "#!/bin/bash\n[[ ${!#} != \"$PLUMB_INSTALL_COPY_CONF\" ]] || exit 3\nexec bash -c 'source \"$FAKE_INSTALL\"' install \"$@\"\n"
				if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				b.env = append(b.env, "FAKE_INSTALL="+fake)
			}
			code, _, errOut := b.run(asRoot, "--yes", "--plumb-binary", b.plumb, "--backup-storage", "backups")
			if (code != 0) != fail {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			info, err := os.Stat(filepath.Dir(b.conf))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o700 {
				t.Errorf("config directory mode: %v", info.Mode())
			}
			for _, call := range b.calls() {
				if strings.HasPrefix(call, "install -d ") && strings.HasSuffix(call, " "+filepath.Dir(b.conf)) && !strings.Contains(call, "-m 0700 ") {
					t.Errorf("directory permissions widened: %s", call)
				}
			}
		})
	}
}

// D-289 N1–N3, D-312: one run of the copy script.
func TestCopyRun(t *testing.T) {
	b := newBackupHost(t)
	b.install("--backup-storage", "backups", "--backup-copy-keep", "2", "--backup-max-size", "1M")
	data := backupFile(testInstallID, "pve1", "plumb", "https://192.168.1.50:8444")
	sum := b.serveBackup("plumb-20261113T030000Z.pbk", data)
	code, out, errOut := b.runCopy()
	copyPath := filepath.Join(b.store, "plumb-backups", "plumb-20261113T030000Z.pbk")
	if code != 0 || strings.TrimSpace(out) != copyPath {
		t.Fatalf("copy: %d %q %s", code, out, errOut)
	}
	got, _ := os.ReadFile(copyPath)
	if string(got) != string(data) {
		t.Error("the copy differs")
	}
	fi, _ := os.Stat(copyPath)
	di, _ := os.Stat(filepath.Dir(copyPath))
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Errorf("modes %v %v", fi.Mode(), di.Mode())
	}
	st := b.copyStatus()
	if st["result"] != "ok" || st["sha256"] != sum || st["storage"] != "backups" {
		t.Errorf("status %v", st)
	}
	opts, _ := os.ReadFile(filepath.Join(b.state, "push", "_var_lib_plumb-copy_status.json.opts"))
	if strings.TrimSpace(string(opts)) != "--perms 0644 --user 0 --group 0" {
		t.Errorf("status push %q", opts)
	}
	// Retention: the newest 2 stay.
	for _, n := range []string{"plumb-20261102T030000Z.pbk", "plumb-20261103T030000Z-2.pbk"} {
		b.serveBackup(n, data)
		if code, _, errOut := b.runCopy(); code != 0 {
			t.Fatal(errOut)
		}
	}
	os.WriteFile(filepath.Join(b.store, "plumb-backups", "notes.txt"), []byte("mine"), 0o600)
	b.runCopy()
	left, _ := filepath.Glob(filepath.Join(b.store, "plumb-backups", "*"))
	var names []string
	for _, f := range left {
		names = append(names, filepath.Base(f))
	}
	if strings.Join(names, " ") != "notes.txt plumb-20261102T030000Z.pbk plumb-20261103T030000Z-2.pbk" {
		t.Errorf("after the retention: %v", names)
	}

	// Each bad input fails, writes no copy and says so in the status.
	ptr := func(name string, size int, sha string) []byte {
		p, _ := json.Marshal(map[string]any{"name": name, "size": size, "sha256": sha})
		return p
	}
	big := make([]byte, 2<<20)
	copy(big, data)
	for _, tc := range []struct {
		name  string
		setup func()
		want  string
	}{
		{"bad name", func() { b.lxcFile("/var/lib/plumb/backups/latest", ptr("../etc/shadow", 10, sum)) }, "names no valid backup"},
		{"name with a path", func() { b.lxcFile("/var/lib/plumb/backups/latest", ptr("plumb-20261104T030000Z.pbk/../x", 10, sum)) }, "names no valid backup"},
		{"big pointer", func() { b.lxcFile("/var/lib/plumb/backups/latest", make([]byte, 5000)) }, "larger than 4 KiB"},
		{"not json", func() { b.lxcFile("/var/lib/plumb/backups/latest", []byte("{")) }, "not valid JSON"},
		{"over the cap", func() { b.serveBackup("plumb-20261104T030000Z.pbk", big) }, "more than the limit"},
		{"other sha", func() {
			b.serveBackup("plumb-20261104T030000Z.pbk", data)
			bad := append([]byte{}, data...)
			bad[len(bad)-1] = 'X'
			b.lxcFile("/var/lib/plumb/backups/plumb-20261104T030000Z.pbk", bad)
		}, "another sha256"},
		{"other size", func() {
			b.serveBackup("plumb-20261104T030000Z.pbk", data)
			b.lxcFile("/var/lib/plumb/backups/plumb-20261104T030000Z.pbk", append(append([]byte{}, data...), 'X'))
		}, "another size"},
		{"no magic", func() { b.serveBackup("plumb-20261104T030000Z.pbk", []byte("PLUMB_ENV=secret value of plumb.env")) }, "not a Plumb backup file"},
		{"a link", func() {
			b.serveBackup("plumb-20261104T030000Z.pbk", data)
			f := filepath.Join(b.state, "ct", "105", "var/lib/plumb/backups/plumb-20261104T030000Z.pbk")
			os.Remove(f)
			os.Symlink("/etc/plumb/plumb.env", f)
		}, "cannot read"},
		{"pointer is a link", func() {
			f := filepath.Join(b.state, "ct", "105", "var/lib/plumb/backups/latest")
			os.Remove(f)
			os.Symlink("/etc/passwd", f)
		}, "cannot read the backup pointer"},
	} {
		tc.setup()
		code, _, errOut := b.runCopy()
		if code == 0 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%s: %d %s", tc.name, code, errOut)
		}
		if _, err := os.Stat(filepath.Join(b.store, "plumb-backups", "plumb-20261104T030000Z.pbk")); err == nil {
			t.Errorf("%s: a copy was written", tc.name)
		}
		if tmp, _ := filepath.Glob(filepath.Join(b.store, "plumb-backups", ".tmp-*")); len(tmp) > 0 {
			t.Errorf("%s: temp files left: %v", tc.name, tmp)
		}
		if st := b.copyStatus(); st["result"] != "failed" || !strings.Contains(fmt.Sprint(st["message"]), strings.Fields(tc.want)[0]) {
			t.Errorf("%s: status %v", tc.name, st)
		}
	}
	// Review M1 of #221: a copy is never replaced, a time far ahead is
	// refused, and the retention follows the host's copy time.
	keepCopy := filepath.Join(b.store, "plumb-backups", "plumb-20261103T030000Z-2.pbk")
	before, _ := os.ReadFile(keepCopy)
	other := append(append([]byte{}, data...), []byte(" replaced")...)
	b.serveBackup("plumb-20261103T030000Z-2.pbk", other)
	if code, _, errOut := b.runCopy(); code == 0 || !strings.Contains(errOut, "refuses to overwrite") {
		t.Errorf("overwrite: %d %s", code, errOut)
	}
	if after, _ := os.ReadFile(keepCopy); string(after) != string(before) {
		t.Error("a copy was replaced")
	}
	b.serveBackup("plumb-20991231T235959Z.pbk", data)
	if code, _, errOut := b.runCopy(); code == 0 || !strings.Contains(errOut, "more than 1 day ahead") {
		t.Errorf("future time: %d %s", code, errOut)
	}
	// A name that sorts last but was copied first goes first.
	old := filepath.Join(b.store, "plumb-backups", "plumb-20991231T000000Z.pbk")
	os.WriteFile(old, data, 0o600)
	past := time.Now().Add(-48 * time.Hour)
	os.Chtimes(old, past, past)
	b.serveBackup("plumb-20261104T030000Z.pbk", data)
	if code, _, errOut := b.runCopy(); code != 0 {
		t.Fatal(errOut)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("the retention kept the oldest copy because of its name")
	}

	// N3 at run time: the storage went inactive.
	b.serveBackup("plumb-20261104T030000Z.pbk", data)
	if code, _, errOut := b.runCopy("FAKE_STORAGE_INACTIVE=backups"); code == 0 || !strings.Contains(errOut, "not active") {
		t.Errorf("inactive at run time: %d %s", code, errOut)
	}
}

// installedWithGuests is an install with the copy timer, a guest in the
// pool and a backup that serve has written.
func installedWithGuests(t *testing.T) *backupHost {
	b := newBackupHost(t)
	b.install("--backup-storage", "backups", "--lxc-ip", "192.168.1.50/24", "--lxc-gw", "192.168.1.1")
	b.serveBackup("plumb-20261113T030000Z.pbk", backupFile(b.stateFile()["install_id"], "pve1", "plumb", "https://192.168.1.50:8444"))
	b.env = append(b.env, `FAKE_POOL_MEMBERS=[{"poolid":"plumb","members":[{"vmid":9000,"type":"qemu"}]}]`)
	return b
}

func (b *backupHost) keepGuests(env []string, args ...string) (int, string, string) {
	return b.run(append(append([]string{}, asRoot...), env...), append([]string{"--remove", "--keep-guests", "--yes", "--plumb-binary", b.plumb}, args...)...)
}

// D-290, D-304: --remove --keep-guests takes a backup, copies it off,
// checks it with the recovery key, then removes only the controller.
func TestRemoveKeepGuests(t *testing.T) {
	b := installedWithGuests(t)
	id := b.stateFile()["install_id"]
	b.resetCalls()
	code, out, errOut := b.keepGuests(nil)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	calls := b.calls()
	order := []string{"pct exec 105 -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb backup now --data /var/lib/plumb",
		"pct exec 105 -- dd if=/var/lib/plumb/backups/plumb-20261113T030000Z.pbk", "pct destroy 105", "pveum user token remove plumb@pve plumb"}
	last := -1
	for _, c := range order {
		i := index(calls, c)
		if i <= last {
			t.Fatalf("%q at %d, after %d:\n%s", c, i, last, strings.Join(calls, "\n"))
		}
		last = i
	}
	verified, _ := os.ReadFile(filepath.Join(b.state, "verify"))
	copyPath := filepath.Join(b.store, "plumb-backups", "plumb-20261113T030000Z.pbk")
	if strings.TrimSpace(string(verified)) != copyPath || !strings.Contains(out, "The backup is at "+copyPath) {
		t.Errorf("verify %q, out %s", verified, out)
	}
	// Kept: pool, user with its ACLs, roles, storages, snippet.
	for _, f := range []string{"pool.plumb", "user.plumb@pve", "role.PlumbGuests", "storage.plumb-snippets", "storage.plumb-images"} {
		if _, err := os.Stat(filepath.Join(b.state, f)); err != nil {
			t.Errorf("%s removed", f)
		}
	}
	if _, err := os.Stat(filepath.Join(b.snippet, "snippets", "plumb-vendor.yaml")); err != nil {
		t.Error("the vendor snippet was removed")
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "pveum user delete") || strings.HasPrefix(c, "pveum pool delete") || strings.HasPrefix(c, "pvesm remove") || strings.HasPrefix(c, "pveum role delete") {
			t.Errorf("removed: %s", c)
		}
	}
	if _, err := os.Stat(filepath.Join(b.units, "plumb-backup-copy.timer")); err == nil {
		t.Error("the copy timer stays without an LXC")
	}
	st := b.stateFile()
	if st["lxc_id"] != "" || st["kept_guests"] != "1" || st["install_id"] != id || st["node"] != "pve1" || st["pool"] != "plumb" || st["old_address"] != "192.168.1.50/24" || st["old_gateway"] != "192.168.1.1" {
		t.Errorf("kept state %v", st)
	}
	if !strings.Contains(out, "--restore "+copyPath+" --lxc-ip 192.168.1.50/24 --lxc-gw 192.168.1.1") {
		t.Errorf("no exact restore line:\n%s", out)
	}
	// A plain install into the kept pool is refused.
	code, _, errOut = b.run(asRoot, "--yes", "--plumb-binary", b.plumb)
	if code == 0 || !strings.Contains(errOut, "Use --restore") {
		t.Errorf("plain install into a pool with guests: %d %s", code, errOut)
	}
}

// Every failure before the removal changes nothing (D-290 N4).
func TestRemoveKeepGuestsStopsEarly(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		args []string
		prep func(*backupHost)
		want string
	}{
		{"wrong key", []string{"FAKE_VERIFY_EXIT=3"}, nil, nil, "wrong recovery key"},
		{"verify fails", []string{"FAKE_VERIFY_EXIT=1"}, nil, nil, "verify failed"},
		{"backup now fails", []string{"FAKE_BACKUP_NOW_FAIL=1"}, nil, nil, "plumb backup now failed"},
		{"no backup", nil, nil, func(b *backupHost) { os.RemoveAll(filepath.Join(b.state, "ct")) }, "could not be copied"},
		{"storage gone", []string{"FAKE_STORAGE_INACTIVE=backups"}, nil, nil, "not active"},
		{"no copy target", nil, nil, func(b *backupHost) { b.install("--backup-storage", "none") }, "--backup-storage ID first"},
		{"backup-to not root", []string{"FAKE_DIR_OWNER=jannes"}, []string{"--backup-to", "/tmp"}, nil, "not owned by root"},
		{"backup-to in vz", nil, []string{"--backup-to", "/var/lib/vz"}, nil, "not an existing directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := installedWithGuests(t)
			if tc.prep != nil {
				tc.prep(b)
			}
			b.resetCalls()
			code, _, errOut := b.keepGuests(tc.env, tc.args...)
			if code == 0 || !strings.Contains(errOut, tc.want) {
				t.Fatalf("%d %s", code, errOut)
			}
			for _, c := range b.calls() {
				if strings.HasPrefix(c, "pct stop") || strings.HasPrefix(c, "pct destroy") || strings.HasPrefix(c, "pveum") && strings.Contains(c, "remove") {
					t.Errorf("removed although it failed: %s", c)
				}
			}
			if _, err := os.Stat(filepath.Join(b.state, "lxc.105")); err != nil {
				t.Error("the LXC is gone")
			}
		})
	}
}

// --backup-to: a directory on this host, with a warning.
func TestRemoveKeepGuestsBackupTo(t *testing.T) {
	b := installedWithGuests(t)
	b.install("--backup-storage", "none")
	to := t.TempDir()
	code, out, errOut := b.keepGuests(nil, "--backup-to", to)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(to, "plumb-backups", "plumb-20261113T030000Z.pbk")); err != nil {
		t.Error("no copy in --backup-to")
	}
	if !strings.Contains(out, "not an off-host copy") {
		t.Errorf("no warning:\n%s", out)
	}
	if st := b.copyStatus(); st["storage"] != "" || st["result"] != "ok" {
		t.Errorf("status %v", st)
	}
}

// keptHost is a host after --remove --keep-guests.
func keptHost(t *testing.T) (*backupHost, string) {
	b := installedWithGuests(t)
	if code, out, errOut := b.keepGuests(nil); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	return b, filepath.Join(b.store, "plumb-backups", "plumb-20261113T030000Z.pbk")
}

func (b *backupHost) restore(env []string, file string, args ...string) (int, string, string) {
	return b.run(append(append([]string{}, asRoot...), env...), append([]string{"--yes", "--plumb-binary", b.plumb, "--restore", file}, args...)...)
}

// D-290, D-304: --restore into the kept pool.
func TestRestore(t *testing.T) {
	b, file := keptHost(t)
	id := b.stateFile()["install_id"]
	b.resetCalls()
	code, out, errOut := b.restore(nil, file, "--lxc-ip", "192.168.1.50/24", "--lxc-gw", "192.168.1.1", "--backup-storage", "backups")
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	calls := b.calls()
	order := []string{"pveum user token add plumb@pve plumb", "pveum acl modify / --tokens plumb@pve!plumb --roles PlumbAudit",
		"pct create 105", "pct exec 105 -- install -d -o root -g plumb -m 0750 /var/lib/plumb-restore",
		"pct push 105 " + file + " /var/lib/plumb-restore/.restore.pbk.plumb-new",
		"lxc-attach -n 105 --clear-env -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb key restore --data /var/lib/plumb",
		"lxc-attach -n 105 --clear-env -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb restore --data /var/lib/plumb --from /var/lib/plumb-restore/restore.pbk",
		"pct exec 105 -- rm -rf -- /var/lib/plumb-restore", "pct exec 105 -- systemctl restart plumb.service"}
	last := -1
	for _, c := range order {
		i := index(calls, c)
		if i <= last {
			t.Fatalf("%q at %d, after %d:\n%s", c, i, last, strings.Join(calls, "\n"))
		}
		last = i
	}
	if index(calls, keyInitCall) >= 0 {
		t.Error("plumb key init on a restore")
	}
	verified, _ := os.ReadFile(filepath.Join(b.state, "verify"))
	if !strings.Contains(string(verified), file) {
		t.Errorf("no host verify of %s: %q", file, verified)
	}
	env, _ := os.ReadFile(filepath.Join(b.state, "push", "_etc_plumb_plumb.env"))
	if !strings.Contains(string(env), "PLUMB_INSTALL_ID="+id+"\n") {
		t.Errorf("plumb.env:\n%s", env)
	}
	st := b.stateFile()
	if st["lxc_id"] != "105" || st["kept_guests"] != "" || st["install_id"] != id || st["backup_storage"] != "backups" {
		t.Errorf("state %v", st)
	}
	if strings.Contains(out, "pw-from-journal") {
		t.Error("the old first-start password is shown on a restore")
	}
}

// D-290 N7, D-304: each wrong restore is refused before any change.
func TestRestoreRefused(t *testing.T) {
	b, file := keptHost(t)
	other := filepath.Join(t.TempDir(), "other.pbk")
	write := func(id, node, pool, url string) {
		os.WriteFile(other, backupFile(id, node, pool, url), 0o600)
	}
	net := []string{"--lxc-ip", "192.168.1.50/24", "--lxc-gw", "192.168.1.1"}
	for _, tc := range []struct {
		name string
		prep func()
		file string
		args []string
		env  []string
		want string
	}{
		{"dhcp", nil, file, nil, nil, "--restore needs --lxc-ip CIDR"},
		{"other address", nil, file, []string{"--lxc-ip", "192.168.1.51/24", "--lxc-gw", "192.168.1.1"}, nil, "differs from the address in the backup"},
		{"other install", func() { write(testInstallID, "pve1", "plumb", "https://192.168.1.50:8444") }, other, net, nil, "belong to install"},
		{"other node", func() { write(testInstallID, "pve9", "plumb", "https://192.168.1.50:8444") }, other, net, nil, "made on node pve9"},
		{"other pool", func() { write(testInstallID, "pve1", "lab", "https://192.168.1.50:8444") }, other, net, nil, "of pool lab"},
		{"no backup file", func() { os.WriteFile(other, []byte("PLUMB_PVE_SECRET=x"), 0o600) }, other, net, nil, "not a Plumb backup file"},
		{"bad url", func() { write(testInstallID, "pve1", "plumb", "https://evil;rm:8444") }, other, net, nil, "no agent URL"},
		{"wrong key", nil, file, net, []string{"FAKE_VERIFY_EXIT=3"}, "wrong recovery key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.prep != nil {
				tc.prep()
			}
			b.resetCalls()
			code, _, errOut := b.restore(tc.env, tc.file, tc.args...)
			if code == 0 || !strings.Contains(errOut, tc.want) {
				t.Fatalf("%d %s", code, errOut)
			}
			for _, c := range b.calls() {
				if mutating.MatchString(c) {
					t.Errorf("changed the host: %s", c)
				}
			}
		})
	}
	// A pool with guests and no kept state: refused.
	os.WriteFile(b.stateF, nil, 0o600)
	if code, _, errOut := b.restore(nil, file, net...); code == 0 || !strings.Contains(errOut, "no kept state") {
		t.Errorf("no kept state: %d %s", code, errOut)
	}
}

// Review M2 of #221: a restore that failed in its LXC; the same
// --restore again removes that LXC first and passes.
func TestRestoreAgainAfterFailure(t *testing.T) {
	b, file := keptHost(t)
	net := []string{"--lxc-ip", "192.168.1.50/24", "--lxc-gw", "192.168.1.1"}
	code, _, errOut := b.restore([]string{"FAKE_RESTORE_FAIL=1"}, file, net...)
	if code == 0 || !strings.Contains(errOut, "Run the same install.sh --restore again") {
		t.Fatalf("failed restore: %d %s", code, errOut)
	}
	st := b.stateFile()
	if st["restoring"] != "1" || st["lxc_id"] != "105" || st["kept_guests"] != "1" {
		t.Fatalf("state after the failure: %v", st)
	}
	// T-130: the backup file and its directory leave the LXC also after a
	// failed restore.
	if index(b.calls(), "pct exec 105 -- rm -rf -- /var/lib/plumb-restore") < 0 {
		t.Error("the backup file stayed in the LXC after the failed restore")
	}
	if left, _ := filepath.Glob(filepath.Join(b.state, "push", "_var_lib_plumb-restore_*")); len(left) != 0 {
		t.Errorf("left in the LXC: %v", left)
	}
	b.resetCalls()
	code, out, errOut := b.restore(nil, file, net...)
	if code != 0 {
		t.Fatalf("second restore: %d\n%s\n%s", code, out, errOut)
	}
	calls := b.calls()
	if d, c := index(calls, "pct destroy 105"), index(calls, "pct create 105"); d < 0 || c < d {
		t.Errorf("the half-made LXC was not destroyed first:\n%s", strings.Join(calls, "\n"))
	}
	st = b.stateFile()
	if st["restoring"] != "" || st["kept_guests"] != "" || st["lxc_id"] != "105" {
		t.Errorf("state after the restore: %v", st)
	}
	if !strings.Contains(out, "No backup copy timer is set") {
		t.Errorf("no hint for the copy timer:\n%s", out)
	}
	// A restored LXC (it has a database) is never destroyed by --restore.
	b2, file2 := keptHost(t)
	b2.restore([]string{"FAKE_RESTORE_FAIL=0", "FAKE_SIGNAL_ON_RESTART=TERM"}, file2, net...)
	if b2.stateFile()["restoring"] == "1" {
		code, _, errOut = b2.restore(nil, file2, net...)
		if code == 0 || !strings.Contains(errOut, "has a restored database") {
			t.Errorf("restored LXC: %d %s", code, errOut)
		}
	}
}

// A restore while the Plumb LXC still runs is refused.
func TestRestoreNeedsKeptLXCGone(t *testing.T) {
	b := installedWithGuests(t)
	file := filepath.Join(t.TempDir(), "b.pbk")
	os.WriteFile(file, backupFile(b.stateFile()["install_id"], "pve1", "plumb", "https://192.168.1.50:8444"), 0o600)
	code, _, errOut := b.restore(nil, file, "--lxc-ip", "192.168.1.50/24", "--lxc-gw", "192.168.1.1")
	if code == 0 || !strings.Contains(errOut, "a Plumb LXC (105) exists") {
		t.Errorf("%d %s", code, errOut)
	}
}

// Dry runs of each path change nothing and list the steps.
func TestBackupDryRuns(t *testing.T) {
	b := installedWithGuests(t)
	b.resetCalls()
	code, out, errOut := b.keepGuests(nil, "--dry-run")
	if code != 0 || !strings.Contains(out, "+ pct destroy 105") || !strings.Contains(out, "plumb backup now") {
		t.Fatalf("keep-guests dry run: %d\n%s\n%s", code, out, errOut)
	}
	for _, c := range b.calls() {
		if mutating.MatchString(c) || strings.HasPrefix(c, "systemctl") {
			t.Errorf("dry run changed: %s", c)
		}
	}
	b2, file := keptHost(t)
	b2.resetCalls()
	code, out, errOut = b2.restore(nil, file, "--dry-run", "--lxc-ip", "192.168.1.50/24", "--lxc-gw", "192.168.1.1", "--backup-storage", "backups")
	if code != 0 || !strings.Contains(out, "plumb key restore") || !strings.Contains(out, "systemctl enable --now plumb-backup-copy.timer") {
		t.Fatalf("restore dry run: %d\n%s\n%s", code, out, errOut)
	}
	for _, c := range b2.calls() {
		if mutating.MatchString(c) || strings.HasPrefix(c, "systemctl") {
			t.Errorf("dry run changed: %s", c)
		}
	}
}
