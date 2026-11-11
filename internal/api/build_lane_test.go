package api

// T-062 part B: the build lane with the fake driver and the fake agent
// (linktest, simulated seal). Review items B1, S4, S5, n2 of T-039.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/link/linktest"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// noBuildLeft checks that no build guest, no build agent and no building
// version is left.
func (e *env) noBuildLeft(image string) {
	e.t.Helper()
	b, err := e.drv.ListBuilds(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, g := range b {
		if !g.Template {
			e.t.Errorf("build guest left: %+v", g)
		}
	}
	if _, err := e.st.AgentByTarget(e.ctx, wire.BuildLinkPrefix+image); !errors.Is(err, store.ErrNotFound) {
		e.t.Errorf("the build agent of %s can still log in: %v", image, err)
	}
	if v, _ := e.st.BuildingImageVersions(e.ctx); len(v) != 0 {
		e.t.Errorf("building versions left: %+v", v)
	}
}

func (e *env) versionTemplates(image string) []int {
	e.t.Helper()
	objs, err := e.drv.ListImages(e.ctx, image)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []int
	for _, o := range objs {
		if o.Kind == driver.ImageVersionTemplate {
			out = append(out, o.Version)
		}
	}
	return out
}

func (e *env) lastSteps(planID int64) map[string]store.Step {
	e.t.Helper()
	run, err := e.st.RunOfPlan(e.ctx, planID)
	if err != nil {
		e.t.Fatal(err)
	}
	steps, _ := e.st.Steps(e.ctx, run.ID)
	out := map[string]store.Step{}
	for _, s := range steps {
		out[s.Resource] = s
	}
	return out
}

func TestBuildThenTargetFromVersion(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
	if v, err := e.st.GetImageVersion(e.ctx, "web", 1); err != nil || v.Status != store.ImageReady {
		t.Fatalf("v1 = %+v %v", v, err)
	}
	e.noBuildLeft("web")
	e.agent("web1")
	target := doc(state.KindTarget, "web1", `{"type":"vm","image":"web","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"}}`)
	e.runPlan(adm, map[string]any{"reason": "target", "put": []state.Doc{target}})
	b, err := e.st.GetBinding(e.ctx, state.KindTarget, "web1")
	if err != nil {
		t.Fatal(err)
	}
	st, err := e.drv.Observe(e.ctx, driver.GuestID(b.Ref))
	if err != nil || st.Spec.ImageVersion != 1 || st.Spec.ImageName != "web" {
		t.Fatalf("guest of web1 = %+v %v", st.Spec, err)
	}
	code, body := adm.do("GET", "/api/v1/targets/web1", nil)
	if code != 200 || !strings.Contains(string(body), `"image_version":1`) {
		t.Fatalf("target: %d %s", code, body)
	}
	code, body = adm.do("GET", "/api/v1/images/web", nil)
	if code != 200 || !strings.Contains(string(body), `"targets":["web1"]`) {
		t.Fatalf("image: %d %s", code, body)
	}
	// A new version changes no Target; a new Target takes the newest.
	e.manualBuild(adm, "web", 2, nil)
	if st, _ := e.drv.Observe(e.ctx, driver.GuestID(b.Ref)); st.Spec.ImageVersion != 1 {
		t.Fatalf("web1 moved to %d", st.Spec.ImageVersion)
	}
	e.agent("web2")
	t2 := doc(state.KindTarget, "web2", `{"type":"vm","image":"web","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"}}`)
	e.runPlan(adm, map[string]any{"reason": "target 2", "put": []state.Doc{t2}})
	b2, _ := e.st.GetBinding(e.ctx, state.KindTarget, "web2")
	if st, _ := e.drv.Observe(e.ctx, driver.GuestID(b2.Ref)); st.Spec.ImageVersion != 2 {
		t.Fatalf("web2 from version %d, want 2", st.Spec.ImageVersion)
	}
}

func TestBuildFailures(t *testing.T) {
	failed := store.ImageFailed
	cases := map[string]struct {
		tune func(*linktest.Agent)
		step string // the step that fails
		text string
	}{
		"seal step fails":   {func(a *linktest.Agent) { a.FailSeal(wire.SealStepLogs) }, "image:seal", "seal:logs: failed"},
		"seal refused":      {func(a *linktest.Agent) { a.NoBuildMark(true) }, "image:seal", "seal:guard: refused"},
		"no power off (B1)": {func(a *linktest.Agent) { a.SealStuck(true) }, "image:seal", "did not power itself off"},
		"apply fails":       {func(a *linktest.Agent) { a.FailApply("file:/etc/motd", "disk full") }, "file:/etc/motd", "disk full"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, false, withBuilds)
			e.buildAgents(c.tune)
			adm := e.admin()
			p, _ := e.plan(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")}, 201)
			if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
				t.Fatalf("approve: %d %s", code, body)
			}
			e.eng.Wait()
			v, err := e.st.GetImageVersion(e.ctx, "web", 1)
			if err != nil || v.Status != failed {
				t.Fatalf("v1 = %+v %v", v, err)
			}
			steps := e.lastSteps(p.ID)
			if s := steps[c.step]; s.Status != "failed" || !strings.Contains(s.Output, c.text) {
				t.Errorf("step %s = %+v", c.step, s)
			}
			if s := steps["image:template"]; s.Status != "failed" || !strings.Contains(s.Output, "nothing was converted") {
				t.Errorf("template step = %+v", s)
			}
			if got := e.versionTemplates("web"); len(got) != 0 {
				t.Errorf("a failed build made templates %v", got)
			}
			e.noBuildLeft("web")
		})
	}
}

