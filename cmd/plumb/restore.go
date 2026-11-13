package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/api"
	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
)

// plumb backup verify FILE and plumb restore (T-113; D-290, D-302, D-303).

// Exit codes of verify and restore: 3 is a wrong recovery key, named
// before any decrypt, so the installer can stop before it removes
// anything (D-290 N4).
const (
	exitFailed   = 1
	exitUsage    = 2
	exitWrongKey = 3
)

const verifyUsage = "usage: plumb backup verify [--key-file F] FILE   (--key-file only for the test host and tests; else the recovery key is read on the terminal)"

// readKey reads the master key: a key file (a 32-byte master key file or
// a file with the recovery key), else the recovery key on the terminal.
func readKey(keyFile string, kio keyIO) (secret.Key, error) {
	if keyFile != "" {
		if k, err := secret.LoadKeyFile(keyFile); err == nil {
			return k, nil
		}
		b, err := os.ReadFile(keyFile)
		if err != nil {
			return secret.Key{}, err
		}
		if len(b) > 1024 {
			return secret.Key{}, errors.New("key file too large")
		}
		return secret.ParseRecovery(string(b))
	}
	var line string
	var err error
	if kio.inTerminal {
		fmt.Fprint(kio.errOut, "Recovery key: ")
		line, err = kio.hidden()
	} else {
		line, err = readLine(kio.in, 1024)
	}
	if err != nil {
		return secret.Key{}, fmt.Errorf("read the recovery key: %w", err)
	}
	return secret.ParseRecovery(line)
}

// backupVerify is "plumb backup verify": it opens every chunk and checks
// the entries; it writes nothing.
func backupVerify(args []string, kio keyIO) int {
	fs := flag.NewFlagSet("backup verify", flag.ContinueOnError)
	fs.SetOutput(kio.errOut)
	keyFile := fs.String("key-file", "", "recovery key or master key file (test host and tests only)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(kio.errOut, verifyUsage)
		return exitUsage
	}
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(kio.errOut, "plumb backup verify: %v\n", err)
		return exitFailed
	}
	defer f.Close()
	// The claimed header first, so a wrong key costs nothing.
	key, err := readKey(*keyFile, kio)
	if err != nil {
		fmt.Fprintf(kio.errOut, "plumb backup verify: %v\n", err)
		return exitFailed
	}
	h, m, err := backup.Read(f, key, nil)
	if err != nil {
		fmt.Fprintf(kio.errOut, "plumb backup verify: %v\n", err)
		if errors.Is(err, backup.ErrWrongKey) {
			return exitWrongKey
		}
		return exitFailed
	}
	fmt.Fprintf(kio.out, "OK: backup of %s, Plumb %s, schema %d, install %s, %d bytes of database\n",
		h.CreatedAt, h.PlumbVersion, h.SchemaVer, h.InstallID, m.Sizes[backup.EntryDB])
	return 0
}

const restoreUsage = "usage: plumb restore --data DIR --from FILE [--key-file PATH]"

// restoreCmd is "plumb restore": after "plumb key restore", into a data
// directory that holds only the key volume. It verifies the whole file
// into a temp directory, then moves the database and the link CA in
// place, and marks the restore in one step (D-303): sessions, tokens,
// users, enrollment tokens, restored mode, and a new admin password that
// it prints once on the terminal.
func restoreCmd(ctx context.Context, args []string, kio keyIO) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(kio.errOut)
	data := fs.String("data", defaultData, "data directory (only the key volume may be in it)")
	from := fs.String("from", "", "the backup file")
	keyFile := fs.String("key-file", "", "master key file (default <data>/key/master.key, written by plumb key restore)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *from == "" {
		fmt.Fprintln(kio.errOut, restoreUsage)
		return exitUsage
	}
	if !kio.outTerminal {
		// The new admin password goes to a terminal only (D-086, D-178).
		fmt.Fprintln(kio.errOut, "plumb restore: stdout is not a terminal; the new admin password is shown only on a terminal")
		return exitFailed
	}
	code, err := restore(ctx, *data, *from, *keyFile, kio)
	if err != nil {
		fmt.Fprintf(kio.errOut, "plumb restore: %v\n", err)
	}
	return code
}

