package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/api"
	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
)

// makeBackup writes a backup of a controller with users, tokens, sessions
// and enrollment tokens. It returns the file and the key.
func makeBackup(t *testing.T) (string, secret.Key) {
	t.Helper()
	return makeBackupUsers(t, true)
}

func makeBackupUsers(t *testing.T, withAdmin bool) (string, secret.Key) {
	t.Helper()
	ctx := context.Background()
	data := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(data, "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	key := secret.NewKey()
	if _, err := secret.Open(ctx, st, key); err != nil {
		t.Fatal(err)
	}
	fast := api.PasswordParams{Time: 1, Memory: 64, Threads: 1}
	name := "admin"
	if !withAdmin {
		name = "root"
	}
	admin, _ := st.CreateUser(ctx, store.User{Name: name, PasswordHash: api.HashPassword("old-admin-password", fast)})
	st.CreateUser(ctx, store.User{Name: "bob", PasswordHash: api.HashPassword("bob-password", fast)})
	st.CreateSession(ctx, store.Session{IDHash: "s1", UserID: admin.ID, ExpiresAt: time.Now().Add(time.Hour)})
	st.CreateToken(ctx, store.Token{ID: "tok_ci", Name: "ci", Hash: "h1", Tier: "plan", CreatedBy: "user:admin"})
	st.CreateToken(ctx, store.Token{ID: "tok_old", Name: "old", Hash: "h2", Tier: "read", CreatedBy: "user:admin"})
	st.RevokeToken(ctx, "tok_old")
	st.PutEnrollToken(ctx, "web1", "enroll-hash", time.Now().Add(time.Hour))
	os.MkdirAll(filepath.Join(data, "link"), 0o700)
	os.WriteFile(filepath.Join(data, "link", "ca.crt"), []byte("CA CERT"), 0o644)
	os.WriteFile(filepath.Join(data, "link", "ca.key"), []byte("CA KEY"), 0o600)
	svc, err := backup.New(ctx, backup.Config{Store: st, Key: key, DataDir: data, InstallID: "inst-1", Serve: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.RunNow(ctx, "system", store.BackupCLI)
	if err != nil || run.Result != store.BackupOK {
		t.Fatalf("backup: %+v %v", run, err)
	}
	return filepath.Join(data, backup.DirName, run.Name), key
}

func termIO(out, errOut *bytes.Buffer) keyIO {
	return keyIO{in: strings.NewReader(""), out: out, errOut: errOut, outTerminal: true}
}

func TestBackupVerifyCLI(t *testing.T) {
	file, key := makeBackup(t)
	keyFile := filepath.Join(t.TempDir(), "recovery.key")
	os.WriteFile(keyFile, []byte(key.Recovery()+"\n"), 0o600)
	var out, errOut bytes.Buffer
	if code := backupVerify([]string{"--key-file", keyFile, file}, termIO(&out, &errOut)); code != 0 || !strings.Contains(out.String(), "OK: backup of") {
		t.Fatalf("verify: %d %s %s", code, out.String(), errOut.String())
	}
	// The recovery key on stdin (the installer's TTY in production).
	kio := termIO(&out, &errOut)
	kio.in = strings.NewReader(key.Recovery() + "\n")
	if code := backupVerify([]string{file}, kio); code != 0 {
		t.Fatalf("verify from stdin: %d %s", code, errOut.String())
	}
	other := filepath.Join(t.TempDir(), "other.key")
	os.WriteFile(other, []byte(secret.NewKey().Recovery()), 0o600)
	if code := backupVerify([]string{"--key-file", other, file}, termIO(&out, &errOut)); code != exitWrongKey {
		t.Errorf("wrong key: %d, want 3", code)
	}
	raw, _ := os.ReadFile(file)
	cut := filepath.Join(t.TempDir(), "cut.pbk")
	os.WriteFile(cut, raw[:len(raw)-10], 0o600)
	if code := backupVerify([]string{"--key-file", keyFile, cut}, termIO(&out, &errOut)); code != exitFailed {
		t.Errorf("cut file: %d", code)
	}
	if code := backupVerify(nil, termIO(&out, &errOut)); code != exitUsage {
		t.Errorf("usage: %d", code)
	}
}

func TestRestoreCLI(t *testing.T) {
	ctx := context.Background()
	file, key := makeBackup(t)
	data := t.TempDir()
	keyFile := filepath.Join(data, "key", "master.key")
	os.MkdirAll(filepath.Dir(keyFile), 0o700)
	if err := secret.WriteKeyFile(keyFile, key); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	args := []string{"--data", data, "--from", file}

	// Not a terminal: refused before anything.
	kio := termIO(&out, &errOut)
	kio.outTerminal = false
	if code := restoreCmd(ctx, args, kio); code != exitFailed || !strings.Contains(errOut.String(), "not a terminal") {
		t.Fatalf("no terminal: %d %s", code, errOut.String())
	}
	// Another key: exit 3, nothing written.
	otherData := t.TempDir()
	otherKey := filepath.Join(otherData, "key", "master.key")
	os.MkdirAll(filepath.Dir(otherKey), 0o700)
	secret.WriteKeyFile(otherKey, secret.NewKey())
	if code := restoreCmd(ctx, []string{"--data", otherData, "--from", file}, termIO(&out, &errOut)); code != exitWrongKey {
		t.Errorf("wrong key: %d", code)
	}
	if ents, _ := os.ReadDir(otherData); len(ents) != 1 {
		t.Errorf("wrong key left files: %v", ents)
	}
	// A data directory with a database: refused.
	busy := t.TempDir()
	os.MkdirAll(filepath.Join(busy, "key"), 0o700)
	secret.WriteKeyFile(filepath.Join(busy, "key", "master.key"), key)
	os.WriteFile(filepath.Join(busy, "plumb.db"), []byte("x"), 0o600)
	if code := restoreCmd(ctx, []string{"--data", busy, "--from", file}, termIO(&out, &errOut)); code != exitFailed {
		t.Errorf("non-empty data dir: %d", code)
	}

	out.Reset()
	errOut.Reset()
	if code := restoreCmd(ctx, args, termIO(&out, &errOut)); code != 0 {
		t.Fatalf("restore: %d %s", code, errOut.String())
	}
	m := regexp.MustCompile(`User: admin  Password: (\S+)`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no password: %s", out.String())
	}
	if ca, _ := os.ReadFile(filepath.Join(data, "link", "ca.key")); string(ca) != "CA KEY" {
		t.Errorf("ca.key: %q", ca)
	}
	if st, _ := os.Stat(filepath.Join(data, "link", "ca.key")); st.Mode().Perm() != 0o600 {
		t.Errorf("ca.key mode %v", st.Mode().Perm())
	}
	ents, _ := os.ReadDir(data)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".restore-") {
			t.Errorf("temp dir left: %s", e.Name())
		}
	}

	st, err := store.Open(ctx, filepath.Join(data, "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r, err := st.GetRestore(ctx)
	if err != nil || !r.Restored || !r.RestoredMode || r.BackupCreatedAt.IsZero() || !strings.Contains(string(r.Header), `"install_id":"inst-1"`) {
		t.Fatalf("restore record: %+v %v", r, err)
	}
	admin, _ := st.UserByName(ctx, "admin")
	if !api.VerifyPassword(m[1], admin.PasswordHash) || api.VerifyPassword("old-admin-password", admin.PasswordHash) ||
		!admin.MustChangePassword || !admin.LockedAt.IsZero() {
		t.Errorf("admin: %+v", admin)
	}
	if bob, _ := st.UserByName(ctx, "bob"); bob.LockedAt.IsZero() {
		t.Error("bob is not locked")
	}
	if _, err := st.SessionByHash(ctx, "s1", time.Now()); err == nil {
		t.Error("a session of the backup survived")
	}
	ci, _ := st.TokenRow(ctx, "tok_ci")
	old, _ := st.TokenRow(ctx, "tok_old")
	if ci.DisabledAt.IsZero() || !old.DisabledAt.IsZero() || old.RevokedAt.IsZero() {
		t.Errorf("tokens: ci %+v old %+v", ci, old)
	}
	if n, _ := st.CountDisabledTokens(ctx); n != 1 {
		t.Errorf("disabled tokens: %d", n)
	}
	if _, err := st.Enroll(ctx, "enroll-hash", time.Now(), store.Agent{ID: "agent-x", Target: "web1", CredentialHash: "cred-hash"}); err == nil {
		t.Error("an unused enrollment token of the backup still works")
	}
	// A second restore into the now full data directory is refused.
	if code := restoreCmd(ctx, args, termIO(&out, &errOut)); code != exitFailed {
		t.Errorf("second restore: %d", code)
	}
}

// A restore that fails after the file was verified leaves no database in
// the data directory; the rest is cleaned at the next try (review H1 of
// #222).
func TestRestoreFailureLeavesNoDatabase(t *testing.T) {
	ctx := context.Background()
	file, key := makeBackupUsers(t, false) // no user admin: the restore step fails
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "key"), 0o700)
	secret.WriteKeyFile(filepath.Join(data, "key", "master.key"), key)
	var out, errOut bytes.Buffer
	if code := restoreCmd(ctx, []string{"--data", data, "--from", file}, termIO(&out, &errOut)); code != exitFailed ||
		!strings.Contains(errOut.String(), "admin") {
		t.Fatalf("restore without admin: %d %s", code, errOut.String())
	}
	ents, _ := os.ReadDir(data)
	if len(ents) != 1 || ents[0].Name() != "key" {
		t.Fatalf("data dir after a failed restore: %v", ents)
	}
	if strings.Contains(out.String(), "Password") {
		t.Error("a password was shown for a failed restore")
	}
	// The rest of a restore cut before its commit point: link/ and a temp
	// dir without plumb.db. A new try cleans them and works.
	good, key2 := makeBackup(t)
	data2 := t.TempDir()
	os.MkdirAll(filepath.Join(data2, "key"), 0o700)
	secret.WriteKeyFile(filepath.Join(data2, "key", "master.key"), key2)
	os.MkdirAll(filepath.Join(data2, "link"), 0o700)
	os.MkdirAll(filepath.Join(data2, ".restore-123"), 0o700)
	if code := restoreCmd(ctx, []string{"--data", data2, "--from", good}, termIO(&out, &errOut)); code != 0 {
		t.Fatalf("restore after a cut one: %d %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(data2, ".restore-123")); !os.IsNotExist(err) {
		t.Error("the old temp dir stays")
	}
	for _, f := range []string{"plumb.db-wal", "plumb.db-shm"} {
		if fi, err := os.Stat(filepath.Join(data2, f)); err == nil && fi.Size() > 0 {
			t.Errorf("%s with data next to the database", f)
		}
	}
}

// failWriter fails every write, like a terminal that went away.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

// The password is shown before the commit point: when it cannot be shown,
// nothing is committed (review M-b of #222).
func TestRestorePasswordBeforeCommit(t *testing.T) {
	ctx := context.Background()
	file, key := makeBackup(t)
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "key"), 0o700)
	secret.WriteKeyFile(filepath.Join(data, "key", "master.key"), key)
	var errOut bytes.Buffer
	kio := keyIO{in: strings.NewReader(""), out: failWriter{}, errOut: &errOut, outTerminal: true}
	if code := restoreCmd(ctx, []string{"--data", data, "--from", file}, kio); code != exitFailed || !strings.Contains(errOut.String(), "nothing restored") {
		t.Fatalf("restore with a broken terminal: %d %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(data, "plumb.db")); !os.IsNotExist(err) {
		t.Fatal("a database was committed without a shown password")
	}
	var out bytes.Buffer
	if code := restoreCmd(ctx, []string{"--data", data, "--from", file}, termIO(&out, &errOut)); code != 0 {
		t.Fatalf("rerun: %d %s", code, errOut.String())
	}
}
