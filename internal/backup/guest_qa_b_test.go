package backup

// T-226 (slice 7 QA, row C10): the part B of T-221 (#334). Second-opinion
// tests: the count of a Plan's note against the real next job, the
// removed field, the schedule under a race, and restored mode at a slot.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/store"
)

// The keep risk of a Plan comes from KeepDeletes (D-417, M1 of #334). It
// must say exactly what the next scheduled job then deletes, for every
// keep, with manual, unmarked and older-guest volumes beside the scheduled
// ones.
func TestQAKeepDeletesMatchesTheNextJob(t *testing.T) {
	for keep := 1; keep <= 6; keep++ {
		t.Run(fmt.Sprint("keep ", keep), func(t *testing.T) {
			e := newJobEnv(t, nil)
			ctx := context.Background()
			e.t.Backup.Keep = 10 // build up without retention
			for range 5 {
				if err := e.g.BackUp(ctx, "db1", TriggerScheduled, "system"); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				e.g.BackUp(ctx, "db1", TriggerManual, "user:admin")
			}
			e.d.vols["9001"] = append(e.d.vols["9001"], Volume{Volid: "plumb-backup:backup/person", Time: e.now.Add(-time.Hour)})
			want, err := e.g.KeepDeletes(ctx, "db1", keep)
			if err != nil {
				t.Fatal(err)
			}
			e.t.Backup.Keep = keep
			before := len(e.d.deleted)
			if err := e.g.BackUp(ctx, "db1", TriggerScheduled, "system"); err != nil {
				t.Fatal(err)
			}
			if got := len(e.d.deleted) - before; got != want {
				t.Fatalf("KeepDeletes said %d, the job deleted %d", want, got)
			}
		})
	}
}

// Removing the backup field stops the jobs and deletes nothing (D-417):
// three days of slots, no job, no delete. When the field comes back with a
// lower keep, the next slot deletes exactly what KeepDeletes said.
func TestQAFieldRemovedThenBack(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	for range 4 {
		e.g.BackUp(ctx, "db1", TriggerScheduled, "system")
	}
	jobs, have, deleted := len(e.d.notes), len(e.d.vols["9001"]), len(e.d.deleted) // keep 3: one went already
	field := e.t.Backup
	e.t.Backup = nil
	e.g.Tick(ctx) // learns nothing: no field
	for day := range 3 {
		e.now = time.Date(2026, 11, 15+day, 3, 0, 30, 0, time.UTC)
		e.g.Tick(ctx)
	}
	if len(e.d.notes) != jobs || len(e.d.deleted) != deleted || len(e.d.vols["9001"]) != have {
		t.Fatalf("without the field: %d jobs (want %d), deleted %v", len(e.d.notes), jobs, e.d.deleted)
	}
	field.Keep = 1
	e.t.Backup = field
	want, _ := e.g.KeepDeletes(ctx, "db1", 1)
	e.now = time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC)
	e.g.Tick(ctx) // the first tick with the field only learns the slot
	e.now = time.Date(2026, 11, 18, 3, 0, 30, 0, time.UTC)
	e.g.Tick(ctx)
	if len(e.d.notes) != jobs+1 || len(e.d.deleted)-deleted != want || want != 3 {
		t.Fatalf("field back: %d jobs, deleted %d, KeepDeletes %d", len(e.d.notes)-jobs, len(e.d.deleted)-deleted, want)
	}
}

// Two ticks at the same moment (the schedule and a second runner in one
// process): one job per slot, on every slot (run it with -race).
func TestQATickRaceOneJobPerSlot(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	var clock sync.Mutex
	e.g.cfg.Now = func() time.Time { clock.Lock(); defer clock.Unlock(); return e.now }
	e.d.clock = new(time.Time) // the driver's own clock: no write to e.now
	*e.d.clock = e.now
	e.g.Tick(ctx) // learn the slot
	for day := range 5 {
		clock.Lock()
		e.now = time.Date(2026, 11, 15+day, 3, 0, 30, 0, time.UTC)
		clock.Unlock()
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); e.g.Tick(ctx) }()
		}
		wg.Wait()
		if len(e.d.notes) != day+1 {
			t.Fatalf("day %d: %d jobs", day, len(e.d.notes))
		}
	}
}

// Restored mode at a slot (D-303): the job is refused with the reason in
// the run row and no vzdump runs. After the confirm the missed slot is not
// made up; the next slot runs once.
func TestQARestoredModeAtTheSlot(t *testing.T) {
	// D-303 (answer of #339): in restored mode the job waits. Nothing is
	// attempted or recorded; after the confirm the latest slot runs once,
	// then the normal slots follow.
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.g.Tick(ctx)
	e.restor = true
	e.now = time.Date(2026, 11, 15, 3, 0, 30, 0, time.UTC)
	e.g.Tick(ctx)
	if len(e.d.notes) != 0 {
		t.Fatal("a vzdump in restored mode")
	}
	if _, err := e.st.LastGuestBackupRun(ctx, "db1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a run row in restored mode: %v", err)
	}
	e.restor = false
	e.now = time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	e.g.Tick(ctx)
	e.g.Tick(ctx)
	if len(e.d.notes) != 1 {
		t.Fatalf("%d jobs after the confirm, want the missed slot once", len(e.d.notes))
	}
	e.now = time.Date(2026, 11, 16, 3, 0, 30, 0, time.UTC)
	e.g.Tick(ctx)
	e.g.Tick(ctx)
	if len(e.d.notes) != 2 {
		t.Fatalf("%d jobs at the next slot", len(e.d.notes))
	}
}

// A controller that starts in restored mode (plumb restore) keeps the
// latest slot pending: after the confirm it runs once.
func TestRestoredStartRunsTheLatestSlotOnce(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.restor = true
	e.now = time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC) // the 03:00 slot passed
	e.g.Tick(ctx)
	e.now = e.now.Add(time.Hour)
	e.g.Tick(ctx)
	if len(e.d.notes) != 0 {
		t.Fatal("a job in restored mode")
	}
	e.restor = false
	e.g.Tick(ctx)
	e.g.Tick(ctx)
	if len(e.d.notes) != 1 {
		t.Fatalf("%d jobs after the confirm, want 1", len(e.d.notes))
	}
}