func restore(ctx context.Context, data, from, keyFile string, kio keyIO) (int, error) {
	if keyFile == "" {
		keyFile = defaultKeyFile(data)
	}
	key, err := secret.LoadKeyFile(keyFile)
	if err != nil {
		return exitFailed, fmt.Errorf("%w (run plumb key restore first)", err)
	}
	if err := cleanRestoreRest(data); err != nil {
		return exitFailed, err
	}
	max, err := store.MaxSchemaVersion()
	if err != nil {
		return exitFailed, err
	}
	f, err := os.Open(from)
	if err != nil {
		return exitFailed, err
	}
	defer f.Close()
	tmp, err := os.MkdirTemp(data, ".restore-")
	if err != nil {
		return exitFailed, err
	}
	defer os.RemoveAll(tmp)
	if err := os.Mkdir(filepath.Join(tmp, "link"), 0o700); err != nil {
		return exitFailed, err
	}
	h, _, err := backup.Read(f, key, func(name string, _ int64, r io.Reader) error {
		mode := os.FileMode(0o600)
		if name == backup.EntryCACert {
			mode = 0o644
		}
		out, err := os.OpenFile(filepath.Join(tmp, filepath.FromSlash(name)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, r); err != nil {
			out.Close()
			return err
		}
		if err := out.Sync(); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
	if errors.Is(err, backup.ErrWrongKey) {
		return exitWrongKey, err
	}
	if err != nil {
		return exitFailed, err
	}
	if h.SchemaVer > max {
		return exitFailed, fmt.Errorf("the backup has database schema %d, this plumb knows %d: use a newer plumb", h.SchemaVer, max)
	}
	created, err := time.Parse(time.RFC3339, h.CreatedAt)
	if err != nil {
		return exitFailed, fmt.Errorf("backup header: created_at: %w", err)
	}
	// Everything happens on the database in the temp directory: the
	// migration, the key check and the restore step. Only then link/ and,
	// last, plumb.db move in place: the rename of plumb.db is the commit
	// point. A failure or a crash before it leaves no database in the
	// data directory (review H1 of #222).
	password, err := restoreDB(ctx, filepath.Join(tmp, "plumb.db"), key, created, h)
	if err != nil {
		return exitFailed, err
	}
	if err := os.Rename(filepath.Join(tmp, "link"), filepath.Join(data, "link")); err != nil {
		return exitFailed, err
	}
	if err := syncDir(data); err != nil {
		return exitFailed, err
	}
	// The password goes to the terminal before the commit point (review
	// M-b of #222): when the terminal is gone, nothing is committed and a
	// rerun makes a new one; when the commit fails, the text says so.
	if _, err := fmt.Fprintf(kio.out, "User: admin  Password: %s\nThis password is shown once. The first login must change it. Every other user is locked, every API token is disabled.\n", password); err != nil {
		return exitFailed, fmt.Errorf("show the new admin password: %w; nothing restored, run plumb restore again", err)
	}
	commit := func() error {
		if err := os.Rename(filepath.Join(tmp, "plumb.db"), filepath.Join(data, "plumb.db")); err != nil {
			return err
		}
		return syncDir(data)
	}
	if err := commit(); err != nil {
		return exitFailed, fmt.Errorf("the restore did not finish (%w); the password above is not valid; run plumb restore again", err)
	}
	fmt.Fprintf(kio.out, "Restored the backup of %s (install %s).\n", h.CreatedAt, h.InstallID)
	fmt.Fprintln(kio.out, "Plumb starts in restored mode: it changes nothing until you confirm that the old controller is off (POST /restore/confirm).")
	return 0, nil
}

// restoreDB migrates the database file, checks the key against it and
// runs the restore step (D-303). It returns the new admin password. The
// database is closed when it returns, and no WAL file stays next to it.
func restoreDB(ctx context.Context, db string, key secret.Key, created time.Time, h backup.Header) (string, error) {
	st, err := store.Open(ctx, db) // migrates an older schema
	if err != nil {
		return "", err
	}
	password, err := func() (string, error) {
		if _, err := secret.Open(ctx, st, key); err != nil {
			return "", err
		}
		header, err := json.Marshal(h)
		if err != nil {
			return "", err
		}
		password := api.RandomPassword()
		return password, st.Restore(ctx, created, header, api.HashPassword(password, api.DefaultPasswordParams))
	}()
	if cerr := st.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	// The last close checkpoints the WAL. A WAL that still holds data
	// would carry the restore step outside plumb.db.
	if fi, serr := os.Stat(db + "-wal"); serr == nil && fi.Size() > 0 {
		return "", errors.New("the database left a WAL file with data after close")
	}
	os.Remove(db + "-wal")
	os.Remove(db + "-shm")
	return password, nil
}

// cleanRestoreRest checks that the data directory holds nothing but the
// key volume. The rest of a restore that stopped before its commit point
// (link/ or .restore-* without plumb.db) is removed; anything else
// refuses.
func cleanRestoreRest(data string) error {
	ents, err := os.ReadDir(data)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.Name() == "plumb.db" {
			return fmt.Errorf("%s holds a database: restore only into a new install", data)
		}
	}
	for _, e := range ents {
		switch n := e.Name(); {
		case n == "key" || n == "lost+found":
		case n == "link" && e.IsDir(), strings.HasPrefix(n, ".restore-") && e.IsDir():
			if err := os.RemoveAll(filepath.Join(data, n)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s is not empty (%s): restore only into a new install", data, n)
		}
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
