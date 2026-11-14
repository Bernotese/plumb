package backup

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/store"
)

type memDriver struct {
	vols     map[string][]Volume // guest → volumes
	seq      int
	enc      bool
	free     int
	noNotes  bool
	fail     bool
	notes    []string
	excludes [][]string
	deleted  []string
	clock    *time.Time
	rewrite  func(string) string // changes the notes that vzdump writes
	block    chan struct{}       // non-nil: BackUpGuest waits for ctx end
}

func (d *memDriver) BackUpGuest(ctx context.Context, storage, guest, notes string, ex []string) (string, bool, error) {
	if d.fail {
		return "", false, errors.New("vzdump failed")
	}
	if d.block != nil {
		close(d.block)
		<-ctx.Done()
		return "", false, ctx.Err()
	}
	d.seq++
	d.notes = append(d.notes, notes)
	d.excludes = append(d.excludes, ex)
	n := notes
	if d.noNotes {
		n = ""
	}
	if d.rewrite != nil {
		n = d.rewrite(n)
	}
	*d.clock = d.clock.Add(time.Minute)
	v := Volume{Volid: fmt.Sprintf("%s:backup/vzdump-%s-%03d", storage, guest, d.seq), Time: *d.clock, Notes: n}
	d.vols[guest] = append(d.vols[guest], v)
	return v.Volid, d.noNotes, nil
}

func (d *memDriver) ListGuestBackups(_ context.Context, _, guest string) ([]Volume, error) {
	return append([]Volume(nil), d.vols[guest]...), nil
}

func (d *memDriver) DeleteGuestBackup(_ context.Context, _, guest, volid string) error {
	for i, v := range d.vols[guest] {
		if v.Volid == volid {
			d.vols[guest] = append(d.vols[guest][:i], d.vols[guest][i+1:]...)
			d.deleted = append(d.deleted, volid)
			return nil
		}
	}
	return errors.New("not found")
}

func (d *memDriver) BackupStorageState(context.Context, string) (StorageState, error) {
	return StorageState{Encrypted: d.enc, FreePct: d.free}, nil
}

type jobEnv struct {
	g      *Guests
	st     *store.Store
	d      *memDriver
	t      *GuestTarget
	now    time.Time
	restor bool
}

func newJobEnv(t *testing.T, mut func(*GuestConfig)) *jobEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.PutBinding(ctx, store.Binding{Kind: "Target", Name: "db1", Ref: "9001"}); err != nil {
		t.Fatal(err)
	}
	b, _ := st.GetBinding(ctx, "Target", "db1")
	e := &jobEnv{st: st, now: time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC)}
	e.d = &memDriver{vols: map[string][]Volume{}, enc: true, free: 50, clock: &e.now}
	e.t = &GuestTarget{Name: "db1", Guest: "9001", Gen: b.Gen, Backup: &GuestSchedule{Schedule: "daily", Hour: 3, Keep: 3}}
	cfg := GuestConfig{Store: st, Events: event.New(st), Driver: e.d, InstallID: tInstall, Storage: "plumb-backup",
		Restored: func() bool { return e.restor },
		Target: func(_ context.Context, name string) (GuestTarget, error) {
			if name != e.t.Name {
				return GuestTarget{}, store.ErrNotFound
			}
			return *e.t, nil
		},
		Targets: func(context.Context) ([]GuestTarget, error) { return []GuestTarget{*e.t}, nil },
		Now:     func() time.Time { return e.now }}
	if mut != nil {
		mut(&cfg)
	}
	e.g = NewGuests(cfg)
	return e
}

