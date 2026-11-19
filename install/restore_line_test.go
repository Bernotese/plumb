// Tests of the printed restore and repair lines (T-265): they hold every
// flag that shapes the rights and storages of the install, read from the
// state file.
package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// t265Flags: a guest backup storage, one extra bridge and two VLAN tags.
var t265Flags = []string{"--extra-bridge", "vmbr1", "--extra-vlan", "vmbr9:20,30", "--guest-backup-storage", "plumb-backup", "--guest-backup-clear"}

const t265Extra = " --extra-bridge vmbr1 --extra-vlan vmbr9:20,30 --guest-backup-storage plumb-backup --guest-backup-clear"

// dirs: the test host gives every run its own --snippet-dir and --image-dir.
func (b *backupHost) dirs() string {
	return " --snippet-dir " + b.snippet + " --image-dir " + b.images()
}

// t265Host is installedWithGuests with the T-265 flags.
func t265Host(t *testing.T) *backupHost {
	t.Helper()
	b := newBackupHost(t)
	b.host.addStorage("plumb-backup", "dir", "backup", "")
	b.install(append([]string{"--backup-storage", "backups", "--lxc-ip", "192.0.2.50/24", "--lxc-gw", "192.0.2.1"}, t265Flags...)...)
	b.serveBackup("plumb-20261113T030000Z.pbk", backupFile(b.stateFile()["install_id"], "pve1", "plumb", "https://192.0.2.50:8444"))
	b.env = append(b.env, `FAKE_POOL_MEMBERS=[{"poolid":"plumb","members":[{"vmid":9000,"type":"qemu"}]}]`)
	return b
}

// restoreLine is the line after "Restore with:" in out.
func restoreLine(t *testing.T, out string) string {
	t.Helper()
	_, rest, ok := strings.Cut(out, "Restore with:\n")
	if !ok {
		t.Fatalf("no restore line:\n%s", out)
	}
	line, _, _ := strings.Cut(rest, "\n")
	return strings.TrimSpace(line)
}

func (b *backupHost) aclLines() []string {
	raw, _ := os.ReadFile(filepath.Join(b.state, "acl"))
	return sortedLines(string(raw))
}