// S5: the build does not start when the disk would be too full; nothing
// is made.
func TestBuildDiskGate(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.drv.SetDiskSpace(20*(1<<30), 10*(1<<30), false) // 10 GiB used of 20; a 10 GiB build is 100 %
	p, body := e.plan(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")}, 201)
	if !strings.Contains(body, "would not start now") || !strings.Contains(body, "thin-pool metadata use is unknown") {
		t.Errorf("plan warnings: %s", body)
	}
	adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil)
	e.eng.Wait()
	steps := e.lastSteps(p.ID)
	if s := steps["guest:config"]; s.Status != "failed" || !strings.Contains(s.Output, "nothing was made") {
		t.Fatalf("guest step = %+v", s)
	}
	if e.eng.Disk().Reserved() != 0 {
		t.Errorf("reservation left: %d", e.eng.Disk().Reserved())
	}
	e.noBuildLeft("web")
}

// S4: a crash leaves a building version and a build guest; Recover fails
// the version, revokes the agent and removes the guest.
func TestRecoverBuild(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	adm := e.admin()
	e.buildAgents(func(a *linktest.Agent) { a.FailSeal(wire.SealStepTrim) }) // the setup build fails; it makes the base
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
	objs, _ := e.drv.ListImages(e.ctx, "web")
	var base driver.ImageRef
	for _, o := range objs {
		if o.Kind == driver.ImageBaseTemplate {
			base = o.Ref
		}
	}
	gid, err := e.drv.CreateBuild(e.ctx, driver.BuildSpec{Image: "web", Version: 2, Base: base, Cores: 1, MemoryMB: 1024, DiskGB: 10, Bridge: "vmbr0", IP: "dhcp"},
		driver.Bootstrap{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	head, _ := e.st.Head(e.ctx)
	run, _ := e.st.RunOfPlan(e.ctx, head.PlanID)
	if _, err := e.st.AddImageVersion(e.ctx, store.ImageVersion{Image: "web", Trigger: store.TriggerManual, RunID: run.ID, RevisionID: head.ID,
		RecipeSHA256: builtSHA, BaseSHA256: builtSHA, DiskGB: 10}, func(int) string { return builtSHA }); err != nil {
		t.Fatal(err)
	}
	if err := e.eng.Recover(e.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.drv.Observe(e.ctx, gid); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("build guest after Recover: %v", err)
	}
	if v, _ := e.st.GetImageVersion(e.ctx, "web", 2); v.Status != store.ImageFailed {
		t.Fatalf("v2 = %+v", v)
	}
	e.noBuildLeft("web")
}

// A rest of an earlier build is removed before a new build starts.
func TestBuildRemovesOldBuildGuest(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
	objs, _ := e.drv.ListImages(e.ctx, "web")
	var base driver.ImageRef
	for _, o := range objs {
		if o.Kind == driver.ImageBaseTemplate {
			base = o.Ref
		}
	}
	old, err := e.drv.CreateBuild(e.ctx, driver.BuildSpec{Image: "web", Version: 9, Base: base, Cores: 1, MemoryMB: 1024, DiskGB: 10, Bridge: "vmbr0", IP: "dhcp"},
		driver.Bootstrap{Token: "old"})
	if err != nil {
		t.Fatal(err)
	}
	e.manualBuild(adm, "web", 2, nil)
	if _, err := e.drv.Observe(e.ctx, old); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("old build guest: %v", err)
	}
	e.noBuildLeft("web")
}

// D-209: deleting a built Image removes every template of it; the
// version rows stay as history (removed).
func TestDeleteBuiltImage(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
	p, body := e.plan(adm, map[string]any{"reason": "delete", "delete": []map[string]string{{"kind": "Image", "name": "web"}}}, 201)
	if len(p.HumanOnly) != 1 || len(p.Images) != 1 || p.Images[0].Build != nil || len(p.Images[0].Removes) != 2 {
		t.Fatalf("delete plan: %s", body)
	}
	adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil)
	e.eng.Wait()
	if objs, _ := e.drv.ListImages(e.ctx, "web"); len(objs) != 1 || objs[0].Kind != driver.ImageImportFile {
		t.Fatalf("objects left: %+v", objs)
	}
	if v, _ := e.st.GetImageVersion(e.ctx, "web", 1); v.Status != store.ImageRemoved {
		t.Fatalf("v1 = %+v", v)
	}
	if s := e.lastSteps(p.ID)["image:retention"]; s.Status != "applied" || !strings.Contains(s.Output, "removed plumb-img-web-v1") {
		t.Fatalf("retention step = %+v", s)
	}
}

