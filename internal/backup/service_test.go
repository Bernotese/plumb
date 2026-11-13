package backup

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

type fixture struct {
	t     *testing.T
	ctx   context.Context
	data  string
	st    *store.Store
	ev    *event.Log
	key   secret.Key
	clock *clock
	svc   *Service
	copy  string
}

func newFixture(t *testing.T, cfg func(*Config)) *fixture {
	t.Helper()
	ctx := context.Background()
	data := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(data, "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	link := filepath.Join(data, "link")
	if err := os.MkdirAll(link, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(link, "ca.crt"), []byte("-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----\n"), 0o644)
	os.WriteFile(filepath.Join(link, "ca.key"), []byte("ca-private-key-bytes"), 0o600)
	f := &fixture{t: t, ctx: ctx, data: data, st: st, ev: event.New(st), key: secret.NewKey(),
		clock: &clock{t: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)}, copy: filepath.Join(t.TempDir(), "status.json")}
	st.Now = f.clock.now
	c := Config{Store: st, Events: f.ev, Key: f.key, DataDir: data, PlumbVersion: "v-test", InstallID: "install-1",
		Node: "pve-dev", Pool: "plumb", AgentURL: "https://192.0.2.10:8444", CopyStatus: f.copy, Now: f.clock.now,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if cfg != nil {
		cfg(&c)
	}
	f.svc, err = New(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) backup(by string) store.BackupRun {
	f.t.Helper()
	run, err := f.svc.RunNow(f.ctx, by)
	if err != nil {
		f.t.Fatal(err)
	}
	if run.Result != store.BackupOK {
		f.t.Fatalf("backup: %+v", run)
	}
	return run
}

func (f *fixture) file(name string) []byte {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.data, DirName, name))
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func TestBackupRoundTrip(t *testing.T) {
	f := newFixture(t, nil)
	run := f.backup("user:admin")
	if !NameRE.MatchString(run.Name) || run.Name != "plumb-20261113T100000Z.pbk" {
		t.Fatalf("name %q", run.Name)
	}
	dir := filepath.Join(f.data, DirName)
	for _, p := range []string{dir, filepath.Join(dir, run.Name), filepath.Join(dir, run.Name+SumSuffix), filepath.Join(dir, PointerName)} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if p == dir {
			want = 0o700
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s: mode %v", p, st.Mode().Perm())
		}
	}
	raw := f.file(run.Name)
	sum := sha256.Sum256(raw)
	var ptr Pointer
	if err := json.Unmarshal(f.file(PointerName), &ptr); err != nil || ptr.Name != run.Name || ptr.Size != int64(len(raw)) || ptr.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("pointer %+v %v", ptr, err)
	}
	if string(f.file(run.Name+SumSuffix)) != hex.EncodeToString(sum[:])+"  "+run.Name+"\n" {
		t.Errorf("sum file: %q", f.file(run.Name+SumSuffix))
	}
	// No plain data and no key in the file.
	for _, s := range []string{"ca-private-key-bytes", "SQLite format 3", "BEGIN CERTIFICATE"} {
		if bytes.Contains(raw, []byte(s)) {
			t.Errorf("the file holds %q in clear", s)
		}
	}
	got := map[string][]byte{}
	h, m, err := Read(bytes.NewReader(raw), f.key, func(name string, size int64, r io.Reader) error {
		b, err := io.ReadAll(r)
		got[name] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.InstallID != "install-1" || h.Node != "pve-dev" || h.Pool != "plumb" || h.AgentURL != "https://192.0.2.10:8444" ||
		h.PlumbVersion != "v-test" || h.KeyFP != Fingerprint(f.key) || h.SchemaVer < 14 || m.SchemaVer != h.SchemaVer {
		t.Errorf("header %+v manifest %+v", h, m)
	}
	if string(got[EntryCAKey]) != "ca-private-key-bytes" || !strings.Contains(string(got[EntryCACert]), "CA") ||
		!bytes.HasPrefix(got[EntryDB], []byte("SQLite format 3\x00")) || int64(len(got[EntryDB])) != m.Sizes[EntryDB] {
		t.Errorf("entries: %d bytes db, %q", len(got[EntryDB]), got[EntryCAKey])
	}
	// The snapshot is a working database with this run in it.
	snap := filepath.Join(t.TempDir(), "snap.db")
	os.WriteFile(snap, got[EntryDB], 0o600)
	s2, err := store.Open(f.ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if r, err := s2.LastBackupRun(f.ctx, ""); err != nil || r.Name != run.Name {
		t.Errorf("snapshot run: %+v %v", r, err)
	}
	// Event and stored run.
	last, err := f.st.LastBackupRun(f.ctx, "")
	if err != nil || last.Result != store.BackupOK || last.FinishedAt.IsZero() || last.By != "user:admin" {
		t.Errorf("run: %+v %v", last, err)
	}
	evs, _ := f.st.EventsAfter(f.ctx, 0, 100)
	found := false
	for _, e := range evs {
		if e.Type == "backup.finished" && e.Subject == "backup:"+run.Name && strings.Contains(string(e.Data), `"result":"ok"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("no backup.finished event: %+v", evs)
	}
	// No temp file is left.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), TempPrefix) {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

func TestBackupWrongKey(t *testing.T) {
	f := newFixture(t, nil)
	run := f.backup("system")
	_, _, err := Read(bytes.NewReader(f.file(run.Name)), secret.NewKey(), nil)
	if !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong key: %v", err)
	}
	// A header with the right fingerprint but another key: no chunk opens.
	raw := f.file(run.Name)
	other := secret.NewKey()
	h, pre, _ := ReadHeader(bytes.NewReader(raw))
	h.KeyFP = Fingerprint(other)
	forged := reheader(t, raw, pre, h)
	if _, _, err := Read(bytes.NewReader(forged), other, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("forged fingerprint: %v", err)
	}
}

// reheader replaces the header of a file.
func reheader(t *testing.T, raw, pre []byte, h Header) []byte {
	t.Helper()
	hb, _ := json.Marshal(h)
	out := append([]byte(Magic), binary.BigEndian.AppendUint32(nil, uint32(len(hb)))...)
	out = append(out, hb...)
	return append(out, raw[len(pre):]...)
}

func TestBackupCutAndChanged(t *testing.T) {
	f := newFixture(t, nil)
	run := f.backup("system")
	raw := f.file(run.Name)
	_, pre, err := ReadHeader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 5, len(Magic) + 2, len(pre) - 1, len(pre), len(pre) + 3, len(pre) + 100, len(raw) - 17, len(raw) - 1} {
		if _, _, err := Read(bytes.NewReader(raw[:n]), f.key, nil); err == nil {
			t.Errorf("cut at %d of %d: accepted", n, len(raw))
		}
	}
	for _, at := range []int{len(pre) + 2, len(pre) + 40, len(raw) / 2, len(raw) - 1} {
		bad := bytes.Clone(raw)
		bad[at] ^= 0x01
		if _, _, err := Read(bytes.NewReader(bad), f.key, nil); !errors.Is(err, ErrCorrupt) {
			t.Errorf("byte %d changed: %v", at, err)
		}
	}
	// A changed header (here: the claimed install) fails the AAD.
	h, _, _ := ReadHeader(bytes.NewReader(raw))
	h.InstallID = "install-2"
	if _, _, err := Read(bytes.NewReader(reheader(t, raw, pre, h)), f.key, nil); !errors.Is(err, ErrCorrupt) {
		t.Errorf("changed header: %v", err)
	}
	// Data after the last chunk.
	if _, _, err := Read(bytes.NewReader(append(bytes.Clone(raw), 0)), f.key, nil); !errors.Is(err, ErrCorrupt) {
		t.Errorf("trailing data: %v", err)
	}
	// Bad magic and version.
	bad := bytes.Clone(raw)
	bad[0] = 'X'
	if _, _, err := Read(bytes.NewReader(bad), f.key, nil); !errors.Is(err, ErrFormat) {
		t.Errorf("magic: %v", err)
	}
	h, _, _ = ReadHeader(bytes.NewReader(raw))
	h.V = 2
	if _, _, err := Read(bytes.NewReader(reheader(t, raw, pre, h)), f.key, nil); !errors.Is(err, ErrFormat) {
		t.Errorf("version: %v", err)
	}
}

// chunks splits the chunk part of a file.
func chunks(t *testing.T, raw []byte) (pre []byte, list [][]byte) {
	t.Helper()
	_, pre, err := ReadHeader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	rest := raw[len(pre):]
	for len(rest) > 0 {
		n := int(binary.BigEndian.Uint32(rest))
		list = append(list, rest[:4+n])
		rest = rest[4+n:]
	}
	return pre, list
}

func encrypt(t *testing.T, k secret.Key, plain []byte) []byte {
	t.Helper()
	h, err := newHeader(k, Header{CreatedAt: "2026-11-13T03:00:00Z", InstallID: "i"})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := newWriter(&buf, k, h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decrypt(raw []byte, k secret.Key) ([]byte, error) {
	_, r, err := Open(bytes.NewReader(raw), k)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// The chunk stream: sizes at the chunk edges, reorder, drop, swap.
func TestChunks(t *testing.T) {
	k := secret.NewKey()
	for _, n := range []int{0, 1, ChunkSize - 1, ChunkSize, ChunkSize + 1, 2 * ChunkSize, 2*ChunkSize + 7} {
		plain := make([]byte, n)
		rand.Read(plain)
		raw := encrypt(t, k, plain)
		got, err := decrypt(raw, k)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%d bytes: %v (got %d)", n, err, len(got))
		}
		_, list := chunks(t, raw)
		// The last chunk holds 0 … ChunkSize bytes: an exact multiple
		// ends with a full last chunk.
		if want := max(1, (n+ChunkSize-1)/ChunkSize); len(list) != want {
			t.Errorf("%d bytes: %d chunks, want %d", n, len(list), want)
		}
	}
	plain := make([]byte, 2*ChunkSize+7)
	rand.Read(plain)
	raw := encrypt(t, k, plain)
	pre, list := chunks(t, raw)
	join := func(parts ...[]byte) []byte { return bytes.Join(append([][]byte{pre}, parts...), nil) }
	cases := map[string][]byte{
		"reordered":         join(list[1], list[0], list[2]),
		"first dropped":     join(list[1], list[2]),
		"middle dropped":    join(list[0], list[2]),
		"last dropped":      join(list[0], list[1]),
		"last duplicated":   join(list[0], list[1], list[2], list[2]),
		"chunk of a second": nil,
	}
	other := encrypt(t, k, plain) // same key and plaintext, another salt
	_, otherList := chunks(t, other)
	cases["chunk of a second"] = join(list[0], otherList[1], list[2])
	for name, b := range cases {
		if _, err := decrypt(b, k); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The info labels in secret are the ones of the format.
func TestKeyDerivation(t *testing.T) {
	k := secret.NewKey()
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ReplaceAll(k.Recovery(), "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	master := raw[:secret.KeySize]
	fp, _ := hkdf.Key(sha256.New, master, nil, KDFInfoFingerprint, FingerprintBytes)
	if hex.EncodeToString(fp) != Fingerprint(k) {
		t.Error("fingerprint label differs")
	}
	salt := []byte("0123456789abcdef0123456789abcdef")
	dk, _ := hkdf.Key(sha256.New, master, salt, KDFInfoData, 32)
	if !bytes.Equal(dk, k.BackupDataKey(salt)) {
		t.Error("data key label differs")
	}
}

func TestRetention(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Keep = 3 })
	var names []string
	for i := range 5 {
		f.clock.set(time.Date(2026, 11, 13, 10, 0, i, 0, time.UTC))
		names = append(names, f.backup("system").Name)
	}
	// Two in the same second get -1.
	second := f.backup("user:admin").Name
	if second != "plumb-20261113T100004Z-1.pbk" {
		t.Fatalf("same second: %q", second)
	}
	items, err := f.svc.Items()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.Name)
	}
	want := []string{second, names[4], names[3]}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("kept %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(f.data, DirName, names[0]+SumSuffix)); !os.IsNotExist(err) {
		t.Error("sum file of a removed backup stays")
	}
	// The pointer protects its file even when it is not among the newest.
	dir := filepath.Join(f.data, DirName)
	for _, n := range []string{"plumb-20261113T120000Z.pbk", "plumb-20261113T120001Z.pbk", "plumb-20261113T120002Z.pbk"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	os.WriteFile(filepath.Join(dir, PointerName), []byte(`{"name":"`+names[3]+`","size":1,"sha256":"x"}`), 0o600)
	if err := f.svc.retain(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, names[3])); err != nil {
		t.Errorf("the pointer file was removed: %v", err)
	}
	for _, gone := range []string{second, names[4]} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s stays", gone)
		}
	}
}

func TestBackupFailureVisible(t *testing.T) {
	f := newFixture(t, nil)
	os.Remove(filepath.Join(f.data, "link", "ca.key"))
	run, err := f.svc.RunNow(f.ctx, "system")
	if err != nil {
		t.Fatal(err)
	}
	if run.Result != store.BackupFailed || !strings.Contains(run.Message, "link/ca.key") {
		t.Fatalf("run: %+v", run)
	}
	evs, _ := f.st.EventsAfter(f.ctx, 0, 100)
	if len(evs) == 0 || evs[len(evs)-1].Type != "backup.finished" || !strings.Contains(string(evs[len(evs)-1].Data), `"failed"`) {
		t.Errorf("events: %+v", evs)
	}
	ents, _ := os.ReadDir(filepath.Join(f.data, DirName))
	if len(ents) != 0 {
		t.Errorf("files after a failed backup: %v", ents)
	}
}

func TestOneAtATimeAndInterrupted(t *testing.T) {
	f := newFixture(t, nil)
	f.svc.mu.Lock()
	if _, err := f.svc.Start(f.ctx, "user:a"); !errors.Is(err, ErrRunning) {
		t.Errorf("second backup: %v", err)
	}
	f.svc.mu.Unlock()
	if _, err := f.svc.Start(f.ctx, "user:a"); err != nil {
		t.Fatal(err)
	}
	f.svc.Wait()
	// A running row of a stopped serve is failed at the next start.
	if _, err := f.st.StartBackupRun(f.ctx, "system", "plumb-20261113T100001Z.pbk"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(f.data, DirName, TempPrefix+"plumb-x"), []byte("half"), 0o600)
	if _, err := New(f.ctx, f.svc.cfg); err != nil {
		t.Fatal(err)
	}
	if r, _ := f.st.LastBackupRun(f.ctx, ""); r.Result != store.BackupFailed || !strings.Contains(r.Message, "interrupted") {
		t.Errorf("cut run: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(f.data, DirName, TempPrefix+"plumb-x")); !os.IsNotExist(err) {
		t.Error("temp file of a cut run stays")
	}
}

func TestDue(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Time = "03:00" })
	f.clock.set(time.Date(2026, 11, 13, 2, 0, 0, 0, time.UTC))
	if due, _ := f.svc.Due(f.ctx); !due {
		t.Error("no backup yet: due")
	}
	f.backup(SystemActor) // at 02:00, before today's slot: yesterday's slot is done
	if due, _ := f.svc.Due(f.ctx); due {
		t.Error("due right after a scheduled backup")
	}
	f.clock.set(time.Date(2026, 11, 13, 3, 0, 0, 0, time.UTC))
	if due, _ := f.svc.Due(f.ctx); !due {
		t.Error("03:00: due")
	}
	f.backup("user:admin") // a manual one does not count for the schedule
	if due, _ := f.svc.Due(f.ctx); !due {
		t.Error("after a manual backup: still due")
	}
	f.backup(SystemActor)
	f.clock.set(time.Date(2026, 11, 14, 2, 59, 0, 0, time.UTC))
	if due, _ := f.svc.Due(f.ctx); due {
		t.Error("before the next slot: not due")
	}
	for _, bad := range []string{"3:00", "24:00", "12:60", "ab:cd", ""} {
		if _, _, err := ParseTime(bad); err == nil && bad != "" {
			t.Errorf("time %q accepted", bad)
		}
	}
	if _, err := New(f.ctx, Config{Store: f.st, DataDir: f.data, Keep: 91}); err == nil {
		t.Error("keep 91 accepted")
	}
}

func TestCopyState(t *testing.T) {
	f := newFixture(t, nil)
	if c := f.svc.CopyState(); c.State != "none" {
		t.Errorf("no file: %+v", c)
	}
	write := func(s string) { os.WriteFile(f.copy, []byte(s), 0o644) }
	write(`{"v":1,"at":"2026-11-13T03:30:02Z","result":"ok","name":"plumb-20261113T030000Z.pbk","sha256":"ab","storage":"nas","message":""}`)
	if c := f.svc.CopyState(); c.State != "ok" || c.Name != "plumb-20261113T030000Z.pbk" || c.Storage != "nas" {
		t.Errorf("ok: %+v", c)
	}
	f.clock.set(time.Date(2026, 11, 14, 16, 0, 0, 0, time.UTC))
	if c := f.svc.CopyState(); c.State != "stale" {
		t.Errorf("37 h later: %+v", c)
	}
	write(`{"v":1,"at":"2026-11-14T03:30:02Z","result":"failed","name":"","sha256":"","storage":"nas","message":"storage nas is not mounted"}`)
	if c := f.svc.CopyState(); c.State != "failed" || c.Message != "storage nas is not mounted" {
		t.Errorf("failed: %+v", c)
	}
	for _, bad := range []string{
		`{"v":2,"at":"2026-11-14T03:30:02Z","result":"ok","name":"","sha256":"","storage":"","message":""}`,
		`{"v":1,"at":"x","result":"ok","name":"","sha256":"","storage":"","message":""}`,
		`{"v":1,"at":"2026-11-14T03:30:02Z","result":"ok","name":"../etc/passwd","sha256":"","storage":"","message":""}`,
		`{"v":1,"at":"2026-11-14T03:30:02Z","result":"maybe","name":"","sha256":"","storage":"","message":""}`,
		`{"v":1,"extra":1}`, `not json`,
	} {
		write(bad)
		if c := f.svc.CopyState(); c.State != "none" {
			t.Errorf("%s: %+v", bad, c)
		}
	}
	os.Remove(f.copy)
	os.Symlink("/etc/hostname", f.copy)
	if c := f.svc.CopyState(); c.State != "none" {
		t.Errorf("symlink: %+v", c)
	}
}
