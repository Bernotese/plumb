package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestImageVersions(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	p, _ := s.CreatePlan(ctx, Plan{Origin: "proposal", Risk: "reversible", Reason: "r", Author: "user:a"})
	rev, run, err := s.ApprovePlan(ctx, p.ID, "user:a", NewRevision{Author: "user:a", Docs: []DocRow{row("Block", "a", `{"kind":"Block","name":"a","spec":{}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	hex := strings.Repeat("a", 64)
	digest := func(n int) string { return fmt.Sprintf("%064d", n) }
	add := func(image string) (ImageVersion, error) {
		return s.AddImageVersion(ctx, ImageVersion{Image: image, Trigger: TriggerManual, RunID: run.ID, RevisionID: rev.ID,
			RecipeSHA256: hex, BaseSHA256: hex, DiskGB: 10}, digest)
	}

	v1, err := add("web")
	if err != nil || v1.Version != 1 || v1.Status != ImageBuilding || v1.Digest != digest(1) || v1.StartedAt.IsZero() {
		t.Fatalf("v1 = %+v, %v", v1, err)
	}
	// One build per Image at a time; another Image may build.
	if _, err := add("web"); !errors.Is(err, ErrBuildRunning) {
		t.Fatalf("second build: %v, want ErrBuildRunning", err)
	}
	if d1, err := add("db"); err != nil || d1.Version != 1 {
		t.Fatalf("other image: %+v %v", d1, err)
	}
	// Status rules.
	if err := s.RemoveImageVersion(ctx, "web", 1); !errors.Is(err, ErrImageVersionState) {
		t.Errorf("remove while building: %v", err)
	}
	if err := s.FinishImageVersion(ctx, "web", 1, ImageRemoved); !errors.Is(err, ErrImageVersionState) {
		t.Errorf("finish as removed: %v", err)
	}
	if err := s.FinishImageVersion(ctx, "web", 1, ImageFailed); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishImageVersion(ctx, "web", 1, ImageReady); !errors.Is(err, ErrImageVersionState) {
		t.Errorf("finish twice: %v", err)
	}
	if err := s.FinishImageVersion(ctx, "web", 9, ImageReady); !errors.Is(err, ErrNotFound) {
		t.Errorf("finish a missing version: %v", err)
	}
	v2, _ := add("web")
	if v2.Version != 2 {
		t.Fatalf("v2 = %+v", v2)
	}
	_ = s.FinishImageVersion(ctx, "web", 2, ImageReady)
	// Numbers are never used again: all versions removed (the Image
	// document deleted) and the next build still counts up.
	for _, n := range []int{1, 2} {
		if err := s.RemoveImageVersion(ctx, "web", n); err != nil {
			t.Fatalf("remove v%d: %v", n, err)
		}
	}
	if v3, _ := add("web"); v3.Version != 3 {
		t.Fatalf("after removing all versions: %+v, want version 3", v3)
	}
	list, err := s.ImageVersions(ctx, "web")
	if err != nil || len(list) != 3 || list[0].Version != 3 || list[2].Status != ImageRemoved || list[2].RemovedAt.IsZero() || list[1].FinishedAt.IsZero() {
		t.Fatalf("versions = %+v, %v", list, err)
	}
	building, _ := s.BuildingImageVersions(ctx)
	if len(building) != 2 || building[0].Image != "db" || building[1].Image != "web" {
		t.Errorf("building = %+v", building)
	}
	// The database keeps the history even against a direct write.
	for _, q := range []string{
		`DELETE FROM image_version WHERE image = 'web'`,
		`UPDATE image_version SET version = 7 WHERE image = 'web' AND version = 3`,
		`UPDATE image_version SET status = 'building', finished_at = NULL, removed_at = NULL WHERE image = 'web' AND version = 1`,
		`INSERT INTO image_version (image, version, status, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb, started_at)
		 VALUES ('db', 5, 'building', 'manual', 1, 1, '` + hex + `', '` + hex + `', '` + hex + `', 10, 'x')`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err == nil {
			t.Errorf("accepted: %s", q)
		}
	}
}

func TestImageScheduleSlot(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if got, err := s.ImageScheduleSlot(ctx, "web"); err != nil || !got.IsZero() {
		t.Fatalf("empty: %v %v", got, err)
	}
	slot := time.Date(2026, 11, 15, 3, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{slot.Add(-7 * 24 * time.Hour), slot} {
		if err := s.SetImageScheduleSlot(ctx, "web", at); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.ImageScheduleSlot(ctx, "web"); !got.Equal(slot) {
		t.Errorf("slot = %v, want %v", got, slot)
	}
}

// A database from before the image migration, with a Plan, a revision and
// a Run, migrates: the rows and their references stay, and the new origin
// build works.
func TestMigrateImageVersion(t *testing.T) {
	n := migrationNumber(t, "_image_version.sql")
	path := openAt(t, n-1,
		`INSERT INTO plan (id, origin, proposal, changes, risk, status, reason, author, created_at, human_only)
		 VALUES (1, 'proposal', '{}', '[]', 'reversible', 'approved', 'r', 'user:a', '2026-11-01T00:00:00Z', '["rule-change"]')`,
		`INSERT INTO revision (id, plan_id, author, reason, created_at) VALUES (1, 1, 'user:a', 'r', '2026-11-01T00:00:00Z')`,
		`INSERT INTO run (id, plan_id, revision_id, status, started_at) VALUES (1, 1, 1, 'succeeded', '2026-11-01T00:00:00Z')`,
	)
	ctx := context.Background()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old, err := s.GetPlan(ctx, 1)
	if err != nil || old.Origin != "proposal" || len(old.HumanOnly) != 1 || old.HumanOnly[0] != "rule-change" {
		t.Fatalf("old plan = %+v, %v", old, err)
	}
	if r, err := s.GetRun(ctx, 1); err != nil || r.PlanID != 1 {
		t.Fatalf("run = %+v, %v", r, err)
	}
	p, err := s.CreatePlan(ctx, Plan{Origin: "build", Risk: "reversible", Reason: "build now", Author: "system", HumanOnly: []string{"image-recipe"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetPlan(ctx, p.ID); err != nil || got.Origin != "build" {
		t.Fatalf("build plan = %+v, %v", got, err)
	}
	if _, err := s.CreatePlan(ctx, Plan{Origin: "nope", Risk: "reversible", Author: "user:a"}); err == nil {
		t.Error("unknown origin accepted")
	}
}
