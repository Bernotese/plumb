package install

// T-130 (T-122 F5): the fake-PVE tests check only the command lines of
// install.sh, so a path that the installer uses and plumb refuses passed
// them. These tests run the real plumb binary with the exact plumb
// commands that install.sh ran, on the layout it made: backup now → the
// copy script → backup verify (host) → key restore → restore (LXC).

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/Bernotese/plumb/internal/api"
	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
)

var realBin struct {
	once sync.Once
	path string
	err  error
	out  []byte
}

// realPlumb builds cmd/plumb once per test binary.
func realPlumb(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is needed to build plumb")
	}
	realBin.once.Do(func() {
		dir, err := os.MkdirTemp("", "plumb-t130-")
		if err != nil {
			realBin.err = err
			return
		}
		realBin.path = filepath.Join(dir, "plumb")
		realBin.out, realBin.err = exec.Command("go", "build", "-o", realBin.path, "../cmd/plumb").CombinedOutput()
	})
	if realBin.err != nil {
		t.Fatalf("build plumb: %v\n%s", realBin.err, realBin.out)
	}
	return realBin.path
}

// plumbArgs returns the plumb arguments of the first call that runs
// /usr/local/bin/plumb with sub (for example "restore --data").
func plumbArgs(t *testing.T, calls []string, sub string) []string {
	t.Helper()
	const bin = "/usr/local/bin/plumb "
	for _, c := range calls {
		if i := strings.Index(c, bin+sub); i >= 0 {
			return strings.Fields(c[i+len(bin):])
		}
	}
	t.Fatalf("no plumb %s call:\n%s", sub, strings.Join(calls, "\n"))
	return nil
}

// reroot puts every absolute path argument under root (the fake LXC).
func reroot(root string, args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = a
		if strings.HasPrefix(a, "/") {
			out[i] = filepath.Join(root, a)
		}
	}
	return out
}

// runPlumb runs the real binary; tty gives it a terminal on stdout (plumb
// restore prints the new admin password only there).
func runPlumb(t *testing.T, stdin string, tty bool, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(realPlumb(t), args...)
	if tty {
		script, err := exec.LookPath("script")
		if err != nil {
			t.Skip("script is needed to provide a terminal")
		}
		q := []string{"'" + realPlumb(t) + "'"}
		for _, a := range args {
			q = append(q, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
		}
		cmd = exec.Command(script, "-qefc", strings.Join(q, " "), "/dev/null")
	}
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := bounded(t, cmd)
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out.String()
	} else if err != nil {
		t.Fatal(err)
	}
	return 0, out.String()
}

