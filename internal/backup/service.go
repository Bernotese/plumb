package backup

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
)

// Defaults of the schedule (D-302): daily at 03:00 UTC, keep 7.
const (
	DefaultTime = "03:00"
	DefaultKeep = 7
	MaxKeep     = 90
)

// SystemActor starts the scheduled backups.
const SystemActor = "system"

// ErrRunning: a backup runs already (409 backup-running).
var ErrRunning = errors.New("backup: a backup is running")

// Config of the backup writer of serve.
type Config struct {
	Store  *store.Store
	Events *event.Log
	Key    secret.Key
	// DataDir is the data directory of serve: plumb.db, link/ and the
	// backup directory DirName are in it.
	DataDir string
	// Time is the daily time (UTC, "HH:MM"), Keep the number of backups.
	Time string
	Keep int
	// Header fields of the install (D-304). Empty in a dev install.
	PlumbVersion, InstallID, Node, Pool, AgentURL string
	// CopyStatus is the status file of the host copy timer; default
	// CopyStatusPath.
	CopyStatus string
	Now        func() time.Time
	Logger     *slog.Logger
	// Serve: this is the service of serve. New then removes temp files of
	// a cut run, fails the runs that a stop cut, and writes InstallFile.
	// "plumb backup now" runs beside serve and does none of it.
	Serve bool
}

// InstallFile in the backup directory holds the header fields of the
// install (no secret), written by serve at start, so "plumb backup now"
// writes the same header without the flags and environment of serve.
const InstallFile = "install.json"

// Install is the content of InstallFile.
type Install struct {
	InstallID string `json:"install_id"`
	Node      string `json:"node"`
	Pool      string `json:"pool"`
	AgentURL  string `json:"agent_url"`
	// Keep is --backup-keep of serve: "plumb backup now" runs the same
	// retention (review L1 of #220: the installer runs it without
	// plumb.env).
	Keep int `json:"keep"`
}

// LoadInstall reads InstallFile of a data directory.
func LoadInstall(dataDir string) (Install, error) {
	b, err := readSmall(filepath.Join(dataDir, DirName, InstallFile), 4096)
	if err != nil {
		return Install{}, fmt.Errorf("read %s (start plumb serve once): %w", InstallFile, err)
	}
	var in Install
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return Install{}, fmt.Errorf("%s: %w", InstallFile, err)
	}
	if in.Keep < 1 || in.Keep > MaxKeep {
		return Install{}, fmt.Errorf("%s: keep %d: restart plumb serve once", InstallFile, in.Keep)
	}
	return in, nil
}

// Service writes, lists and schedules backups.
type Service struct {
	cfg  Config
	dir  string
	hour int
	min  int
	mu   sync.Mutex // one backup at a time, also against the store row
	wg   sync.WaitGroup
}

// ParseTime checks a schedule time "HH:MM" (UTC).
func ParseTime(s string) (hour, minute int, err error) {
	h, m, ok := strings.Cut(s, ":")
	if ok && len(h) == 2 && len(m) == 2 {
		hour, err1 := strconv.Atoi(h)
		minute, err2 := strconv.Atoi(m)
		if err1 == nil && err2 == nil && hour >= 0 && hour < 24 && minute >= 0 && minute < 60 {
			return hour, minute, nil
		}
	}
	return 0, 0, fmt.Errorf("backup time %q: want HH:MM (UTC, 00:00 … 23:59)", s)
}

// New checks the config, makes the backup directory (0700), removes temp
// files of a cut run and ends the runs that a stop cut.
func New(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.DataDir == "" {
		return nil, errors.New("backup: Store and DataDir are required")
	}
	if cfg.Time == "" {
		cfg.Time = DefaultTime
	}
	if cfg.Keep == 0 {
		cfg.Keep = DefaultKeep
	}
	if cfg.Keep < 1 || cfg.Keep > MaxKeep {
		return nil, fmt.Errorf("backup keep %d: want 1 … %d", cfg.Keep, MaxKeep)
	}
	h, m, err := ParseTime(cfg.Time)
	if err != nil {
		return nil, err
	}
	if cfg.CopyStatus == "" {
		cfg.CopyStatus = CopyStatusPath
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	s := &Service{cfg: cfg, dir: filepath.Join(cfg.DataDir, DirName), hour: h, min: m}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return nil, err
	}
	if !cfg.Serve {
		return s, nil
	}
	s.cleanTemps()
	if err := cfg.Store.FailRunningBackups(ctx, "interrupted: the controller stopped during the backup"); err != nil {
		return nil, err
	}
	in, err := json.Marshal(Install{InstallID: cfg.InstallID, Node: cfg.Node, Pool: cfg.Pool, AgentURL: cfg.AgentURL, Keep: cfg.Keep})
	if err != nil {
		return nil, err
	}
	if err := s.writeSmall(InstallFile, append(in, '\n')); err != nil {
		return nil, err
	}
	return s, nil
}

