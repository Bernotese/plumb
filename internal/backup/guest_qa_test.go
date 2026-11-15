package backup

// T-226 (slice 7 QA, row C10 of T-215): second-opinion tests of the guest
// backup jobs (T-221). The sketches of pl-reviewer are the base; the parts
// that #331 and #334 test already are not repeated here.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/store"
)

// kinds counts the list items by kind and trigger.
func kinds(t *testing.T, g *Guests, target string) map[string]int {
	t.Helper()
	l, err := g.List(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	n := map[string]int{}
	for _, it := range l.Items {
		n[it.Kind+"/"+it.Trigger]++
	}
	return n
}

// M1 of #331, end to end: keep+3 volumes with a copied marker of this
// Target and lineage and a newer time than every real one never push the
// recorded volumes out; retention never touches them.
func TestQAForgedNewerMarkersKeepRecorded(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.t.Backup.Keep = 2
	for range 2 {
		if err := e.g.BackUp(ctx, "db1", TriggerScheduled, "system"); err != nil {
			t.Fatal(err)
		}
	}
	real := slices.Clone(e.d.vols["9001"])
	marker := real[0].Notes
	for i := range e.t.Backup.Keep + 3 {
		e.d.vols["9001"] = append(e.d.vols["9001"], Volume{Volid: fmt.Sprintf("plumb-backup:backup/vzdump-9001-9%02d", i),
			Time: e.now.Add(time.Duration(i+1) * time.Hour), Notes: marker})
	}
	if err := e.g.BackUp(ctx, "db1", TriggerScheduled, "system"); err != nil {
		t.Fatal(err)
	}
	// Exactly the oldest real one went; the forged ones stay unmarked.
	if !slices.Equal(e.d.deleted, []string{real[0].Volid}) {
		t.Fatalf("deleted %v, want only %s", e.d.deleted, real[0].Volid)
	}
	if n := kinds(t, e.g, "db1"); n["plumb/scheduled"] != 2 || n["unmarked/unknown"] != e.t.Backup.Keep+3 {
		t.Fatalf("kinds %v", n)
	}
	// A person cannot delete a forged one through Plumb either.
	if err := e.g.Delete(ctx, "db1", VolumeID("plumb-backup:backup/vzdump-9001-900"), "user:admin"); !errors.Is(err, ErrNotPlumbBackup) {
		t.Fatalf("delete of a forged volume: %v", err)
	}
}

// Risk §6: a hand vzdump of the same VMID with Plumb's name pattern, with
// no marker or with a marker copied from a recorded volume but another
// size, is never deleted, whatever keep says.
func TestQAForeignVolumeSameNamePattern(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.t.Backup.Keep = 1
	e.g.BackUp(ctx, "db1", TriggerScheduled, "system")
	rec := e.d.vols["9001"][0]
	e.d.vols["9001"] = append([]Volume{
		{Volid: "plumb-backup:backup/vzdump-9001-000", Time: e.now.Add(-48 * time.Hour)},                         // no notes
		{Volid: "plumb-backup:backup/vzdump-9001-0000", Time: e.now.Add(-24 * time.Hour), Notes: "my own backup"}, // a person's notes
	}, e.d.vols["9001"]...)
	// The recorded volume replaced by another archive (F2 of #331): same
	// volid, another size.
	e.d.vols["9001"][2].Size = rec.Size + 4096
	for range 3 {
		if err := e.g.BackUp(ctx, "db1", TriggerScheduled, "system"); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range e.d.deleted {
		if strings.HasSuffix(v, "-000") || strings.HasSuffix(v, "-0000") || v == rec.Volid {
			t.Fatalf("retention deleted a foreign or replaced volume: %s (all %v)", v, e.d.deleted)
		}
	}
	if n := kinds(t, e.g, "db1"); n["unmarked/unknown"] != 3 || n["plumb/scheduled"] != 1 {
		t.Fatalf("kinds %v", n)
	}
}

// M1 of #325 with the jobs mixed: keep=2, S M S M M S M S. After each job
// the newest 2 scheduled and the newest MaxManualKept manual ones stay; a
// manual job never deletes a scheduled volume.
func TestQAMixedManualScheduledRetention(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.t.Backup.Keep = 2
	var made []string // volids in job order
	trig := map[string]string{}
	for _, c := range "SMSMMSMS" {
		tr := TriggerScheduled
		if c == 'M' {
			tr = TriggerManual
		}
		before := len(e.d.deleted)
		if err := e.g.BackUp(ctx, "db1", tr, "system"); err != nil {
			t.Fatal(err)
		}
		v := e.d.vols["9001"][len(e.d.vols["9001"])-1].Volid
		made, trig[v] = append(made, v), tr
		for _, d := range e.d.deleted[before:] {
			if tr == TriggerManual && trig[d] == TriggerScheduled {
				t.Fatalf("a manual job deleted the scheduled volume %s", d)
			}
		}
	}
	var keptS, keptM []string
	for _, v := range e.d.vols["9001"] {
		if trig[v.Volid] == TriggerScheduled {
			keptS = append(keptS, v.Volid)
		} else {
			keptM = append(keptM, v.Volid)
		}
	}
	// Job order: S0 M1 S2 M3 M4 S5 M6 S7.
	if !slices.Equal(keptS, []string{made[5], made[7]}) || !slices.Equal(keptM, []string{made[4], made[6]}) {
		t.Fatalf("kept scheduled %v, manual %v", keptS, keptM)
	}
}

// twoTargets is a job env with db1 on 9001 and web1 on 9002 (one storage).
func twoTargets(t *testing.T) (*Guests, *store.Store, *memDriver, map[string]*GuestTarget) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC)
	d := &memDriver{vols: map[string][]Volume{}, enc: true, free: 50, clock: &now}
	ts := map[string]*GuestTarget{}
	for name, ref := range map[string]string{"db1": "9001", "web1": "9002"} {
		if err := st.PutBinding(ctx, store.Binding{Kind: "Target", Name: name, Ref: ref}); err != nil {
			t.Fatal(err)
		}
		b, _ := st.GetBinding(ctx, "Target", name)
		ts[name] = &GuestTarget{Name: name, Guest: ref, Gen: b.Gen, Backup: &GuestSchedule{Schedule: "daily", Hour: 3, Keep: 1}}
	}
	g := NewGuests(GuestConfig{Store: st, Events: event.New(st), Driver: d, InstallID: tInstall, Storage: "plumb-backup",
		Target: func(_ context.Context, name string) (GuestTarget, error) {
			if x, ok := ts[name]; ok {
				return *x, nil
			}
			return GuestTarget{}, store.ErrNotFound
		},
		Targets: func(context.Context) ([]GuestTarget, error) { return []GuestTarget{*ts["db1"], *ts["web1"]}, nil },
		Now:     func() time.Time { return now }})
	return g, st, d, ts
}

