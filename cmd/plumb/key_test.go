package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
)

// otherFS returns a directory on another file system than t.TempDir, or
// skips the test.
func otherFS(t *testing.T) string {
	t.Helper()
	for _, base := range []string{"/dev/shm", "/run/user/" + strconv.Itoa(os.Getuid())} {
		dir, err := os.MkdirTemp(base, "plumb-key-test-*")
		if err != nil {
			continue
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		if same, err := sameFileSystem(dir, t.TempDir()); err == nil && !same {
			return dir
		}
	}
	t.Skip("no second file system for the key directory")
	return ""
}

type fakeTerm struct {
	out, errOut bytes.Buffer
}

func (ft *fakeTerm) io(outTerminal bool, in string) keyIO {
	return keyIO{in: strings.NewReader(in), out: &ft.out, errOut: &ft.errOut, outTerminal: outTerminal}
}

var recoveryPattern = regexp.MustCompile(`[A-Z2-7]{4}(-[A-Z2-7]{4}){13}`)

func TestKeyInit(t *testing.T) {
	data := t.TempDir()
	keyDir := otherFS(t)
	keyFile := filepath.Join(keyDir, "master.key")
	args := []string{"init", "--data", data, "--key-file", keyFile}

	// Not a terminal: refused, nothing written. The real stdout of a test
	// is a buffer, so run() itself refuses too.
	var ft fakeTerm
	if code := keyCmd(args, ft.io(false, "")); code != 1 || !strings.Contains(ft.errOut.String(), "not a terminal") {
		t.Fatalf("no terminal: %d %q", code, ft.errOut.String())
	}
	var out, errOut bytes.Buffer
	if code := run(append([]string{"key"}, args...), &out, &errOut); code != 1 || out.Len() != 0 {
		t.Fatalf("run without terminal: %d %q", code, out.String())
	}
	if _, err := os.Stat(keyFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused init wrote a key")
	}

	// On a terminal: the key file and the recovery key, once, on stdout.
	ft = fakeTerm{}
	if code := keyCmd(args, ft.io(true, "")); code != 0 {
		t.Fatalf("init: %d %s", code, ft.errOut.String())
	}
	rec := recoveryPattern.FindString(ft.out.String())
	if rec == "" || strings.Count(ft.out.String(), rec) != 1 || ft.errOut.Len() != 0 {
		t.Fatalf("init output: %q / %q", ft.out.String(), ft.errOut.String())
	}
	k, err := secret.LoadKeyFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := secret.ParseRecovery(rec); err != nil || parsed != k {
		t.Fatalf("recovery key does not match the key file: %v", err)
	}
	if info, _ := os.Stat(keyFile); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	// No other file holds the recovery key.
	filepath.WalkDir(data, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); bytes.Contains(b, []byte(rec)) {
				t.Errorf("%s holds the recovery key", p)
			}
		}
		return nil
	})

	// A key file exists: refused, the key stays.
	ft = fakeTerm{}
	if code := keyCmd(args, ft.io(true, "")); code != 1 || !strings.Contains(ft.errOut.String(), "exists") || recoveryPattern.MatchString(ft.out.String()) {
		t.Fatalf("existing key: %d %q", code, ft.errOut.String())
	}
	if again, _ := secret.LoadKeyFile(keyFile); again != k {
		t.Fatal("the key changed")
	}
}