func TestRemoveKeepGuestsLineHasEveryFlag(t *testing.T) {
	t.Parallel()
	b := t265Host(t)
	before := b.aclLines()
	code, out, errOut := b.keepGuests(nil)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	file := filepath.Join(b.store, "plumb-backups", "plumb-20261113T030000Z.pbk")
	line := restoreLine(t, out)
	t265Want := b.dirs() + t265Extra + " --backup-storage backups"
	want := "install.sh --plumb-binary FILE --restore " + file + " --lxc-ip 192.0.2.50/24 --lxc-gw 192.0.2.1" + t265Want
	if line != want {
		t.Fatalf("restore line:\n%s\nwant:\n%s", line, want)
	}
	if !strings.Contains(out, "Then: install.sh --plumb-binary FILE --restore BACKUP --lxc-ip 192.0.2.50/24 --lxc-gw 192.0.2.1"+t265Want+"\n") {
		t.Errorf("the plan line lacks the flags:\n%s", out)
	}
	for k, v := range map[string]string{"extra_nets": "vmbr1,vmbr9/20,vmbr9/30", "guest_backup_storage": "plumb-backup", "guest_backup_clear": "1", "bridge": "vmbr0"} {
		if got := b.stateFile()[k]; got != v {
			t.Errorf("kept state %s=%q, want %q", k, got, v)
		}
	}

	// A restore from exactly that line gives the same ACL rows, and no warning.
	args := strings.Fields(strings.TrimPrefix(line, "install.sh --plumb-binary FILE --restore "+file))
	code, out, errOut = b.restore(nil, file, args...)
	if code != 0 {
		t.Fatalf("restore from the line: %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, "WARNING: this run's rights flags differ") {
		t.Errorf("warning on the exact line:\n%s", out)
	}
	if after := b.aclLines(); !slices.Equal(after, before) {
		t.Errorf("ACLs after the restore:\n%s\nbefore the remove:\n%s", strings.Join(after, "\n"), strings.Join(before, "\n"))
	}
}

// A restore without the flags of the old install warns before the confirm
// and names the old flags.
func TestRestoreWithoutTheFlagsWarns(t *testing.T) {
	t.Parallel()
	b := t265Host(t)
	if code, out, errOut := b.keepGuests(nil); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	file := filepath.Join(b.store, "plumb-backups", "plumb-20261113T030000Z.pbk")
	// The VLAN tags in another order are the same networks: no warning.
	code, out, errOut := b.run(asRoot, "--dry-run", "--yes", "--plumb-binary", b.plumb, "--restore", file, "--lxc-ip", "192.0.2.50/24", "--lxc-gw", "192.0.2.1",
		"--extra-vlan", "vmbr9:30,20", "--extra-bridge", "vmbr1", "--guest-backup-storage", "plumb-backup", "--guest-backup-clear")
	if strings.Contains(out, "WARNING: this run's rights flags differ") {
		t.Errorf("warning for the same networks: %d\n%s\n%s", code, out, errOut)
	}
	code, out, errOut = b.run(asRoot, "--dry-run", "--yes", "--plumb-binary", b.plumb, "--restore", file, "--lxc-ip", "192.0.2.50/24", "--lxc-gw", "192.0.2.1", "--extra-bridge", "vmbr1")
	if !strings.Contains(out, "WARNING: this run's rights flags differ from those of the old install. The old install had:\n  install.sh --plumb-binary FILE"+b.dirs()+t265Extra+" (plus") {
		t.Errorf("no warning for a restore without the flags: %d\n%s\n%s", code, out, errOut)
	}
}

// A state file from before T-265 has no rights keys: the line names them
// as unknown, and bash refuses to run it.
func TestRemoveKeepGuestsOldStateSaysUnknown(t *testing.T) {
	t.Parallel()
	b := t265Host(t)
	raw, _ := os.ReadFile(b.stateF)
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		k, _, _ := strings.Cut(l, "=")
		if !slices.Contains([]string{"bridge", "disk_storage", "image_storage", "extra_nets", "guest_backup_storage", "guest_backup_clear"}, k) {
			keep = append(keep, l)
		}
	}
	if err := os.WriteFile(b.stateF, []byte(strings.Join(keep, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := b.keepGuests(nil)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	line := restoreLine(t, out)
	want := b.dirs() + " --backup-storage backups <UNKNOWN: " + b.stateF + " does not hold these flags of the install (older than T-265, or not a plain value), add them by hand: --bridge --disk-storage --image-storage --extra-bridge --extra-vlan --guest-backup-storage --guest-backup-clear>"
	if !strings.HasSuffix(line, want) {
		t.Fatalf("line %q\nwant suffix %q", line, want)
	}
	// Pasted, the line does not run: bash finds a syntax error.
	if err := exec.Command("bash", "-n", "-c", strings.Replace(line, "FILE", "/bin/true", 1)).Run(); err == nil {
		t.Error("bash accepts the line with <UNKNOWN …>")
	}
	if st := b.stateFile(); st["extra_nets"] != "" || strings.Contains(string(mustRead(t, b.stateF)), "extra_nets=") {
		t.Errorf("the kept state invents extra_nets: %v", st)
	}
	// The restore then warns that the flags are not known.
	code, out, errOut = b.run(asRoot, "--dry-run", "--yes", "--plumb-binary", b.plumb, "--restore", filepath.Join(b.store, "plumb-backups", "plumb-20261113T030000Z.pbk"), "--lxc-ip", "192.0.2.50/24", "--lxc-gw", "192.0.2.1")
	if !strings.Contains(out, "is older than T-265 and does not hold the flags of the old install") {
		t.Errorf("no warning on an old state: %d\n%s\n%s", code, out, errOut)
	}
}

// The non-default storages, bridge and pool are in the line too.
func TestRerunLineNamesNonDefaults(t *testing.T) {
	t.Parallel()
	b := newBackupHost(t)
	b.install("--backup-storage", "backups", "--lxc-ip", "192.0.2.50/24", "--lxc-gw", "192.0.2.1", "--bridge", "vmbr1", "--extra-vlan", "vmbr9:20")
	b.serveBackup("plumb-20261113T030000Z.pbk", backupFile(b.stateFile()["install_id"], "pve1", "plumb", "https://192.0.2.50:8444"))
	code, out, errOut := b.keepGuests(nil)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	if line := restoreLine(t, out); !strings.HasSuffix(line, " --lxc-gw 192.0.2.1 --bridge vmbr1"+b.dirs()+" --extra-vlan vmbr9:20 --backup-storage backups") {
		t.Errorf("line %q", line)
	}
}

func mustRead(t *testing.T, f string) []byte {
	t.Helper()
	raw, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A hand-edited state value that the flag checks refuse is never printed:
// the line names the flag as unknown instead.
func TestRerunLineHidesBadValue(t *testing.T) {
	t.Parallel()
	b := t265Host(t)
	raw := mustRead(t, b.stateF)
	bad := strings.Replace(string(raw), "\nbridge=vmbr0\n", "\nbridge=vmbr1;reboot\n", 1)
	if bad == string(raw) {
		t.Fatalf("no bridge line in the state:\n%s", raw)
	}
	if err := os.WriteFile(b.stateF, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := b.keepGuests(nil)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	line := restoreLine(t, out)
	if strings.Contains(line, "reboot") || !strings.HasSuffix(line, "add them by hand: --bridge>") || !strings.Contains(line, t265Extra+" --backup-storage backups <UNKNOWN:") {
		t.Errorf("line %q", line)
	}
}
