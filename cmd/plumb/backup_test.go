package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
)

// plumb backup now (T-112, for the installer of T-114).
func TestBackupNow(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	key := secret.NewKey()
	keyFile := filepath.Join(t.TempDir(), "master.key")
	if err := secret.WriteKeyFile(keyFile, key); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(data, "link"), 0o700)
	os.WriteFile(filepath.Join(data, "link", "ca.crt"), []byte("crt"), 0o644)
	os.WriteFile(filepath.Join(data, "link", "ca.key"), []byte("key"), 0o600)

	var out, errOut bytes.Buffer
	// Before serve ran once: no install file.
	if code := backupCmd(ctx, []string{"now", "--data", data, "--key-file", keyFile}, &out, &errOut); code != 1 {
		t.Fatalf("no database: %d %s", code, errOut.String())
	}
	st, err := store.Open(ctx, filepath.Join(data, "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secret.Open(ctx, st, key); err != nil {
		t.Fatal(err)
	}
	// What serve does at start.
	if _, err := backup.New(ctx, backup.Config{Store: st, Key: key, DataDir: data, InstallID: "inst-9", Node: "pve-dev",
		Pool: "plumb", AgentURL: "https://192.0.2.10:8444", Serve: true}); err != nil {
		t.Fatal(err)
	}

	errOut.Reset()
	code := backupCmd(ctx, []string{"now", "--data", data, "--key-file", keyFile}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "Backup written: "+filepath.Join(data, backup.DirName, "plumb-")) {
		t.Fatalf("backup now: %d %s %s", code, out.String(), errOut.String())
	}
	name := strings.TrimSpace(filepath.Base(strings.TrimPrefix(out.String(), "Backup written: ")))
	f, err := os.Open(filepath.Join(data, backup.DirName, name))
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := backup.Read(f, key, nil)
	f.Close()
	if err != nil || h.InstallID != "inst-9" || h.AgentURL != "https://192.0.2.10:8444" || h.Pool != "plumb" {
		t.Fatalf("header %+v %v", h, err)
	}

	// Another key: refused before any file.
	other := filepath.Join(t.TempDir(), "other.key")
	secret.WriteKeyFile(other, secret.NewKey())
	if code := backupCmd(ctx, []string{"now", "--data", data, "--key-file", other}, &out, &errOut); code != 1 {
		t.Errorf("other key: %d", code)
	}
	// A backup that runs (serve): wait, then give up.
	if _, err := st.StartBackupRun(ctx, "system", store.BackupSchedule, "plumb-20990101T000000Z.pbk"); err != nil {
		t.Fatal(err)
	}
	old := backupWaitPoll
	backupWaitPoll = 10 * time.Millisecond
	defer func() { backupWaitPoll = old }()
	errOut.Reset()
	if code := backupCmd(ctx, []string{"now", "--data", data, "--key-file", keyFile, "--wait", "50ms"}, &out, &errOut); code != 1 ||
		!strings.Contains(errOut.String(), "another backup still runs") {
		t.Errorf("while running: %d %s", code, errOut.String())
	}
	// The running row of serve is not touched by the CLI.
	if r, _ := st.LastBackupRun(ctx, ""); r.Result != store.BackupRunning {
		t.Errorf("the CLI ended the run of serve: %+v", r)
	}
	if code := backupCmd(ctx, []string{"later"}, &out, &errOut); code != 2 {
		t.Errorf("usage: %d", code)
	}
}

// backup now runs the retention of serve (keep from install.json), not
// the default or PLUMB_BACKUP_KEEP (review L1 of #220).
func TestBackupNowKeep(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	key := secret.NewKey()
	keyFile := filepath.Join(t.TempDir(), "master.key")
	secret.WriteKeyFile(keyFile, key)
	os.MkdirAll(filepath.Join(data, "link"), 0o700)
	os.WriteFile(filepath.Join(data, "link", "ca.crt"), []byte("crt"), 0o644)
	os.WriteFile(filepath.Join(data, "link", "ca.key"), []byte("key"), 0o600)
	st, err := store.Open(ctx, filepath.Join(data, "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret.Open(ctx, st, key)
	if _, err := backup.New(ctx, backup.Config{Store: st, Key: key, DataDir: data, Keep: 30, Serve: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLUMB_BACKUP_KEEP", "1")
	var out, errOut bytes.Buffer
	for i := range 3 {
		if code := backupCmd(ctx, []string{"now", "--data", data, "--key-file", keyFile}, &out, &errOut); code != 0 {
			t.Fatalf("backup %d: %s", i, errOut.String())
		}
		time.Sleep(1100 * time.Millisecond) // a new name each time
	}
	n := 0
	ents, _ := os.ReadDir(filepath.Join(data, backup.DirName))
	for _, e := range ents {
		if backup.NameRE.MatchString(e.Name()) {
			n++
		}
	}
	if n != 3 {
		t.Errorf("backups kept: %d, want 3 (keep 30 of serve)", n)
	}
	if r, _ := st.LastBackupRun(ctx, ""); r.Trigger != store.BackupCLI || r.By != "system" {
		t.Errorf("run: %+v", r)
	}
}