// cleanTemps removes temp files of a cut run.
func (s *Service) cleanTemps() {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), TempPrefix) && e.Type().IsRegular() {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}

// Schedule returns the daily time and the number of backups kept.
func (s *Service) Schedule() (string, int) { return s.cfg.Time, s.cfg.Keep }

// Start starts one backup in the background and returns its run. by is
// the principal. ErrRunning while one runs.
func (s *Service) Start(ctx context.Context, by string) (store.BackupRun, error) {
	run, err := s.begin(ctx, by, store.BackupAPI)
	if err != nil {
		return run, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.mu.Unlock()
		s.finish(context.WithoutCancel(ctx), run)
	}()
	return run, nil
}

// RunNow runs one backup and waits for it. It returns the finished run.
// trigger is store.BackupSchedule or store.BackupCLI.
func (s *Service) RunNow(ctx context.Context, by, trigger string) (store.BackupRun, error) {
	run, err := s.begin(ctx, by, trigger)
	if err != nil {
		return run, err
	}
	defer s.mu.Unlock()
	return s.finish(ctx, run), nil
}

// Wait waits for backups that Start began.
func (s *Service) Wait() { s.wg.Wait() }

// begin takes the lock and records the run. On success the caller owns
// s.mu and must unlock it.
func (s *Service) begin(ctx context.Context, by, trigger string) (store.BackupRun, error) {
	if !s.mu.TryLock() {
		return store.BackupRun{}, ErrRunning
	}
	name, err := s.newName()
	if err != nil {
		s.mu.Unlock()
		return store.BackupRun{}, err
	}
	run, err := s.cfg.Store.StartBackupRun(ctx, by, trigger, name)
	if err != nil {
		s.mu.Unlock()
		if errors.Is(err, store.ErrBackupRunning) {
			return store.BackupRun{}, ErrRunning
		}
		return store.BackupRun{}, err
	}
	return run, nil
}

// newName is plumb-<UTC>.pbk, with -<n> when the name is taken.
func (s *Service) newName() (string, error) {
	base := "plumb-" + s.cfg.Now().UTC().Format(NameLayout)
	for n := 0; n < 1000; n++ {
		name := base + FileSuffix
		if n > 0 {
			name = base + "-" + strconv.Itoa(n) + FileSuffix
		}
		if _, err := os.Lstat(filepath.Join(s.dir, name)); errors.Is(err, os.ErrNotExist) {
			return name, nil
		}
	}
	return "", errors.New("backup: too many backups in one second")
}

// finish writes the file, the retention, and the end of the run.
func (s *Service) finish(ctx context.Context, run store.BackupRun) store.BackupRun {
	err := s.write(ctx, run.Name)
	if err == nil {
		if rerr := s.retain(ctx); rerr != nil {
			// The backup itself is good; the run still says what failed.
			s.cfg.Logger.Warn("backup retention failed", "err", rerr)
		}
	}
	run.Result, run.Message = store.BackupOK, ""
	if err != nil {
		run.Result, run.Message = store.BackupFailed, clipMessage(err.Error())
		s.cfg.Logger.Error("backup failed", "name", run.Name, "err", err)
	} else {
		s.cfg.Logger.Info("backup written", "name", run.Name)
	}
	if ferr := s.cfg.Store.FinishBackupRun(ctx, run.ID, run.Result, run.Message); ferr != nil {
		s.cfg.Logger.Error("record backup run", "err", ferr)
	}
	run.FinishedAt = s.cfg.Now().UTC()
	if s.cfg.Events != nil {
		if _, eerr := s.cfg.Events.Emit(ctx, "backup.finished", "backup:"+run.Name, run.By, map[string]any{"result": run.Result}); eerr != nil {
			s.cfg.Logger.Error("backup event", "err", eerr)
		}
	}
	return run
}

func clipMessage(m string) string {
	if len(m) > 400 {
		return m[:400]
	}
	return m
}

