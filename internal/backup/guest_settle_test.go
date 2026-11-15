package backup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// settleEnv is a job env with a row that a crash left running, with its
// task recorded (T-248). It returns the row id, the task's UPID and the
// marker that the job writes.
func settleEnv(t *testing.T) (*jobEnv, int64, string, string) {
	t.Helper()
	e := newClockedEnv(t)
	ctx := context.Background()
	lineage, err := e.st.BackupLineage(ctx, "db1", e.t.Gen)
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.st.StartGuestBackupRun(ctx, "db1", lineage, TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	upid := "UPID:pve:00000099:vzdump:9001"
	e.d.tasks = map[string]*GuestTask{upid: {Running: true}}
	if err := e.st.SetGuestBackupTask(ctx, id, upid); err != nil {
		t.Fatal(err)
	}
	if err := e.g.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	marker, _ := Marker{Install: tInstall, Target: "db1", Lineage: lineage, Manual: true}.String()
	return e, id, upid, marker
}

// newClockedEnv is a job env whose store uses the env clock, so a job's
// start and its volume's time compare as on a real host.
func newClockedEnv(t *testing.T) *jobEnv {
	t.Helper()
	e := newJobEnv(t, nil)
	e.st.Now = func() time.Time { return e.now }
	return e
}

func settleKinds(t *testing.T, e *jobEnv) map[string]int {
	t.Helper()
	list, err := e.g.List(context.Background(), "db1")
	if err != nil {
		t.Fatal(err)
	}
	n := map[string]int{}
	for _, it := range list.Items {
		n[it.Kind+"/"+it.Trigger]++
	}
	return n
}

func runRow(t *testing.T, e *jobEnv, id int64) (result, msg string) {
	t.Helper()
	r, err := e.st.GuestBackupRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r.Result, r.Message
}

// Host finding F1 of T-244: a hand vzdump ends in the job's window. The
// job's own task names its volume, which carries the job's marker: the
// record is completed at once, and the hand volume stays unmarked.
func TestSettleOverlapClaimsOwnVolume(t *testing.T) {
	e := newClockedEnv(t)
	ctx := context.Background()
	e.d.overlap = true
	if err := e.g.BackUp(ctx, "db1", TriggerManual, "user:admin"); err != nil {
		t.Fatalf("the proven job must be done: %v", err)
	}
	last, _ := e.st.LastGuestBackupRun(ctx, "db1")
	if last.Result != "done" || !strings.Contains(last.Message, "its own Proxmox task made it") || last.Volid != "plumb-backup:backup/vzdump-9001-001" {
		t.Fatalf("row %+v", last)
	}
	if n := settleKinds(t, e); n["plumb/manual"] != 1 || n["unmarked/unknown"] != 1 {
		t.Fatalf("kinds %v", n)
	}
	// The safety backup before a restore gets the proven volume too.
	c, err := e.g.SafetyBackup(ctx, "db1", "user:admin")
	if err != nil || c.Trigger != TriggerPreRestore {
		t.Fatalf("safety backup: %+v %v", c, err)
	}
}

// Without proof nothing is claimed, and the job's text says what stays
// and who deletes it (the UI text is true).
func TestSettleNoProofClaimsNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		prep func(e *jobEnv)
		want string
	}{
		{"foreign marker", func(e *jobEnv) {
			e.d.overlap = true
			e.d.rewrite = func(string) string { return "plumb:other-install:db1:0123456789abcdef:m" }
		}, "does not carry this job's marker"},
		{"marker of another trigger", func(e *jobEnv) {
			e.d.overlap = true
			e.d.rewrite = func(n string) string { return strings.TrimSuffix(n, ":m") }
		}, "does not carry this job's marker"},
		{"task failed after its archive", func(e *jobEnv) { e.d.failLeft = true }, "may be incomplete; Plumb does not claim it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newClockedEnv(t)
			ctx := context.Background()
			c.prep(e)
			if err := e.g.BackUp(ctx, "db1", TriggerManual, "user:admin"); err == nil {
				t.Fatal("the job must fail")
			}
			last, _ := e.st.LastGuestBackupRun(ctx, "db1")
			if last.Result != "failed" || last.Volid != "" || !strings.Contains(last.Message, c.want) ||
				!strings.Contains(last.Message, "a person deletes it in Proxmox") {
				t.Fatalf("row %+v", last)
			}
			if n := settleKinds(t, e); n["plumb/manual"] != 0 {
				t.Fatalf("claimed without proof: %v", n)
			}
			// Settled once: a later tick does not change it.
			e.g.Tick(ctx)
			if again, _ := e.st.LastGuestBackupRun(ctx, "db1"); again.Message != last.Message {
				t.Fatalf("settled twice: %q", again.Message)
			}
		})
	}
}

