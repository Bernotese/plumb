package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
)

// stockObjects returns the base templates and import files of Image name.
func (e *env) stockObjects(name string) (bases, imports []driver.ImageObject) {
	e.t.Helper()
	objs, err := e.drv.ListImages(e.ctx, name)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, o := range objs {
		switch o.Kind {
		case driver.ImageBaseTemplate:
			bases = append(bases, o)
		case driver.ImageImportFile:
			imports = append(imports, o)
		}
	}
	return bases, imports
}

// T-242 (D-430): deleting a stock Image removes the base template that
// Plumb made for it; the import file stays and the Plan and the step name
// it with its pvesm free line. A token-free admin approves; the delete is
// not human-only (a template is a cache, D-014).
func TestStockImageDeleteRemovesBaseTemplate(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	bases, imports := e.stockObjects("ubuntu")
	if len(bases) != 1 || len(imports) != 1 {
		t.Fatalf("before: bases %+v imports %+v", bases, imports)
	}
	// A stock Image that stays gets no image part, also when its Target changes.
	if p, body := e.plan(adm, map[string]any{"reason": "cores", "put": []any{targetWith("web1", "ubuntu", 4)}}, 201); len(p.Images) != 0 {
		t.Fatalf("a kept stock Image is planned: %s", body)
	}
	e.runPlan(adm, map[string]any{"reason": "target", "delete": []map[string]string{{"kind": "Target", "name": "web1"}}})

	p, body := e.plan(adm, map[string]any{"reason": "image", "delete": []map[string]string{{"kind": "Image", "name": "ubuntu"}}}, 201)
	if len(p.HumanOnly) != 0 || len(p.Images) != 1 || p.Images[0].Build != nil || len(p.Images[0].Removes) != 1 ||
		p.Images[0].Removes[0].Kind != "base" || p.Images[0].Removes[0].Name != bases[0].Name {
		t.Fatalf("delete plan: %s", body)
	}
	free := "pvesm free " + imports[0].Name
	if !strings.Contains(strings.Join(p.Warnings, "\n"), "kept "+imports[0].Name) || !strings.Contains(strings.Join(p.Warnings, "\n"), free) {
		t.Fatalf("warnings %q, want the kept import file and %q", p.Warnings, free)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	if run, _ := e.st.RunOfPlan(e.ctx, p.ID); run.Status != "succeeded" {
		t.Fatalf("run %s", run.Status)
	}
	s := e.lastSteps(p.ID)["image:retention"]
	if s.Status != "applied" || !strings.Contains(s.Output, "removed "+bases[0].Name) || !strings.Contains(s.Output, free) {
		t.Fatalf("retention step = %+v", s)
	}
	if b, i := e.stockObjects("ubuntu"); len(b) != 0 || len(i) != 1 {
		t.Fatalf("after: bases %+v imports %+v", b, i)
	}
}

// One Plan deletes the Target and its stock Image: Targets are full
// clones, so the template goes in the same Run (T-225 found it left).
func TestStockImageDeleteWithTarget(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.runPlan(adm, map[string]any{"reason": "remove", "delete": []map[string]string{
		{"kind": "Target", "name": "web1"}, {"kind": "Image", "name": "ubuntu"}}})
	if b, i := e.stockObjects("ubuntu"); len(b) != 0 || len(i) != 1 {
		t.Fatalf("after: bases %+v imports %+v", b, i)
	}
}

// The D-209 guard: a template with a linked clone stays and is named. A
// remove that Proxmox refuses stays too. Neither fails the delete: the
// document is gone and no later retention runs, so the step is a warning
// that tells a person what to remove.
func TestStockImageDeleteLeavesTemplateWarning(t *testing.T) {
	for _, c := range []struct {
		name string
		prep func(e *env, base driver.ImageObject)
		want string
	}{
		{"linked clone", func(e *env, b driver.ImageObject) { e.drv.LinkClone(b.Ref) }, "has 1 linked clones"},
		{"refused", func(e *env, _ driver.ImageObject) {
			e.drv.Fail(fake.OpRemoveImage, errors.New("proxmox: 500 can't lock file"))
		}, "could not remove plumb-img-ubuntu-"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, true)
			adm := e.admin()
			e.seed(adm, "web1")
			e.runPlan(adm, map[string]any{"reason": "target", "delete": []map[string]string{{"kind": "Target", "name": "web1"}}})
			bases, imports := e.stockObjects("ubuntu")
			c.prep(e, bases[0])
			p, _ := e.plan(adm, map[string]any{"reason": "image", "delete": []map[string]string{{"kind": "Image", "name": "ubuntu"}}}, 201)
			if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
				t.Fatalf("approve: %d %s", code, body)
			}
			e.eng.Wait()
			if run, _ := e.st.RunOfPlan(e.ctx, p.ID); run.Status != "succeeded" {
				t.Fatalf("run %s: the delete must not fail", run.Status)
			}
			s := e.lastSteps(p.ID)["image:retention"]
			if s.Status != "warning" || !strings.Contains(s.Output, c.want) ||
				!strings.Contains(s.Output, "Image ubuntu is deleted, so Plumb does not try again: remove these templates in Proxmox when nothing uses them: "+bases[0].Name+" (VMID "+string(bases[0].Ref)+").") ||
				strings.Contains(s.Output, "…") ||
				!strings.Contains(s.Output, "it may be the last copy of this base image") ||
				!strings.Contains(s.Output, "pvesm free "+imports[0].Name) {
				t.Fatalf("retention step = %+v", s)
			}
			if b, _ := e.stockObjects("ubuntu"); len(b) != 1 {
				t.Fatalf("the template must stay: %+v", b)
			}
		})
	}
}