// M1 of #325: keep+1 "back up now" calls never push out a scheduled
// backup; retention deletes only the Target's own volumes (D-407).
func TestGuestBackupRetention(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.d.vols["9001"] = []Volume{{Volid: "pb:backup/person", Time: e.now.Add(-time.Hour), Notes: "my own"}}
	for range 3 {
		if err := e.g.BackUp(ctx, "db1", TriggerScheduled, "system"); err != nil {
			t.Fatal(err)
		}
	}
	for range 4 { // keep+1
		if err := e.g.BackUp(ctx, "db1", TriggerManual, "token:tok_qa"); err != nil {
			t.Fatal(err)
		}
	}
	list, err := e.g.List(ctx, "db1")
	if err != nil {
		t.Fatal(err)
	}
	n := map[string]int{}
	for _, it := range list.Items {
		n[it.Kind+"/"+it.Trigger]++
		if strings.Contains(it.ID, ":") || len(it.ID) != 16 {
			t.Fatalf("a volume id leaks: %q", it.ID)
		}
	}
	if n["plumb/scheduled"] != 3 || n["plumb/manual"] != MaxManualKept || n["unmarked/unknown"] != 1 {
		t.Fatalf("after keep+1 manual: %v", n)
	}
	for _, v := range e.d.deleted {
		if !strings.Contains(v, "-00") || v == "pb:backup/person" {
			t.Fatalf("deleted %s", v)
		}
	}
	if !strings.HasSuffix(e.d.notes[len(e.d.notes)-1], ":m") || strings.HasSuffix(e.d.notes[0], ":m") {
		t.Fatalf("markers %v", e.d.notes)
	}
	if list.Storage == nil || list.Storage.State != StoragePBSKey || list.Last == nil || list.Last.Trigger != TriggerManual {
		t.Fatalf("list %+v", list)
	}
}

// D-417 (M3 of #325): a new guest of the Target (a new binding
// generation) starts a new lineage; the old guest's volumes become
// older-guest and are never deleted.
func TestGuestBackupNewGeneration(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	for range 3 {
		e.g.BackUp(ctx, "db1", TriggerScheduled, "system")
	}
	// Re-create on the same VMID: a new generation.
	e.st.PutBinding(ctx, store.Binding{Kind: "Target", Name: "db1", Ref: "9050"})
	e.st.PutBinding(ctx, store.Binding{Kind: "Target", Name: "db1", Ref: "9001"})
	b, _ := e.st.GetBinding(ctx, "Target", "db1")
	e.t.Gen = b.Gen
	e.t.Backup.Keep = 1
	before := len(e.d.deleted)
	for range 2 {
		e.g.BackUp(ctx, "db1", TriggerScheduled, "system")
	}
	list, _ := e.g.List(ctx, "db1")
	n := map[string]int{}
	for _, it := range list.Items {
		n[it.Kind]++
	}
	if n["older-guest"] != 3 || n["plumb"] != 1 || len(e.d.deleted)-before != 1 {
		t.Fatalf("kinds %v, deleted %d", n, len(e.d.deleted)-before)
	}
	// An older-guest volume can be deleted by a person; an unmarked one never.
	var older string
	for _, it := range list.Items {
		if it.Kind == KindOlderGuest {
			older = it.ID
		}
	}
	if err := e.g.Delete(ctx, "db1", older, "user:admin"); err != nil {
		t.Fatal(err)
	}
	e.d.vols["9001"] = append(e.d.vols["9001"], Volume{Volid: "pb:backup/person", Notes: "x"})
	if err := e.g.Delete(ctx, "db1", VolumeID("pb:backup/person"), "user:admin"); !errors.Is(err, ErrNotPlumbBackup) {
		t.Fatalf("unmarked delete: %v", err)
	}
	if err := e.g.Delete(ctx, "db1", "0000000000000000", "user:admin"); !errors.Is(err, ErrGuestBackupNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

// The marker is read back: no notes → warning, the volume is unmarked
// and retention never deletes it.
func TestGuestBackupMarkerReadBack(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.d.noNotes = true
	e.t.Backup.Keep = 1
	for range 3 {
		if err := e.g.BackUp(ctx, "db1", TriggerScheduled, "system"); err != nil {
			t.Fatal(err)
		}
	}
	last, _ := e.st.LastGuestBackupRun(ctx, "db1")
	if last.Result != "warning" || !strings.Contains(last.Message, "no Plumb marker") {
		t.Fatalf("run %+v", last)
	}
	if len(e.d.deleted) != 0 || len(e.d.vols["9001"]) != 3 {
		t.Fatalf("unmarked volumes deleted: %v", e.d.deleted)
	}
}

// Refusals: restored mode, clear storage without the flag, no space, no
// install id, no storage; one job at a time.
func TestGuestBackupRefusals(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		mut  func(*GuestConfig)
		prep func(*jobEnv)
		want string
	}{
		"restored":   {nil, func(e *jobEnv) { e.restor = true }, "restored mode"},
		"clear":      {nil, func(e *jobEnv) { e.d.enc = false }, "--guest-backup-clear"},
		"full":       {nil, func(e *jobEnv) { e.d.free = 9 }, "9 % free"},
		"no install": {func(c *GuestConfig) { c.InstallID = "" }, nil, "no install id"},
		"no storage": {func(c *GuestConfig) { c.Storage = "" }, nil, "no guest backup storage"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newJobEnv(t, c.mut)
			if c.prep != nil {
				c.prep(e)
			}
			err := e.g.BackUp(ctx, "db1", TriggerManual, "user:admin")
			if !errors.Is(err, ErrGuestBackupRefused) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v", err)
			}
			if len(e.d.notes) != 0 {
				t.Fatal("vzdump ran")
			}
			last, _ := e.st.LastGuestBackupRun(ctx, "db1")
			if last.Result != "refused" {
				t.Fatalf("run %+v", last)
			}
		})
	}
	// A clear storage with the install flag runs.
	e := newJobEnv(t, func(c *GuestConfig) { c.Clear = true })
	e.d.enc = false
	if err := e.g.BackUp(ctx, "db1", TriggerManual, "user:admin"); err != nil {
		t.Fatalf("stated clear: %v", err)
	}
	if l, _ := e.g.List(ctx, "db1"); l.Storage.State != StorageStatedClear {
		t.Fatalf("state %+v", l.Storage)
	}
	// One job at a time.
	if _, err := e.st.StartGuestBackupRun(ctx, "db1", "0123456789abcdef", TriggerScheduled); err != nil {
		t.Fatal(err)
	}
	if err := e.g.BackUp(ctx, "db1", TriggerManual, "user:admin"); !errors.Is(err, ErrGuestBackupBusy) {
		t.Fatalf("second job: %v", err)
	}
}

