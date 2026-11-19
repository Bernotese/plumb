// Tests of T-269: a restore keeps the off-host copy working (F2 of T-263),
// and the printed line names every flag of the install (L1 of T-263).
package install

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// copyConf is the config of the copy timer as a map.
func (b *backupHost) copyConf() map[string]string {
	raw, err := os.ReadFile(b.conf)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, l := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			out[k] = v
		}
	}
	return out
}

func (b *backupHost) wholeCopyConf(t *testing.T, when string) {
	t.Helper()
	c := b.copyConf()
	if c["storage"] != "backups" || c["storage_type"] != "dir" || c["storage_path"] != b.store {
		t.Errorf("%s: copy config %v", when, c)
	}
}

// F2: a restore without --backup-storage (the README's line before T-269)
// takes the kept storage with its type and path.
func TestRestoreWithoutBackupStorageKeepsTheCopy(t *testing.T) {
	t.Parallel()
	b, file := keptHost(t)
	code, out, errOut := b.restore(nil, file, "--lxc-ip", "192.0.2.50/24", "--lxc-gw", "192.0.2.1")
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "Using kept backup storage backups") {
		t.Errorf("no kept storage:\n%s", out)
	}
	b.wholeCopyConf(t, "restore without --backup-storage")
	// The copy then works.
	if code, _, errOut := b.runCopy(); code != 0 {
		t.Errorf("the copy after the restore: %d %s", code, errOut)
	}
}

