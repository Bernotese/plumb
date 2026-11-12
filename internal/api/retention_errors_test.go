package api

// D-232: retention after a good build. A template in use is left on
// purpose (step applied, Run succeeded); every other ListImages or
// RemoveImage error fails the step and the Run, and the new version stays
// ready.

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/store"
)

// retentionOf returns the Run status and the retention step of the build
// of a version.
func (e *env) retentionOf(image string, version int) (string, store.Step) {
	e.t.Helper()
	v, err := e.st.GetImageVersion(e.ctx, image, version)
	if err != nil {
		e.t.Fatal(err)
	}
	run, err := e.st.GetRun(e.ctx, v.RunID)
	if err != nil {
		e.t.Fatal(err)
	}
	steps, err := e.st.Steps(e.ctx, v.RunID)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, s := range steps {
		if s.Resource == "image:retention" {
			return run.Status, s
		}
	}
	e.t.Fatalf("v%d: no retention step", version)
	return "", store.Step{}
}

// twoVersions builds v1 and v2 of web (keep 2).
func twoVersions(t *testing.T) (*env, *client) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
	e.manualBuild(adm, "web", 2, nil)
	return e, adm
}

const readySentence = "; v3 is ready and usable; old templates stay until the next build"

// The remove error class: TestRetentionRemoveErrorFailsRunAndRetriesQA.

func TestRetentionListErrorFailsRunVersionReady(t *testing.T) {
	e, adm := twoVersions(t)
	// The template is made; the next list is the one of retention.
	e.drv.Hook(fake.OpMakeImage, func() { e.drv.Fail(fake.OpListImages, errors.New("pool list unavailable")) })
	e.manualBuild(adm, "web", 3, nil) // v3 is ready
	status, s := e.retentionOf("web", 3)
	if status != "failed" || s.Status != "failed" || s.Output != "pool list unavailable"+readySentence {
		t.Fatalf("run %s, retention %+v", status, s)
	}
	if got := e.versionTemplates("web"); len(got) != 3 {
		t.Fatalf("templates: %v", got)
	}
}

// A template with a linked clone is in use: retention leaves it on
// purpose, and the Run succeeds.
func TestRetentionInUseLeavesTemplateRunSucceeds(t *testing.T) {
	e, adm := twoVersions(t)
	v1, err := e.st.GetImageVersion(e.ctx, "web", 1)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := e.drv.FindImage(e.ctx, "web", 1, v1.Digest)
	if err != nil {
		t.Fatal(err)
	}
	e.drv.LinkClone(ref)
	e.manualBuild(adm, "web", 3, nil)
	status, s := e.retentionOf("web", 3)
	if status != "succeeded" || s.Status != "applied" ||
		!strings.Contains(s.Output, "left plumb-img-web-v1: fake: template plumb-img-web-v1 has 1 linked clones") ||
		strings.Contains(s.Output, "is ready and usable") {
		t.Fatalf("run %s, retention %+v", status, s)
	}
	if v, _ := e.st.GetImageVersion(e.ctx, "web", 1); v.Status != store.ImageReady {
		t.Fatalf("v1 = %s", v.Status)
	}
	// The clone goes; the next build removes v1 (and v2, keep 2).
	e.drv.UnlinkClone(ref)
	e.manualBuild(adm, "web", 4, nil)
	if status, s := e.retentionOf("web", 4); status != "succeeded" || !strings.Contains(s.Output, "removed plumb-img-web-v1") {
		t.Fatalf("v4: run %s, retention %+v", status, s)
	}
}

// Review of PR #150: the template of v1 is removed, but the store row
// cannot change. The step fails and says that only the row is stale; the
// next build fixes the row.
func TestRetentionStoreRowErrorFailsRunTemplateGone(t *testing.T) {
	e, adm := twoVersions(t)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(e.dir, "plumb.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(e.ctx, `CREATE TRIGGER fail_v1_removed BEFORE UPDATE ON image_version
		WHEN NEW.version = 1 AND NEW.status = 'removed' BEGIN SELECT RAISE(ABORT, 'injected store fault'); END`); err != nil {
		t.Fatal(err)
	}
	e.manualBuild(adm, "web", 3, nil) // v3 is ready
	status, s := e.retentionOf("web", 3)
	if status != "failed" || s.Status != "failed" || !strings.Contains(s.Output, "removed plumb-img-web-v1") ||
		!strings.Contains(s.Output, "version 1: the template is removed, the store row is not: store: image version: ") ||
		!strings.Contains(s.Output, "injected store fault") ||
		!strings.HasSuffix(s.Output, "; v3 is ready and usable; the next build fixes the store row") ||
		strings.Contains(s.Output, "old templates stay") {
		t.Fatalf("run %s, retention %+v", status, s)
	}
	if got := e.versionTemplates("web"); len(got) != 2 {
		t.Fatalf("templates: %v", got)
	}
	if v, _ := e.st.GetImageVersion(e.ctx, "web", 1); v.Status != store.ImageReady {
		t.Fatalf("v1 = %s, want the stale ready row", v.Status)
	}
	// The store works again: the next build fixes the row.
	if _, err := db.ExecContext(e.ctx, `DROP TRIGGER fail_v1_removed`); err != nil {
		t.Fatal(err)
	}
	e.manualBuild(adm, "web", 4, nil)
	if status, s := e.retentionOf("web", 4); status != "succeeded" || s.Status != "applied" {
		t.Fatalf("v4: run %s, retention %+v", status, s)
	}
	if v, _ := e.st.GetImageVersion(e.ctx, "web", 1); v.Status != store.ImageRemoved {
		t.Fatalf("v1 = %s, want removed", v.Status)
	}
}