// A container archive leaves the agent's files out; a VM's holds all.
func TestGuestBackupExcludes(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.g.BackUp(ctx, "db1", TriggerManual, "x")
	e.t.LXC = true
	e.g.BackUp(ctx, "db1", TriggerManual, "x")
	if len(e.d.excludes[0]) != 0 || len(e.d.excludes[1]) != 2 {
		t.Fatalf("%v", e.d.excludes)
	}
}

// The schedule: one job per slot, no make-up after a start, weekly on
// its day.
func TestGuestBackupSchedule(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.g.Tick(ctx) // 02:00: learns the slot
	e.now = time.Date(2026, 11, 15, 3, 0, 30, 0, time.UTC)
	e.g.Tick(ctx)
	e.g.Tick(ctx)
	if len(e.d.notes) != 1 {
		t.Fatalf("%d jobs at the 03:00 slot", len(e.d.notes))
	}
	if got := NextAt(*e.t.Backup, e.now); !got.Equal(time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("next %v", got)
	}
	w := GuestSchedule{Schedule: "weekly", Weekday: time.Wednesday, Hour: 4}
	if got := NextAt(w, e.now); got.Weekday() != time.Wednesday || got.Hour() != 4 {
		t.Fatalf("weekly next %v", got)
	}
	// A fresh runner after a restart does not make up the slot.
	e2 := NewGuests(e.g.cfg)
	e2.Tick(ctx)
	if len(e.d.notes) != 1 {
		t.Fatal("a restart made up a slot")
	}
}

// n1 of #331 (K3): the read-back checks the lineage too: a marker of
// another lineage on the new volume → warning, never recorded as done.
func TestGuestBackupReadBackLineage(t *testing.T) {
	e := newJobEnv(t, nil)
	e.d.rewrite = func(n string) string { return strings.Replace(n, tLineageOf(n), "fedcba9876543210", 1) }
	if err := e.g.BackUp(context.Background(), "db1", TriggerScheduled, "system"); err != nil {
		t.Fatal(err)
	}
	last, _ := e.st.LastGuestBackupRun(context.Background(), "db1")
	if last.Result != "warning" {
		t.Fatalf("run %+v", last)
	}
}