// F2: every path writes a whole config: install, repair with and without
// the flag, and a restore with the flag.
func TestCopyConfigWholeOnEveryPath(t *testing.T) {
	t.Parallel()
	b := installedWithGuests(t)
	b.wholeCopyConf(t, "install")
	b.install()
	b.wholeCopyConf(t, "repair without --backup-storage")
	b.install("--backup-storage", "backups")
	b.wholeCopyConf(t, "repair with --backup-storage")
	if code, out, errOut := b.keepGuests(nil); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	file := filepath.Join(b.store, "plumb-backups", "plumb-20261113T030000Z.pbk")
	if code, out, errOut := b.restore(nil, file, "--lxc-ip", "192.0.2.50/24", "--lxc-gw", "192.0.2.1", "--backup-storage", "backups"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	b.wholeCopyConf(t, "restore with --backup-storage")
}

// An install with the empty config of an older restore: the next repair
// run writes it whole; when the storage fails its checks, it says so.
func TestBrokenCopyConfigIsRepairedOrNamed(t *testing.T) {
	t.Parallel()
	b := installedWithGuests(t)
	broken := strings.NewReplacer("storage_type=dir\n", "storage_type=\n", "storage_path="+b.store+"\n", "storage_path=\n").Replace(string(mustRead(t, b.conf)))
	if err := os.WriteFile(b.conf, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := b.copyConf(); c["storage_type"] != "" || c["storage_path"] != "" {
		t.Fatalf("not broken: %v", c)
	}
	b.install()
	b.wholeCopyConf(t, "repair of a broken config")

	if err := os.WriteFile(b.conf, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(b.state, "type.backups")) // the storage is gone now
	code, out, errOut := b.run(asRoot, "--yes", "--plumb-binary", b.plumb)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "has no storage type or path (made by an older --restore): every copy fails") {
		t.Errorf("the broken config is not named:\n%s", out)
	}
}

// t269Flags: every flag of the install that L1 named, none the default.
var t269Flags = []string{"--template-storage", "tmplstore", "--vmid-range", "9100-9199", "--lxc-id", "105",
	"--nameserver", "192.0.2.53,192.0.2.54", "--searchdomain", "lab.test", "--backup-time", "04:15", "--backup-keep", "9",
	"--backup-copy-time", "05:00", "--backup-copy-keep", "20", "--backup-max-size", "2G"}

const t269Want = " --template-storage tmplstore --vmid-range 9100-9199 --lxc-id 105 --nameserver 192.0.2.53,192.0.2.54" +
	" --searchdomain lab.test --backup-time 04:15 --backup-keep 9 --backup-copy-time 05:00 --backup-copy-keep 20 --backup-max-size 2G"

func TestRemoveKeepGuestsLineHasAllFlags(t *testing.T) {
	t.Parallel()
	b := newBackupHost(t)
	b.host.addStorage("tmplstore", "dir", "vztmpl", "")
	b.host.addStorage("plumb-backup", "dir", "backup", "")
	b.install(append(append([]string{"--backup-storage", "backups", "--lxc-ip", "192.0.2.50/24", "--lxc-gw", "192.0.2.1"}, t265Flags...), t269Flags...)...)
	b.serveBackup("plumb-20261113T030000Z.pbk", backupFile(b.stateFile()["install_id"], "pve1", "plumb", "https://192.0.2.50:8444"))
	b.env = append(b.env, `FAKE_POOL_MEMBERS=[{"poolid":"plumb","members":[{"vmid":9100,"type":"qemu"}]}]`)

	// A repair run without --lxc-id keeps the given id (the same LXC).
	noID := append(append([]string{}, t269Flags[:4]...), t269Flags[6:]...) // without --lxc-id 105
	b.install(append(append([]string{"--backup-storage", "backups"}, t265Flags...), noID...)...)
	if got := b.stateFile()["lxc_id_arg"]; got != "105" {
		t.Errorf("lxc_id_arg after a repair without --lxc-id: %q", got)
	}
	before := b.stateFile()

	code, out, errOut := b.keepGuests(nil)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	file := filepath.Join(b.store, "plumb-backups", "plumb-20261113T030000Z.pbk")
	line := restoreLine(t, out)
	want := "install.sh --plumb-binary FILE --restore " + file + " --lxc-ip 192.0.2.50/24 --lxc-gw 192.0.2.1" + b.dirs() + t265Extra + t269Want + " --backup-storage backups"
	if line != want {
		t.Fatalf("restore line:\n%s\nwant:\n%s", line, want)
	}

	// A restore from exactly that line: no warning, the same keys again.
	args := strings.Fields(strings.TrimPrefix(line, "install.sh --plumb-binary FILE --restore "+file))
	code, out, errOut = b.restore(nil, file, args...)
	if code != 0 {
		t.Fatalf("restore from the line: %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, "WARNING: this run's flags differ") || strings.Contains(out, "does not hold these flags") {
		t.Errorf("warning on the exact line:\n%s", out)
	}
	after := b.stateFile()
	for _, row := range []string{"template_storage", "vmid_range", "lxc_id_arg", "nameserver_arg", "searchdomain_arg", "backup_time",
		"backup_keep", "backup_copy_time_arg", "backup_copy_keep", "backup_max_size", "extra_nets", "guest_backup_storage"} {
		if after[row] == "" || after[row] != before[row] {
			t.Errorf("%s: before %q, after the restore %q", row, before[row], after[row])
		}
	}
	env := string(mustRead(t, filepath.Join(b.state, "push", "_etc_plumb_plumb.env")))
	for _, l := range []string{"PLUMB_PVE_NAMESERVER=192.0.2.53,192.0.2.54", "PLUMB_PVE_SEARCHDOMAIN=lab.test", "PLUMB_BACKUP_TIME=04:15", "PLUMB_BACKUP_KEEP=9"} {
		if !strings.Contains(env, l+"\n") {
			t.Errorf("plumb.env after the restore lacks %s", l)
		}
	}
	if c := b.copyConf(); c["keep"] != "20" || c["max_bytes"] != "2147483648" {
		t.Errorf("copy config after the restore: %v", c)
	}

	// A restore without them warns and names the old flags.
	b2 := newBackupHost(t)
	b2.host.addStorage("tmplstore", "dir", "vztmpl", "")
	b2.install(append([]string{"--backup-storage", "backups", "--lxc-ip", "192.0.2.50/24", "--lxc-gw", "192.0.2.1"}, t269Flags...)...)
	b2.serveBackup("plumb-20261113T030000Z.pbk", backupFile(b2.stateFile()["install_id"], "pve1", "plumb", "https://192.0.2.50:8444"))
	if code, out, errOut := b2.keepGuests(nil); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	code, out, _ = b2.run(asRoot, "--dry-run", "--yes", "--plumb-binary", b2.plumb, "--restore", filepath.Join(b2.store, "plumb-backups", "plumb-20261113T030000Z.pbk"),
		"--lxc-ip", "192.0.2.50/24", "--lxc-gw", "192.0.2.1")
	if !strings.Contains(out, "WARNING: this run's flags differ from those of the old install. The old install had:\n  install.sh --plumb-binary FILE"+b2.dirs()+t269Want+" (plus") {
		t.Errorf("no warning for a restore without the flags: %d\n%s", code, out)
	}
}

// A T-265 state (the six rights keys, none of T-269's): the line names
// the new flags as unknown, and the kept state does not invent them.
func TestT265StateNamesNewFlagsUnknown(t *testing.T) {
	t.Parallel()
	b := t265Host(t)
	newKeys := []string{"template_storage", "vmid_range", "lxc_id_arg", "nameserver_arg", "searchdomain_arg", "backup_time",
		"backup_keep", "backup_copy_time_arg", "backup_copy_keep", "backup_max_size"}
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(string(mustRead(t, b.stateF))), "\n") {
		k, _, _ := strings.Cut(l, "=")
		if !slices.Contains(newKeys, k) {
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
	if !strings.Contains(line, t265Extra+" --backup-storage backups <UNKNOWN: ") || !strings.HasSuffix(line, "add them by hand: --template-storage --vmid-range --lxc-id --nameserver --searchdomain --backup-time --backup-keep --backup-copy-time --backup-copy-keep --backup-max-size>") {
		t.Errorf("line %q", line)
	}
	st := string(mustRead(t, b.stateF))
	for _, k := range newKeys {
		if strings.Contains(st, "\n"+k+"=") {
			t.Errorf("the kept state invents %s", k)
		}
	}
}
