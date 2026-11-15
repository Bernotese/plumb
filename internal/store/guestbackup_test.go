package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func gbStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "gb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, ctx
}

// D-417: the lineage follows the binding generation; the rows go with the
// binding.
func TestBackupLineage(t *testing.T) {
	s, ctx := gbStore(t)
	if err := s.PutBinding(ctx, Binding{Kind: "Target", Name: "db1", Ref: "9100"}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.GetBinding(ctx, "Target", "db1")
	l1, err := s.BackupLineage(ctx, "db1", b.Gen)
	if err != nil || len(l1) != 16 {
		t.Fatalf("%q %v", l1, err)
	}
	if again, _ := s.BackupLineage(ctx, "db1", b.Gen); again != l1 {
		t.Fatal("the same generation got a new lineage")
	}
	// A new guest of the Target (re-create, same VMID): new generation,
	// new lineage.
	if err := s.PutBinding(ctx, Binding{Kind: "Target", Name: "db1", Ref: "9101"}); err != nil {
		t.Fatal(err)
	}
	s.PutBinding(ctx, Binding{Kind: "Target", Name: "db1", Ref: "9100"})
	b2, _ := s.GetBinding(ctx, "Target", "db1")
	l2, _ := s.BackupLineage(ctx, "db1", b2.Gen)
	if l2 == l1 || b2.Gen == b.Gen {
		t.Fatalf("re-created guest kept the lineage: %s %s", l1, l2)
	}
	id, err := s.StartGuestBackupRun(ctx, "db1", l2, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartGuestBackupRun(ctx, "db1", l2, "scheduled"); !errors.Is(err, ErrGuestBackupRunning) {
		t.Fatalf("second running job: %v", err)
	}
	if err := s.FinishGuestBackupRun(ctx, id, "done", "pb:backup/x", 42, 1700000000, "ok"); err != nil {
		t.Fatal(err)
	}
	if r, err := s.GuestBackupRunOfVolume(ctx, "db1", "pb:backup/x"); err != nil || r.Trigger != "manual" || r.Result != "done" {
		t.Fatalf("%+v %v", r, err)
	}
	// Delete: lineage and runs go; a new Target of the name starts anew.
	if err := s.DeleteBinding(ctx, "Target", "db1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LastGuestBackupRun(ctx, "db1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("runs after delete: %v", err)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM guest_backup_lineage`).Scan(&n)
	if n != 0 {
		t.Fatal("lineage after delete")
	}
	if err := s.RefusedGuestBackupRun(ctx, "x", "0123456789abcdef", "scheduled", string(make([]byte, 2000))); err != nil {
		t.Fatalf("a long message is clipped, not refused: %v", err)
	}
}

// T-248 (M2 of #368): only a finished job without a volume and with its
// task to be checked can be settled, once. A running job, a job with a
// volume and a settled job never change.
func TestSettleGuestBackupTaskGuard(t *testing.T) {
	s, ctx := gbStore(t)
	const lin = "0123456789abcdef"
	id, err := s.StartGuestBackupRun(ctx, "db1", lin, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGuestBackupTask(ctx, id, "UPID:pve:1"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SettleGuestBackupTask(ctx, id, "pb:backup/a", 1, 1, "x"); ok || err != nil {
		t.Fatalf("a running job was settled: %v %v", ok, err)
	}
	if err := s.FinishGuestBackupRun(ctx, id, "failed", "", 0, 0, "overlap"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.UncheckedGuestBackupTasks(ctx, "db1"); len(rows) != 1 || rows[0].UPID != "UPID:pve:1" {
		t.Fatalf("unchecked %+v", rows)
	}
	if ok, err := s.SettleGuestBackupTask(ctx, id, "pb:backup/a", 7, 9, "proven"); !ok || err != nil {
		t.Fatalf("first settle: %v %v", ok, err)
	}
	if ok, _ := s.SettleGuestBackupTask(ctx, id, "pb:backup/b", 1, 1, "again"); ok {
		t.Fatal("a settled job was settled again")
	}
	if ok, _ := s.SettleGuestBackupTask(ctx, id, "", 0, 0, "again"); ok {
		t.Fatal("a settled job got a new message")
	}
	if r, _ := s.GuestBackupRun(ctx, id); r.Result != "done" || r.Volid != "pb:backup/a" || r.Message != "proven" {
		t.Fatalf("row %+v", r)
	}
	if rec, _ := s.RecordedGuestBackups(ctx, "db1"); rec["pb:backup/a"].Size != 7 || rec["pb:backup/a"].CTime != 9 {
		t.Fatalf("recorded %+v", rec)
	}
	// A job that recorded its volume has nothing to check, and a job with a
	// volume is never claimed again even if the flag were set.
	id2, _ := s.StartGuestBackupRun(ctx, "db1", lin, "manual")
	s.SetGuestBackupTask(ctx, id2, "UPID:pve:2")
	s.FinishGuestBackupRun(ctx, id2, "done", "pb:backup/c", 1, 1, "")
	if rows, _ := s.UncheckedGuestBackupTasks(ctx, "db1"); len(rows) != 0 {
		t.Fatalf("a job with a volume is to be checked: %+v", rows)
	}
	s.db.ExecContext(ctx, `UPDATE guest_backup_run SET check_task = 1 WHERE id = ?`, id2)
	if ok, _ := s.SettleGuestBackupTask(ctx, id2, "pb:backup/d", 1, 1, "x"); ok {
		t.Fatal("a job with a volume got another one")
	}
	// Recover keeps the task of a cut-off job to be checked.
	id3, _ := s.StartGuestBackupRun(ctx, "db1", lin, "manual")
	s.SetGuestBackupTask(ctx, id3, "UPID:pve:3")
	if err := s.EndRunningGuestBackups(ctx, "stopped"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.UncheckedGuestBackupTasks(ctx, ""); len(rows) != 1 || rows[0].ID != id3 {
		t.Fatalf("after Recover %+v", rows)
	}
}
