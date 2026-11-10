package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Bernotese/plumb/internal/secret"
)

// plumb key init | restore (D-086, D-102 … D-104). These are the only code
// paths that write a production master key. The recovery key goes to the
// terminal only: never to a log, a file or a pipe (journald is in the LXC
// backup).

// keyIO is the terminal of a key command. Tests replace it.
type keyIO struct {
	in          io.Reader
	out, errOut io.Writer
	outTerminal bool                   // stdout is a terminal
	inTerminal  bool                   // stdin is a terminal
	hidden      func() (string, error) // reads a line from the terminal without echo
}

// osKeyIO is the real terminal.
func osKeyIO(stdout, stderr io.Writer) keyIO {
	kio := keyIO{in: os.Stdin, out: stdout, errOut: stderr, inTerminal: isTerminal(os.Stdin),
		hidden: func() (string, error) { return readHidden(os.Stdin) }}
	if f, ok := stdout.(*os.File); ok {
		kio.outTerminal = isTerminal(f)
	}
	return kio
}

const keyUsage = "usage: plumb key init|restore [--data DIR] [--key-file PATH]"

func keyCmd(args []string, kio keyIO) int {
	if len(args) == 0 || (args[0] != "init" && args[0] != "restore") {
		fmt.Fprintln(kio.errOut, keyUsage)
		return 2
	}
	f, err := parseKey(args[0], args[1:], kio.errOut)
	if err != nil {
		return 2
	}
	if args[0] == "init" {
		err = keyInit(f, kio)
	} else {
		err = keyRestore(f, kio)
	}
	if err != nil {
		fmt.Fprintf(kio.errOut, "plumb key %s: %v\n", args[0], err)
		return 1
	}
	return 0
}

type keyFlags struct {
	data    string
	keyFile string
}

const defaultData = "/var/lib/plumb"

// defaultKeyFile is the key file inside a data directory (D-086). The
// directory key/ is the mount point of the volume with backup=0.
func defaultKeyFile(data string) string { return filepath.Join(data, "key", "master.key") }

func parseKey(name string, args []string, stderr io.Writer) (keyFlags, error) {
	var f keyFlags
	fs := flag.NewFlagSet("key "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.data, "data", defaultData, "data directory with the database")
	fs.StringVar(&f.keyFile, "key-file", "", "master key file (default <data>/key/master.key)")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, keyUsage)
		return f, errUsage
	}
	if f.keyFile == "" {
		f.keyFile = defaultKeyFile(f.data)
	}
	return f, nil
}

// checkKeyPlace refuses a key file that would not stay out of the backups:
// it must not exist yet, its directory must exist (the mounted volume; it
// is never created here) and must not be on the file system of the
// database (D-086, review T-025 M1).
func checkKeyPlace(f keyFlags) error {
	if _, err := os.Lstat(f.keyFile); err == nil {
		return fmt.Errorf("%s exists; a key is never replaced. Remove it by hand only if you are sure no secret needs it", f.keyFile)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := filepath.Dir(f.keyFile)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("the key directory %s does not exist; mount the key volume (backup=0) there first", dir)
	}
	// The database is <data>/plumb.db. Before the first start the data
	// directory may not exist yet: compare with its nearest parent.
	db := f.data
	for {
		if _, err := os.Stat(db); err == nil {
			break
		}
		parent := filepath.Dir(db)
		if parent == db {
			break
		}
		db = parent
	}
	same, err := sameFileSystem(dir, db)
	if err != nil {
		return err
	}
	if same {
		return fmt.Errorf("the key directory %s is on the same file system as the database (%s); the key would be in every backup. Mount the key volume (backup=0) at %s", dir, f.data, dir)
	}
	return nil
}

func keyInit(f keyFlags, kio keyIO) error {
	if !kio.outTerminal {
		return errors.New("stdout is not a terminal; the recovery key is shown only on a terminal, never in a log, file or pipe")
	}
	if err := checkKeyPlace(f); err != nil {
		return err
	}
	k := secret.NewKey()
	if err := secret.WriteKeyFile(f.keyFile, k); err != nil {
		return err
	}
	fmt.Fprintf(kio.out, `Master key written to %s (mode 0600).

RECOVERY KEY (shown once; Plumb stores it nowhere):

    %s

Write it down and keep it offline. Backups of Plumb do not hold the master
key. To restore Plumb from a backup, run "plumb key restore" with this key.
Without it, the stored secrets cannot be read again.
`, f.keyFile, k.Recovery())
	return nil
}

func keyRestore(f keyFlags, kio keyIO) error {
	if err := checkKeyPlace(f); err != nil {
		return err
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
		return fmt.Errorf("read the recovery key: %w", err)
	}
	k, err := secret.ParseRecovery(line)
	if err != nil {
		return err
	}
	if err := secret.WriteKeyFile(f.keyFile, k); err != nil {
		return err
	}
	fmt.Fprintf(kio.out, "Master key written to %s (mode 0600). \"plumb serve\" checks it against the database at start.\n", f.keyFile)
	return nil
}

// readLine reads up to the first newline, byte by byte, so nothing after
// the line is consumed. At most max bytes.
func readLine(r io.Reader, max int) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for b.Len() < max {
		n, err := r.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				return b.String(), nil
			}
			b.WriteByte(buf[0])
		}
		if errors.Is(err, io.EOF) {
			if b.Len() == 0 {
				return "", errors.New("no input")
			}
			return b.String(), nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("the line is too long")
}
