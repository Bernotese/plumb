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
