package backup

// T-232 (D-420): the safety backup before a restore, and S3 of T-225.

import (
	"context"
	"errors"
	"testing"
)

func TestSafetyBackupMarkerAndRetention(t *testing.T) {
	m := Marker{Install: tInstall, Target: "db1", Lineage: tLineage, PreRestore: true}
	s, err := m.String()
	if err != nil || s != "plumb:"+tInstall+":db1:"+tLineage+":r" {
		t.Fatalf("marker %q %v", s, err)
	}
	if back, ok := ParseMarker(s); !ok || back.Trigger() != TriggerPreRestore {
		t.Fatalf("parse %+v %v", back, ok)
	}
	if _, err := (Marker{Install: tInstall, Target: "db1", Lineage: tLineage, Manual: true, PreRestore: true}).String(); err == nil {
		t.Error("a marker that is manual and pre-restore")
	}
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.t.Backup.Keep = 1
	var safeties []Classified
	for range 3 {
		c, err := e.g.SafetyBackup(ctx, "db1", "system")
		if err != nil || c.Kind != KindPlumb || c.Trigger != TriggerPreRestore {
			t.Fatalf("safety backup %+v %v", c, err)
		}
		safeties = append(safeties, c)
	}
	for range 4 { // scheduled and manual jobs: their retention never takes a safety backup
		e.g.BackUp(ctx, "db1", TriggerScheduled, "system")
		e.g.BackUp(ctx, "db1", TriggerManual, "user:admin")
	}
	if n := kinds(t, e.g, "db1"); n["plumb/pre-restore"] != 3 || n["plumb/scheduled"] != 1 || n["plumb/manual"] != MaxManualKept {
		t.Fatalf("kinds %v", n)
	}
	if got, _ := e.g.KeepDeletes(ctx, "db1", 1); got != 1 {
		t.Errorf("KeepDeletes counts safety backups: %d", got)
	}
	// After a restore that worked, only the newest safety backup stays.
	if err := e.g.DropSafetyBackups(ctx, "db1", safeties[2].Volid, "system"); err != nil {
		t.Fatal(err)
	}
	if n := kinds(t, e.g, "db1"); n["plumb/pre-restore"] != 1 {
		t.Fatalf("after the drop %v", n)
	}
	l, _ := e.g.List(ctx, "db1")
	for _, it := range l.Items {
		if it.Trigger == TriggerPreRestore && it.ID != safeties[2].ID {
			t.Errorf("an older safety backup stays: %s", it.ID)
		}
	}
}

// A safety backup that gives no marked volume is an error with Plumb's
// text; a refused job too (restored mode, free space).
func TestSafetyBackupFails(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.d.noNotes = true
	if _, err := e.g.SafetyBackup(ctx, "db1", "system"); !errors.Is(err, ErrSafetyBackup) {
		t.Errorf("no notes: %v", err)
	}
	e.d.noNotes = false
	e.d.free = 5
	if _, err := e.g.SafetyBackup(ctx, "db1", "system"); !errors.Is(err, ErrGuestBackupRefused) {
		t.Errorf("free space: %v", err)
	}
}

// goneDriver lists nothing: the guest left the pool.
type goneDriver struct{ *memDriver }

func (goneDriver) ListGuestBackups(context.Context, string, string) ([]Volume, error) {
	return nil, ErrGuestGone
}

// S3 of T-225: a Target whose guest is gone has an empty list (no error);
// a delete is "not found".
func TestGuestGoneEmptyList(t *testing.T) {
	e := newJobEnv(t, nil)
	ctx := context.Background()
	e.g.BackUp(ctx, "db1", TriggerScheduled, "system")
	e.g.cfg.Driver = goneDriver{e.d}
	l, err := e.g.List(ctx, "db1")
	if err != nil || len(l.Items) != 0 || l.Storage == nil {
		t.Fatalf("list %+v %v", l, err)
	}
	if err := e.g.Delete(ctx, "db1", "0123456789abcdef", "user:admin"); !errors.Is(err, ErrGuestBackupNotFound) {
		t.Errorf("delete: %v", err)
	}
}