// realData makes the data directory of a running controller: database,
// master key on its own directory, admin, and the install file of serve.
func realData(t *testing.T, data string, in backup.Install) secret.Key {
	t.Helper()
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(data, "key"), 0o700); err != nil {
		t.Fatal(err)
	}
	key := secret.NewKey()
	if err := secret.WriteKeyFile(filepath.Join(data, "key", "master.key"), key); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(data, "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := secret.Open(ctx, st, key); err != nil {
		t.Fatal(err)
	}
	fast := api.PasswordParams{Time: 1, Memory: 64, Threads: 1}
	if _, err := st.CreateUser(ctx, store.User{Name: "admin", PasswordHash: api.HashPassword("old", fast)}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(data, "link"), 0o700)
	os.WriteFile(filepath.Join(data, "link", "ca.crt"), []byte("CA CERT"), 0o644)
	os.WriteFile(filepath.Join(data, "link", "ca.key"), []byte("CA KEY"), 0o600)
	raw, _ := json.Marshal(in)
	os.MkdirAll(filepath.Join(data, backup.DirName), 0o700)
	if err := os.WriteFile(filepath.Join(data, backup.DirName, backup.InstallFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return key
}

// keyVolume makes <data>/key a directory on another file system, like the
// key volume (plumb key restore refuses the file system of the database).
// It returns false when no other file system is at hand.
func keyVolume(t *testing.T, data string) bool {
	t.Helper()
	var a, b syscall.Stat_t
	shm, err := os.MkdirTemp("/dev/shm", "plumb-key-")
	if err != nil {
		return false
	}
	t.Cleanup(func() { os.RemoveAll(shm) })
	if syscall.Stat(shm, &a) != nil || syscall.Stat(data, &b) != nil || a.Dev == b.Dev {
		return false
	}
	return os.Symlink(shm, filepath.Join(data, "key")) == nil
}

func TestRealPlumbBackupAndRestoreChain(t *testing.T) {
	t.Parallel()
	b := installedWithGuests(t)
	st := b.stateFile()
	lxc := filepath.Join(b.state, "ct", "105") // the root of the fake LXC 105
	in := backup.Install{InstallID: st["install_id"], Node: "pve1", Pool: "plumb", AgentURL: "https://192.168.1.50:8444", Keep: 7}
	os.RemoveAll(filepath.Join(lxc, "var", "lib", "plumb")) // the fake backup of installedWithGuests
	key := realData(t, filepath.Join(lxc, "var", "lib", "plumb"), in)

	// --remove --keep-guests: its "plumb backup now" runs for real, then
	// its copy script copies what that wrote.
	// A dry run prints each command as "+ …" and runs none.
	code, out, errOut := b.keepGuests(nil, "--dry-run")
	if code != 0 {
		t.Fatalf("dry run: %d %s %s", code, out, errOut)
	}
	now := plumbArgs(t, strings.Split(out, "\n"), "backup now")
	if code, out := runPlumb(t, "", false, reroot(lxc, now)...); code != 0 {
		t.Fatalf("real plumb %v: %d %s", now, code, out)
	}
	code, out, errOut = b.keepGuests(nil)
	if code != 0 {
		t.Fatalf("keep guests: %d\n%s\n%s", code, out, errOut)
	}
	copies, _ := filepath.Glob(filepath.Join(b.store, "plumb-backups", "plumb-*.pbk"))
	if len(copies) != 1 {
		t.Fatalf("copies %v", copies)
	}
	file := copies[0]

	// verify_on_host: the real binary with the recovery key on its input;
	// install.sh reads exit 3 as a wrong recovery key.
	if code, out := runPlumb(t, key.Recovery()+"\n", false, "backup", "verify", file); code != 0 || !strings.Contains(out, "OK: backup of") {
		t.Fatalf("verify: %d %s", code, out)
	}
	if code, out := runPlumb(t, secret.NewKey().Recovery()+"\n", false, "backup", "verify", file); code != 3 {
		t.Fatalf("verify with a wrong key: %d %s (install.sh needs 3)", code, out)
	}

	// --restore into a new LXC: the same pushes and plumb calls.
	b.resetCalls()
	code, out, errOut = b.restore(nil, file, "--lxc-ip", "192.168.1.50/24", "--lxc-gw", "192.168.1.1")
	if code != 0 {
		t.Fatalf("restore: %d\n%s\n%s", code, out, errOut)
	}
	calls := b.calls()
	keyRestore, restore := plumbArgs(t, calls, "key restore"), plumbArgs(t, calls, "restore --data")
	root := t.TempDir()
	data := filepath.Join(root, "var", "lib", "plumb")
	os.MkdirAll(data, 0o700)
	// The directories that install.sh makes in the LXC before the push.
	for _, c := range calls {
		if strings.HasPrefix(c, "pct exec 105 -- install -d ") {
			f := strings.Fields(c)
			os.MkdirAll(filepath.Join(root, f[len(f)-1]), 0o750)
		}
	}
	from := ""
	for i, a := range restore {
		if a == "--from" && i+1 < len(restore) {
			from = restore[i+1]
		}
	}
	if from == "" {
		t.Fatalf("plumb restore without --from: %v", restore)
	}
	raw, _ := os.ReadFile(file)
	if err := os.WriteFile(filepath.Join(root, from), raw, 0o600); err != nil {
		t.Fatalf("the backup file at %s: %v", from, err)
	}
	if keyVolume(t, data) {
		if code, out := runPlumb(t, key.Recovery()+"\n", false, reroot(root, keyRestore)...); code != 0 {
			t.Fatalf("real plumb %v: %d %s", keyRestore, code, out)
		}
	} else {
		// No second file system here: the key file as plumb key restore
		// writes it, the rest of the chain still runs.
		t.Log("no tmpfs at /dev/shm; key file written directly")
		os.MkdirAll(filepath.Join(data, "key"), 0o700)
		if err := secret.WriteKeyFile(filepath.Join(data, "key", "master.key"), key); err != nil {
			t.Fatal(err)
		}
	}
	code, out = runPlumb(t, "", true, reroot(root, restore)...)
	if code != 0 || !strings.Contains(out, "User: admin  Password: ") {
		t.Fatalf("real plumb %v: %d %s", restore, code, strings.SplitN(out, "Password:", 2)[0])
	}
	if _, err := os.Stat(filepath.Join(data, "plumb.db")); err != nil {
		t.Error("no database after the restore")
	}
}