// Two Targets on one storage: one job per Target at a time (a second
// Start of the same Target is busy, another Target starts), and the
// retention of one Target never deletes a volume of the other, also one
// that carries a copy of its marker.
func TestQATwoTargetsOneStorage(t *testing.T) {
	g, _, d, _ := twoTargets(t)
	ctx := context.Background()
	run1, err := g.Start(ctx, "db1", TriggerManual, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Start(ctx, "db1", TriggerScheduled, "system"); !errors.Is(err, ErrGuestBackupBusy) {
		t.Fatalf("second job of db1: %v", err)
	}
	run2, err := g.Start(ctx, "web1", TriggerScheduled, "system")
	if err != nil {
		t.Fatalf("job of web1 while db1 runs: %v", err)
	}
	if err := run1(ctx); err != nil {
		t.Fatal(err)
	}
	if err := run2(ctx); err != nil {
		t.Fatal(err)
	}
	// web1's guest gets a copy of db1's marker; keep=1 on both.
	d.vols["9002"] = append(d.vols["9002"], Volume{Volid: "plumb-backup:backup/vzdump-9002-copy", Time: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC), Notes: d.notes[0]})
	for range 2 {
		for _, n := range []string{"db1", "web1"} {
			if err := g.BackUp(ctx, n, TriggerScheduled, "system"); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, v := range d.deleted {
		if strings.Contains(v, "-copy") {
			t.Fatalf("deleted the copy %s", v)
		}
	}
	for guest, want := range map[string]int{"9001": 2, "9002": 2} { // db1: 1 scheduled + 1 manual; web1: 1 scheduled + the copy
		if len(d.vols[guest]) != want {
			t.Fatalf("guest %s has %d volumes, want %d: %v", guest, len(d.vols[guest]), want, d.vols[guest])
		}
	}
	if n := kinds(t, g, "web1"); n["unmarked/unknown"] != 1 || n["plumb/scheduled"] != 1 {
		t.Fatalf("web1 kinds %v", n)
	}
}

// VMID reuse by another Target (D-397): db1 is deleted (its rows go with
// the binding), web2 is bound to db1's VMID. db1's volumes are not web2's:
// web2's retention never deletes them, and web2 cannot delete them by id.
func TestQAVMIDReusedByAnotherTarget(t *testing.T) {
	g, st, d, ts := twoTargets(t)
	ctx := context.Background()
	for range 2 {
		g.BackUp(ctx, "db1", TriggerScheduled, "system")
	}
	old := slices.Clone(d.vols["9001"])
	if err := st.DeleteBinding(ctx, "Target", "db1"); err != nil {
		t.Fatal(err)
	}
	delete(ts, "db1")
	if err := st.PutBinding(ctx, store.Binding{Kind: "Target", Name: "web2", Ref: "9001"}); err != nil {
		t.Fatal(err)
	}
	b, _ := st.GetBinding(ctx, "Target", "web2")
	ts["web2"] = &GuestTarget{Name: "web2", Guest: "9001", Gen: b.Gen, Backup: &GuestSchedule{Schedule: "daily", Hour: 3, Keep: 1}}
	before := len(d.deleted)
	for range 3 {
		if err := g.BackUp(ctx, "web2", TriggerScheduled, "system"); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range d.deleted[before:] {
		for _, o := range old {
			if v == o.Volid {
				t.Fatalf("web2's retention deleted db1's volume %s", v)
			}
		}
	}
	if n := kinds(t, g, "web2"); n["unmarked/unknown"] != len(old) || n["plumb/scheduled"] != 1 {
		t.Fatalf("web2 kinds %v", n)
	}
	if err := g.Delete(ctx, "web2", VolumeID(old[0].Volid), "user:admin"); !errors.Is(err, ErrNotPlumbBackup) {
		t.Fatalf("web2 deletes db1's volume: %v", err)
	}
	// A new db1 on another VMID sees none of them (its guest lists its own).
	if err := st.PutBinding(ctx, store.Binding{Kind: "Target", Name: "db1", Ref: "9003"}); err != nil {
		t.Fatal(err)
	}
	nb, _ := st.GetBinding(ctx, "Target", "db1")
	ts["db1"] = &GuestTarget{Name: "db1", Guest: "9003", Gen: nb.Gen}
	if n := kinds(t, g, "db1"); len(n) != 0 {
		t.Fatalf("new db1 kinds %v", n)
	}
}
