package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
)

// plumb backup now (T-112, D-302, D-304): one backup beside a running
// serve, for the installer (--remove --keep-guests, T-114). It opens the
// database of the data directory, takes the header of the install from
// the file that serve wrote (backup.InstallFile), waits while another
// backup runs, and exits 0 only when the backup is written.

const backupUsage = "usage: plumb backup now [--data DIR] [--key-file PATH] [--wait DURATION]"

// backupWaitPoll is the pause while another backup runs.
var backupWaitPoll = 2 * time.Second

func backupCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "now" {
		fmt.Fprintln(stderr, backupUsage)
		return 2
	}
	fs := flag.NewFlagSet("backup now", flag.ContinueOnError)
	fs.SetOutput(stderr)
	data := fs.String("data", defaultData, "data directory of serve")
	keyFile := fs.String("key-file", "", "master key file (default <data>/key/master.key)")
	wait := fs.Duration("wait", 15*time.Minute, "how long to wait for a backup that runs already")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, backupUsage)
		return 2
	}
	name, err := backupNow(ctx, *data, *keyFile, *wait)
	if err != nil {
		fmt.Fprintf(stderr, "plumb backup now: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Backup written: %s\n", filepath.Join(*data, backup.DirName, name))
	return 0
}

func backupNow(ctx context.Context, data, keyFile string, wait time.Duration) (string, error) {
	if keyFile == "" {
		keyFile = defaultKeyFile(data)
	}
	key, err := secret.LoadKeyFile(keyFile)
	if err != nil {
		return "", err
	}
	db := filepath.Join(data, "plumb.db")
	if _, err := os.Stat(db); err != nil {
		return "", fmt.Errorf("no database in %s: %w", data, err)
	}
	in, err := backup.LoadInstall(data)
	if err != nil {
		return "", err
	}
	st, err := store.Open(ctx, db)
	if err != nil {
		return "", err
	}
	defer st.Close()
	// A backup with another key could never be restored.
	if _, err := secret.Open(ctx, st, key); err != nil {
		return "", err
	}
	// The retention of serve: keep comes from the install file, never from
	// the environment (the installer runs this without plumb.env).
	svc, err := backup.New(ctx, backup.Config{Store: st, Events: event.New(st), Key: key, DataDir: data,
		Keep: in.Keep, PlumbVersion: version,
		InstallID: in.InstallID, Node: in.Node, Pool: in.Pool, AgentURL: in.AgentURL,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(wait)
	for {
		run, err := svc.RunNow(ctx, backup.SystemActor, store.BackupCLI)
		if errors.Is(err, backup.ErrRunning) {
			if time.Now().After(deadline) {
				return "", fmt.Errorf("another backup still runs after %s", wait)
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backupWaitPoll):
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if run.Result != store.BackupOK {
			return "", fmt.Errorf("backup %s failed: %s", run.Name, run.Message)
		}
		return run.Name, nil
	}
}