func TestKeyInitRefusesPlaces(t *testing.T) {
	data := t.TempDir()
	os.Mkdir(filepath.Join(data, "key"), 0o700)
	for name, args := range map[string][]string{
		// The default place is inside the data directory: same file system.
		"database file system": {"init", "--data", data},
		// Also when the data directory does not exist yet.
		"new data directory": {"init", "--data", filepath.Join(data, "new"), "--key-file", filepath.Join(data, "k", "..", "key", "x.key")},
		"missing key dir":    {"init", "--data", data, "--key-file", filepath.Join(data, "nope", "master.key")},
	} {
		var ft fakeTerm
		if code := keyCmd(args, ft.io(true, "")); code != 1 {
			t.Errorf("%s: %d", name, code)
		}
		if name != "missing key dir" && !strings.Contains(ft.errOut.String(), "same file system") {
			t.Errorf("%s: %q", name, ft.errOut.String())
		}
		if recoveryPattern.MatchString(ft.out.String()) {
			t.Errorf("%s: printed a recovery key", name)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(data, "key"))
	if len(entries) != 0 {
		t.Fatal("a refused init wrote a file")
	}
	var ft fakeTerm
	if code := keyCmd([]string{"rotate"}, ft.io(true, "")); code != 2 {
		t.Errorf("unknown subcommand: %d", code)
	}
}

func TestKeyRestore(t *testing.T) {
	data := t.TempDir()
	keyDir := otherFS(t)
	keyFile := filepath.Join(keyDir, "master.key")
	args := []string{"restore", "--data", data, "--key-file", keyFile}
	k := secret.NewKey()
	rec := k.Recovery()

	// A bad checksum: refused, no file.
	bad := []byte(rec)
	bad[5] = map[bool]byte{true: 'B', false: 'A'}[bad[5] == 'A']
	var ft fakeTerm
	if code := keyCmd(args, ft.io(false, string(bad)+"\n")); code != 1 || !strings.Contains(ft.errOut.String(), "not valid") {
		t.Fatalf("bad checksum: %d %q", code, ft.errOut.String())
	}
	if strings.Contains(ft.errOut.String(), string(bad)) {
		t.Fatal("the error repeats the input")
	}
	if _, err := os.Stat(keyFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused restore wrote a key")
	}

	// From stdin (a pipe).
	ft = fakeTerm{}
	if code := keyCmd(args, ft.io(false, strings.ToLower(rec)+"\n")); code != 0 {
		t.Fatalf("restore: %d %s", code, ft.errOut.String())
	}
	if got, err := secret.LoadKeyFile(keyFile); err != nil || got != k {
		t.Fatalf("restored key: %v", err)
	}
	if strings.Contains(ft.out.String()+ft.errOut.String(), rec) {
		t.Fatal("restore printed the recovery key")
	}

	// Never over an existing file.
	ft = fakeTerm{}
	if code := keyCmd(args, ft.io(false, secret.NewKey().Recovery()+"\n")); code != 1 || !strings.Contains(ft.errOut.String(), "exists") {
		t.Fatalf("overwrite: %d %q", code, ft.errOut.String())
	}
	if got, _ := secret.LoadKeyFile(keyFile); got != k {
		t.Fatal("the key changed")
	}

	// From the terminal: the hidden reader, a prompt on stderr.
	os.Remove(keyFile)
	ft = fakeTerm{}
	kio := ft.io(false, "")
	kio.inTerminal = true
	kio.hidden = func() (string, error) { return rec, nil }
	if code := keyCmd(args, kio); code != 0 || !strings.Contains(ft.errOut.String(), "Recovery key:") {
		t.Fatalf("terminal restore: %d %q", code, ft.errOut.String())
	}

	// Same file system as the database: refused.
	ft = fakeTerm{}
	os.Mkdir(filepath.Join(data, "key"), 0o700)
	if code := keyCmd([]string{"restore", "--data", data}, ft.io(false, rec+"\n")); code != 1 || !strings.Contains(ft.errOut.String(), "same file system") {
		t.Fatalf("restore on the database file system: %d %q", code, ft.errOut.String())
	}
}

// serve without --dev: a missing key stops the start on an empty data
// directory and creates no file at all; a key that group or others can
// read stops it too.
func TestServeNeedsKey(t *testing.T) {
	for _, extra := range [][]string{nil, {"--key-file", "/nonexistent/master.key"}} {
		data := t.TempDir()
		var out, errOut bytes.Buffer
		code := serve(context.Background(), append([]string{"--data", data}, extra...), &out, &errOut)
		if code != 1 || !strings.Contains(errOut.String(), "plumb key init") || !strings.Contains(errOut.String(), "plumb key restore") {
			t.Errorf("%v: %d %q", extra, code, errOut.String())
		}
		if entries, _ := os.ReadDir(data); len(entries) != 0 {
			t.Errorf("%v: serve created %v", extra, entries)
		}
	}
	data := t.TempDir()
	os.Mkdir(filepath.Join(data, "key"), 0o700)
	secret.WriteKeyFile(defaultKeyFile(data), secret.NewKey())
	os.Chmod(defaultKeyFile(data), 0o640)
	var out, errOut bytes.Buffer
	if code := serve(context.Background(), []string{"--data", data}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "must be 0600") {
		t.Errorf("readable key: %d %q", code, errOut.String())
	}
	if _, err := parseServe([]string{"--dev", "--key-file", "/tmp/x"}, &bytes.Buffer{}); err == nil {
		t.Error("--dev with --key-file accepted")
	}
}

// runDev starts a dev server on data, waits until it listens, stops it and
// returns its stdout and log.
func runDev(t *testing.T, data string, extra ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f, err := parseServe(append([]string{"--dev", "--data", data, "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0"}, extra...), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var mu chanBuffer
	mu.init()
	out := outputProbe(func(b []byte) (int, error) {
		mu.write(b)
		if strings.HasPrefix(string(b), "Plumb listens") {
			go func() { time.Sleep(300 * time.Millisecond); cancel() }()
		}
		return len(b), nil
	})
	logOut := outputProbe(func(b []byte) (int, error) { mu.write(b); return len(b), nil })
	err = runServer(ctx, f, out, slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return mu.String(), err
}

type chanBuffer struct{ c chan []byte }

func (b *chanBuffer) init()          { b.c = make(chan []byte, 1); b.c <- nil }
func (b *chanBuffer) write(p []byte) { buf := <-b.c; b.c <- append(buf, p...) }
func (b *chanBuffer) String() string { buf := <-b.c; b.c <- buf; return string(buf) }

func TestDevKey(t *testing.T) {
	data := t.TempDir()
	output, err := runDev(t, data, "--dev-demo")
	if err != nil {
		t.Fatal(err)
	}
	k, err := secret.LoadKeyFile(defaultKeyFile(data))
	if err != nil {
		t.Fatalf("dev key: %v", err)
	}
	// The output never holds the recovery key (journald is in the backup).
	rec := k.Recovery()
	if strings.Contains(output, rec) || strings.Contains(output, strings.ReplaceAll(rec, "-", "")) || recoveryPattern.MatchString(output) ||
		strings.Contains(strings.ToLower(output), "recovery") {
		t.Fatalf("serve printed a recovery key: %s", output)
	}
	// The second start uses the same key; the demo secret exists once.
	if _, err := runDev(t, data, "--dev-demo"); err != nil {
		t.Fatal(err)
	}
	if again, _ := secret.LoadKeyFile(defaultKeyFile(data)); again != k {
		t.Fatal("the dev key changed")
	}
	st, err := store.Open(context.Background(), filepath.Join(data, "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	sec, err := secret.Open(context.Background(), st, k)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := sec.List(context.Background())
	if len(list) != 1 || list[0].Name != demoSecret || list[0].UpdatedBy != "system" {
		t.Fatalf("demo secrets: %+v", list)
	}
	st.Close()
	// A wrong key stops the start.
	os.Remove(defaultKeyFile(data))
	secret.WriteKeyFile(defaultKeyFile(data), secret.NewKey())
	if _, err := runDev(t, data); !errors.Is(err, secret.ErrWrongKey) {
		t.Fatalf("wrong key: %v", err)
	}
}

// Review nit 4: a key file on the file system of the database gives a
// warning at start; one on the key volume does not.
func TestServeWarnsKeyOnDatabaseFS(t *testing.T) {
	data := t.TempDir()
	os.Mkdir(filepath.Join(data, "key"), 0o700)
	secret.WriteKeyFile(defaultKeyFile(data), secret.NewKey())
	var out, errOut bytes.Buffer
	serve(context.Background(), []string{"--data", data}, &out, &errOut)
	if !strings.Contains(errOut.String(), "backups hold it") {
		t.Errorf("no warning: %q", errOut.String())
	}
	keyFile := filepath.Join(otherFS(t), "master.key")
	secret.WriteKeyFile(keyFile, secret.NewKey())
	errOut.Reset()
	serve(context.Background(), []string{"--data", t.TempDir(), "--key-file", keyFile}, &out, &errOut)
	if strings.Contains(errOut.String(), "backups hold it") {
		t.Errorf("warning for a key on its own file system: %q", errOut.String())
	}
}