// A crash during the task: Recover ends the row, its task is kept. While
// the task runs in Proxmox, no new job of the guest starts. When it ended
// OK with the job's volume, the next tick completes the record.
func TestSettleAfterCrash(t *testing.T) {
	e, id, upid, marker := settleEnv(t)
	ctx := context.Background()
	if res, _ := runRow(t, e, id); res != "failed" {
		t.Fatalf("after Recover: %s", res)
	}
	err := e.g.BackUp(ctx, "db1", TriggerManual, "user:admin")
	if !errors.Is(err, ErrGuestBackupRefused) || !strings.Contains(err.Error(), "earlier backup job of this guest still runs") {
		t.Fatalf("a job while the old task runs: %v", err)
	}
	vol := Volume{Volid: "plumb-backup:backup/vzdump-9001-late", Time: e.now, Size: 7, Notes: marker}
	e.d.vols["9001"] = append(e.d.vols["9001"], vol)
	e.d.tasks[upid] = &GuestTask{OK: true, Volid: vol.Volid}
	e.g.Tick(ctx)
	if res, msg := runRow(t, e, id); res != "done" || !strings.Contains(msg, "completed the record") {
		t.Fatalf("after the tick: %s %q", res, msg)
	}
	if n := settleKinds(t, e); n["plumb/manual"] != 1 {
		t.Fatalf("kinds %v", n)
	}
	if err := e.g.BackUp(ctx, "db1", TriggerManual, "user:admin"); err != nil {
		t.Fatalf("a job after the old task ended: %v", err)
	}
}

// A stop through Close: the driver's wait asked Proxmox to stop the task,
// which left nothing. The next job's start settles it with a true text.
func TestSettleAfterClose(t *testing.T) {
	e := newClockedEnv(t)
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
	e.d.block = nil
	g2 := NewGuests(e.g.cfg)
	defer g2.Close()
	if err := g2.BackUp(ctx, "db1", TriggerManual, "user:admin"); err != nil {
		t.Fatal(err)
	}
	if _, msg := runRow(t, e, last.ID); !strings.Contains(msg, "it left no volume of this guest") {
		t.Fatalf("message %q", msg)
	}
}

// The other ends of a check: a volume older than the job, a volume that
// another job recorded, a task of another guest, a gone Target, and a task
// that cannot be read for a day.
func TestSettleRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("older volume", func(t *testing.T) {
		e, id, upid, marker := settleEnv(t)
		vol := Volume{Volid: "plumb-backup:backup/old", Time: e.now.Add(-time.Hour), Notes: marker}
		e.d.vols["9001"] = append(e.d.vols["9001"], vol)
		e.d.tasks[upid] = &GuestTask{OK: true, Volid: vol.Volid}
		e.g.Tick(ctx)
		if res, msg := runRow(t, e, id); res != "failed" || !strings.Contains(msg, "older than the job") {
			t.Fatalf("%s %q", res, msg)
		}
	})
	t.Run("recorded by another job", func(t *testing.T) {
		e, id, upid, marker := settleEnv(t)
		vol := Volume{Volid: "plumb-backup:backup/vzdump-9001-taken", Time: e.now, Size: 9, Notes: marker}
		e.d.vols["9001"] = append(e.d.vols["9001"], vol)
		lineage, _ := e.st.BackupLineage(ctx, "db1", e.t.Gen)
		other, err := e.st.StartGuestBackupRun(ctx, "db1", lineage, TriggerManual)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.st.FinishGuestBackupRun(ctx, other, "done", vol.Volid, vol.Size, vol.Time.Unix(), ""); err != nil {
			t.Fatal(err)
		}
		e.d.tasks[upid] = &GuestTask{OK: true, Volid: vol.Volid}
		e.g.Tick(ctx)
		if res, msg := runRow(t, e, id); res != "failed" || !strings.Contains(msg, "already the record of another job") {
			t.Fatalf("%s %q", res, msg)
		}
	})
	t.Run("task of another guest", func(t *testing.T) {
		e, id, _, _ := settleEnv(t)
		e.t.Guest = "9050"
		e.g.Tick(ctx)
		if res, msg := runRow(t, e, id); res != "failed" || !strings.Contains(msg, "not one of this guest") {
			t.Fatalf("%s %q", res, msg)
		}
	})
	t.Run("target gone", func(t *testing.T) {
		e, id, _, _ := settleEnv(t)
		e.t.Name = "other"
		e.g.Tick(ctx)
		if _, msg := runRow(t, e, id); !strings.Contains(msg, "no longer bound") {
			t.Fatalf("%q", msg)
		}
	})
	t.Run("unreadable for a day", func(t *testing.T) {
		e, id, _, _ := settleEnv(t)
		e.d.taskErr = errors.New("proxmox: 403")
		e.g.Tick(ctx)
		if _, msg := runRow(t, e, id); !strings.Contains(msg, "controller stopped") {
			t.Fatalf("settled too early: %q", msg)
		}
		r, _ := e.st.GuestBackupRun(ctx, id)
		e.now = r.FinishedAt.Add(25 * time.Hour)
		e.g.Tick(ctx)
		if res, msg := runRow(t, e, id); res != "failed" || !strings.Contains(msg, "could not be read within a day") {
			t.Fatalf("%s %q", res, msg)
		}
	})
}
