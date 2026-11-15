package store

import (
	"context"
	"testing"
)

// 0029 rebuilds guest_backup_run for the trigger pre-restore (T-232,
// D-420): old rows stay, the new trigger is accepted, the other checks and
// the one-running index stay.
func TestMigrationGuestBackupPreRestore(t *testing.T) {
	v := migrationNumber(t, "_guest_backup_pre_restore.sql")
	path := openAt(t, v-1,
		`INSERT INTO guest_backup_run (id, target, lineage, trigger, started_at, finished_at, result, volid, size, ctime)
		 VALUES (7, 'db1', '0123456789abcdef', 'manual', '2026-11-15T03:00:00Z', '2026-11-15T03:01:00Z', 'done', 'bk:v7', 42, 1700000000)`)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	rec, err := s.RecordedGuestBackups(ctx, "db1")
	if err != nil || rec["bk:v7"].Trigger != "manual" || rec["bk:v7"].Size != 42 {
		t.Fatalf("old row: %+v %v", rec, err)
	}
	id, err := s.StartGuestBackupRun(ctx, "db1", "0123456789abcdef", "pre-restore")
	if err != nil {
		t.Fatalf("pre-restore: %v", err)
	}
	if _, err := s.StartGuestBackupRun(ctx, "db1", "0123456789abcdef", "manual"); err == nil {
		t.Error("a second running job of the Target")
	}
	if err := s.FinishGuestBackupRun(ctx, id, "done", "bk:r1", 1, 2, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO guest_backup_run (target, lineage, trigger, started_at, result) VALUES ('db1', 'x', 'other', 'now', 'done')`); err == nil {
		t.Error("an unknown trigger was accepted")
	}
}