// The controller guards of D-207: seal only to a build link, no watch to
// a build link.
func TestLinkSealGuards(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	if _, err := e.link.Send("web1", wire.CommandSeal, wire.SealBody{}); err == nil {
		t.Error("seal to a Target was sent")
	}
	if _, err := e.link.Send("build:web", wire.CommandWatch, wire.WatchBody{}); err == nil {
		t.Error("watch to a build guest was sent")
	}
	if _, err := e.link.Send("build:web", wire.CommandSeal, wire.SealBody{PlanID: 1}); err != nil {
		t.Errorf("seal to a build link: %v", err)
	}
}

var _ = json.Marshal

// Review N2 of #133: a power read that fails is tried again; three in a
// row fail the build, and nothing is converted.
func TestBuildObserveRetries(t *testing.T) {
	for _, c := range []struct {
		failures int
		want     string
	}{{2, store.ImageReady}, {3, store.ImageFailed}} {
		e := newEnv(t, false, withBuilds)
		e.buildAgents(nil)
		adm := e.admin()
		for i := 0; i < c.failures; i++ {
			e.drv.Fail(fake.OpObserve, errors.New("proxmox: 596 connection timed out"))
		}
		e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
		if v, _ := e.st.GetImageVersion(e.ctx, "web", 1); v.Status != c.want {
			t.Errorf("%d failed reads: v1 %s, want %s", c.failures, v.Status, c.want)
		}
		e.noBuildLeft("web")
	}
}

// Review N1 of #133: a build guest that was converted but never named as a
// version is shown in the Image status, survives a new build and is never
// used.
func TestLeftTemplateShown(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
	objs, _ := e.drv.ListImages(e.ctx, "web")
	var base driver.ImageRef
	for _, o := range objs {
		if o.Kind == driver.ImageBaseTemplate {
			base = o.Ref
		}
	}
	rest, err := e.drv.CreateBuild(e.ctx, driver.BuildSpec{Image: "web", Version: 9, Base: base, Cores: 1, MemoryMB: 1024, DiskGB: 10, Bridge: "vmbr0", IP: "dhcp"},
		driver.Bootstrap{Token: "rest"})
	if err != nil {
		t.Fatal(err)
	}
	e.drv.LeftTemplate(rest)
	want := `"left_templates":[{"name":"plumb-build-web-v9","guest_id":"` + string(rest) + `"}]`
	if code, body := adm.do("GET", "/api/v1/images/web", nil); code != 200 || !strings.Contains(string(body), want) {
		t.Fatalf("image status: %d %s", code, body)
	}
	e.manualBuild(adm, "web", 2, nil)
	if _, err := e.drv.Observe(e.ctx, rest); err != nil {
		t.Fatalf("the rest was touched: %v", err)
	}
}