func tLineageOf(marker string) string {
	m, _ := ParseMarker(marker)
	return m.Lineage
}

// M3 of #331: "back up now" on a Target without a backup field keeps at
// most MaxManualKept manual volumes.
func TestGuestBackupManualCapWithoutField(t *testing.T) {
	e := newJobEnv(t, nil)
	e.t.Backup = nil
	for range 5 {
		if err := e.g.BackUp(context.Background(), "db1", TriggerManual, "token:tok_qa"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e.d.vols["9001"]); n != MaxManualKept {
		t.Fatalf("%d manual volumes kept, want %d", n, MaxManualKept)
	}
}

// M1 of #331: a lost record (a controller restore) makes the volumes
// unmarked: retention never deletes them, the storage only grows.
func TestGuestBackupLostRecord(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.t.Backup.Keep = 1
	e.g.BackUp(ctx, "db1", TriggerScheduled, "system")
	e.g.BackUp(ctx, "db1", TriggerScheduled, "system")
	if len(e.d.vols["9001"]) != 1 {
		t.Fatalf("setup: %d volumes", len(e.d.vols["9001"]))
	}
	// The record is gone: drop the binding (its rows go) and bind again.
	if err := e.st.DeleteBinding(ctx, "Target", "db1"); err != nil {
		t.Fatal(err)
	}
	e.st.PutBinding(ctx, store.Binding{Kind: "Target", Name: "db1", Ref: "9001"})
	b, _ := e.st.GetBinding(ctx, "Target", "db1")
	e.t.Gen = b.Gen
	before := len(e.d.deleted)
	e.g.BackUp(ctx, "db1", TriggerScheduled, "system")
	e.g.BackUp(ctx, "db1", TriggerScheduled, "system")
	l, _ := e.g.List(ctx, "db1")
	unmarked := 0
	for _, it := range l.Items {
		if it.Kind == KindUnmarked {
			unmarked++
		}
	}
	if unmarked != 1 || len(e.d.deleted)-before != 1 {
		t.Fatalf("unmarked %d, deleted %d (only the new lineage's own)", unmarked, len(e.d.deleted)-before)
	}
}

// M2 of #331: Close during a running job (background and schedule) ends
// it; the row ends failed and a new job of the Target can start.
func TestGuestBackupCloseDuringJob(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.d.block = make(chan struct{})
	run, err := e.g.Start(ctx, "db1", TriggerManual, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	e.g.Background("db1", run)
	<-e.d.block
	e.g.Close()
	last, _ := e.st.LastGuestBackupRun(ctx, "db1")
	if last.Result != "failed" || !strings.Contains(last.Message, "controller stopped") {
		t.Fatalf("run after Close %+v", last)
	}
	e.d.block = nil
	g2 := NewGuests(e.g.cfg)
	defer g2.Close()
	if err := g2.BackUp(ctx, "db1", TriggerManual, "user:admin"); err != nil {
		t.Fatalf("a new job after Close: %v", err)
	}
	// The schedule: a tick that runs a job at Close is ended and waited for.
	e.d.block = make(chan struct{})
	g3 := NewGuests(e.g.cfg)
	g3.last["db1"] = time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	e.now = time.Date(2026, 11, 15, 3, 0, 1, 0, time.UTC)
	g3.StartSchedule(time.Hour)
	<-e.d.block
	g3.Close()
	if last, _ := e.st.LastGuestBackupRun(ctx, "db1"); last.Result != "failed" {
		t.Fatalf("scheduled run after Close %+v", last)
	}
	// Recover ends a row that a crash left running.
	if _, err := e.st.StartGuestBackupRun(ctx, "db1", "0123456789abcdef", TriggerScheduled); err != nil {
		t.Fatal(err)
	}
	if err := g2.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if last, _ := e.st.LastGuestBackupRun(ctx, "db1"); last.Result != "failed" {
		t.Fatalf("after Recover %+v", last)
	}
}