// write writes one backup file (D-302): snapshot, tar, chunks into a temp
// file, fsync, rename; then the sum file and the pointer.
func (s *Service) write(ctx context.Context, name string) (err error) {
	tmpDB := filepath.Join(s.dir, TempPrefix+"db-"+randHex())
	defer os.Remove(tmpDB)
	if err := s.cfg.Store.Snapshot(ctx, tmpDB); err != nil {
		return err
	}
	link := filepath.Join(s.cfg.DataDir, "link")
	files := map[string]string{EntryDB: tmpDB, EntryCACert: filepath.Join(link, "ca.crt"), EntryCAKey: filepath.Join(link, "ca.key")}
	sizes := map[string]int64{}
	for entry, p := range files {
		st, err := os.Lstat(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", entry, err)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", entry)
		}
		sizes[entry] = st.Size()
	}
	schema, err := s.cfg.Store.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	created := s.cfg.Now().UTC().Format(time.RFC3339)
	h, err := newHeader(s.cfg.Key, Header{CreatedAt: created, PlumbVersion: s.cfg.PlumbVersion, SchemaVer: schema,
		InstallID: s.cfg.InstallID, Node: s.cfg.Node, Pool: s.cfg.Pool, AgentURL: s.cfg.AgentURL})
	if err != nil {
		return err
	}
	manifest, err := json.Marshal(Manifest{V: Version, CreatedAt: created, InstallID: s.cfg.InstallID, SchemaVer: schema, Sizes: sizes})
	if err != nil {
		return err
	}

	tmp := filepath.Join(s.dir, TempPrefix+name)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	hash := sha256.New()
	bw := bufio.NewWriterSize(io.MultiWriter(f, hash), 256<<10)
	enc, err := newWriter(bw, s.cfg.Key, h)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(enc)
	mtime := time.Unix(0, 0).UTC()
	add := func(entry string, size int64, mode int64, body io.Reader) error {
		if err := tw.WriteHeader(&tar.Header{Name: entry, Typeflag: tar.TypeReg, Size: size, Mode: mode, ModTime: mtime, Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		n, err := io.Copy(tw, io.LimitReader(body, size))
		if err == nil && n != size {
			err = fmt.Errorf("%s changed size during the backup", entry)
		}
		return err
	}
	if err := add(EntryManifest, int64(len(manifest)), 0o600, strings.NewReader(string(manifest))); err != nil {
		return err
	}
	for _, entry := range PlainEntries[1:] {
		src, err := os.Open(files[entry])
		if err != nil {
			return err
		}
		err = add(entry, sizes[entry], 0o600, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, name)); err != nil {
		return err
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	if err := s.writeSmall(name+SumSuffix, []byte(sum+"  "+name+"\n")); err != nil {
		return err
	}
	ptr, err := json.Marshal(Pointer{Name: name, Size: st.Size(), SHA256: sum})
	if err != nil {
		return err
	}
	return s.writeSmall(PointerName, append(ptr, '\n'))
}

// writeSmall writes a file of the backup dir by temp file and rename.
func (s *Service) writeSmall(name string, data []byte) error {
	tmp := filepath.Join(s.dir, TempPrefix+name+"-"+randHex())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(s.dir, name))
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(s.dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func randHex() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// file is one backup in the directory.
type file struct {
	name  string
	run   int64     // id of its backup_run row; 0: no row (an older file)
	mtime time.Time // when the file was written
}

// files lists the backups, newest first by the order of their runs
// (backup_run.id): neither the time in the name nor the file time
// survives a clock step back (review L3 of #220). A file without a run
// row sorts after them, by file time.
func (s *Service) files(ctx context.Context) ([]file, error) {
	runs, err := s.cfg.Store.BackupRunIDs(ctx)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []file
	for _, e := range ents {
		name := e.Name()
		if !e.Type().IsRegular() || !NameRE.MatchString(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, file{name: name, run: runs[name], mtime: info.ModTime()})
	}
	slices.SortFunc(out, func(a, b file) int {
		if a.run != b.run {
			if a.run == 0 || b.run == 0 {
				if a.run == 0 {
					return 1
				}
				return -1
			}
			if a.run > b.run {
				return -1
			}
			return 1
		}
		if c := b.mtime.Compare(a.mtime); c != 0 {
			return c
		}
		return strings.Compare(b.name, a.name)
	})
	return out, nil
}

// pointer reads the pointer file; ok false when it is missing or bad.
func (s *Service) pointer() (Pointer, bool) {
	b, err := readSmall(filepath.Join(s.dir, PointerName), 4096)
	if err != nil {
		return Pointer{}, false
	}
	var p Pointer
	if json.Unmarshal(b, &p) != nil || !NameRE.MatchString(p.Name) {
		return Pointer{}, false
	}
	return p, true
}

// retain keeps the newest Keep backups, scheduled and manual together,
// and never removes the one that the pointer names.
func (s *Service) retain(ctx context.Context) error {
	list, err := s.files(ctx)
	if err != nil {
		return err
	}
	p, hasPtr := s.pointer()
	var errs []error
	for i, f := range list {
		if i < s.cfg.Keep || hasPtr && f.name == p.Name {
			continue
		}
		for _, n := range []string{f.name, f.name + SumSuffix} {
			if err := os.Remove(filepath.Join(s.dir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Item is one backup of the list.
type Item struct {
	Name      string
	CreatedAt time.Time
	Size      int64
	SHA256    string
}

// Items lists the backups, newest first. The hash comes from the sum
// file; a backup without a good sum file is hashed now.
func (s *Service) Items(ctx context.Context) ([]Item, error) {
	list, err := s.files(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(list))
	for _, f := range list {
		p := filepath.Join(s.dir, f.name)
		st, err := os.Lstat(p)
		if err != nil {
			continue
		}
		sum := s.sum(f.name)
		if sum == "" {
			if sum, err = hashFile(p); err != nil {
				continue
			}
		}
		out = append(out, Item{Name: f.name, CreatedAt: st.ModTime().UTC(), Size: st.Size(), SHA256: sum})
	}
	return out, nil
}

// sum reads "<hex>  <name>" of the sum file, or "".
func (s *Service) sum(name string) string {
	b, err := readSmall(filepath.Join(s.dir, name+SumSuffix), 512)
	if err != nil {
		return ""
	}
	hexSum, rest, ok := strings.Cut(strings.TrimSpace(string(b)), "  ")
	if !ok || rest != name || len(hexSum) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(hexSum); err != nil || strings.ToLower(hexSum) != hexSum {
		return ""
	}
	return hexSum
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// readSmall reads a regular file (no link) of at most max bytes.
func readSmall(p string, max int64) ([]byte, error) {
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > max {
		return nil, fmt.Errorf("%s: not a regular file of at most %d bytes", p, max)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max+1))
}

// Copy is the state of the off-host copy (BackupCopy).
type Copy struct {
	State   string // none, ok, failed, stale
	At      time.Time
	Name    string
	Storage string
	Message string
}

// CopyState reads the status file of the host copy timer (D-289). The
// file is host data, but its shape is checked: a bad file is "none".
func (s *Service) CopyState() Copy {
	b, err := readSmall(s.cfg.CopyStatus, 4096)
	if err != nil {
		return Copy{State: "none"}
	}
	var cs CopyStatus
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if dec.Decode(&cs) != nil || cs.V != 1 {
		return Copy{State: "none"}
	}
	at, err := time.Parse(time.RFC3339, cs.At)
	if err != nil || (cs.Result != CopyOK && cs.Result != CopyFailed) ||
		(cs.Name != "" && !NameRE.MatchString(cs.Name)) || len(cs.Storage) > 64 || !printable(cs.Storage) {
		return Copy{State: "none"}
	}
	msg := cs.Message
	if len(msg) > MaxCopyMessage {
		msg = msg[:MaxCopyMessage]
	}
	if !printable(msg) {
		msg = ""
	}
	c := Copy{State: cs.Result, At: at.UTC(), Name: cs.Name, Storage: cs.Storage, Message: msg}
	if cs.Result == CopyOK && s.cfg.Now().Sub(at) > CopyStaleHours*time.Hour {
		c.State = "stale"
	}
	return c
}

func printable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Due reports whether the scheduled backup of today is due: the time of
// today passed and no scheduled backup started since then. A controller
// that was down at the time catches up at its next check.
func (s *Service) Due(ctx context.Context) (bool, error) {
	now := s.cfg.Now().UTC()
	slot := time.Date(now.Year(), now.Month(), now.Day(), s.hour, s.min, 0, 0, time.UTC)
	if now.Before(slot) {
		slot = slot.AddDate(0, 0, -1)
	}
	last, err := s.cfg.Store.LastBackupRun(ctx, store.BackupSchedule)
	if errors.Is(err, store.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return last.StartedAt.Before(slot), nil
}

// RunSchedule checks every interval whether the daily backup is due and
// runs it, until ctx ends. paused reports restored mode (T-113): the
// schedule waits while it is true; nil means never.
func (s *Service) RunSchedule(ctx context.Context, interval time.Duration, paused func() bool) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if paused == nil || !paused() {
			due, err := s.Due(ctx)
			if err != nil {
				s.cfg.Logger.Error("backup schedule", "err", err)
			} else if due {
				if _, err := s.RunNow(ctx, SystemActor, store.BackupSchedule); err != nil && !errors.Is(err, ErrRunning) {
					s.cfg.Logger.Error("scheduled backup", "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
