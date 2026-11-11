package api

// T-062 part A: Plans of image builds (D-199 … D-201). The lanes run in
// part B; here a lane fails at its first step and changes nothing.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/link/linktest"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// withBuilds makes the build timeouts short for tests.
func withBuilds(c *plan.Config) {
	c.BuildEnrollTimeout, c.SealTimeout, c.PowerPoll = 5*time.Second, 2*time.Second, 10*time.Millisecond
}

var builtSHA = strings.Repeat("a", 64)

// buildAgents starts a fake agent for every build guest that appears, as
// the vendor snippet does in a real clone. The agent seals like the real
// one (linktest) and then powers the guest off. tune sets each new agent
// up; a second call only replaces tune.
func (e *env) buildAgents(tune func(*linktest.Agent)) {
	e.t.Helper()
	e.mu.Lock()
	e.buildTune = tune
	running := e.buildLoop
	e.buildLoop = true
	e.mu.Unlock()
	if running {
		return
	}
	seen := map[driver.GuestID]bool{}
	go func() {
		for e.ctx.Err() == nil {
			builds, _ := e.drv.ListBuilds(e.ctx)
			for _, b := range builds {
				if seen[b.ID] || b.Template {
					continue
				}
				seen[b.ID] = true
				id := b.ID
				a := linktest.New(e.link.Handler(), func() (string, bool) {
					boot, ok := e.drv.Bootstrap(id)
					return boot.Token, ok
				})
				a.OnSeal(func() { e.drv.StopOutside(id) })
				e.mu.Lock()
				tune := e.buildTune
				e.mu.Unlock()
				if tune != nil {
					tune(a)
				}
				go a.Run(e.ctx)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
}

func builtImageDoc(name, motd string) []state.Doc {
	return []state.Doc{
		doc(state.KindBlock, "banner", `{"resources":[{"file":{"path":"/etc/motd","content":"`+motd+`"}}]}`),
		doc(state.KindImage, name, `{"type":"vm","url":"https://images.invalid/u.img","sha256":"`+builtSHA+
			`","build":{"blocks":[{"use":"banner"}],"guest":{"disk":10,"bridge":"vmbr0"}},"retention":{"keep":2}}`),
	}
}

type planOut struct {
	ID        int64    `json:"id"`
	Origin    string   `json:"origin"`
	Status    string   `json:"status"`
	Risk      string   `json:"risk"`
	Revision  *int64   `json:"revision"`
	RunID     *int64   `json:"run_id"`
	HumanOnly []string `json:"human_only"`
	Warnings  []string `json:"warnings"`
	Images    []struct {
		Image string `json:"image"`
		Build *struct {
			Version      int    `json:"version"`
			Trigger      string `json:"trigger"`
			RecipeSHA256 string `json:"recipe_sha256"`
			Resources    []struct {
				Resource   string `json:"resource"`
				Unverified bool   `json:"unverified"`
			} `json:"resources"`
		} `json:"build"`
		Removes []struct {
			Kind    string `json:"kind"`
			Name    string `json:"name"`
			Version *int   `json:"version"`
		} `json:"removes"`
	} `json:"images"`
}

func (e *env) plan(c *client, req map[string]any, want int) (planOut, string) {
	e.t.Helper()
	code, body := c.do("POST", "/api/v1/plans", req)
	if code != want {
		e.t.Fatalf("plan %v: %d %s", req["reason"], code, body)
	}
	var p planOut
	json.Unmarshal(body, &p)
	return p, string(body)
}

func (e *env) getPlan(c *client, id int64) planOut {
	e.t.Helper()
	code, body := c.do("GET", "/api/v1/plans/"+itoa(id), nil)
	if code != 200 {
		e.t.Fatalf("get plan %d: %d %s", id, code, body)
	}
	var p planOut
	json.Unmarshal(body, &p)
	return p
}

// addVersion stores a version row as a finished build would.
func (e *env) addVersion(image string, status string) int {
	e.t.Helper()
	head, err := e.st.Head(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	run, err := e.st.RunOfPlan(e.ctx, head.PlanID)
	if err != nil {
		e.t.Fatal(err)
	}
	v, err := e.st.AddImageVersion(e.ctx, store.ImageVersion{Image: image, Trigger: store.TriggerManual, RunID: run.ID, RevisionID: head.ID,
		RecipeSHA256: builtSHA, BaseSHA256: builtSHA, DiskGB: 10}, func(int) string { return builtSHA })
	if err != nil {
		e.t.Fatal(err)
	}
	if status != store.ImageBuilding {
		if err := e.st.FinishImageVersion(e.ctx, image, v.Version, status); err != nil {
			e.t.Fatal(err)
		}
	}
	return v.Version
}

func TestBuildPlanOfAChange(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	p, _ := e.plan(adm, map[string]any{"reason": "new image", "put": builtImageDoc("web", "hi")}, 201)
	if p.Risk != "reversible" || len(p.HumanOnly) != 1 || p.HumanOnly[0] != plan.HumanOnlyImageRecipe {
		t.Fatalf("plan = %+v", p)
	}
	if len(p.Images) != 1 || p.Images[0].Build == nil || p.Images[0].Build.Trigger != "change" || p.Images[0].Build.Version != 1 ||
		len(p.Images[0].Build.Resources) != 1 || !p.Images[0].Build.Resources[0].Unverified || len(p.Images[0].Build.RecipeSHA256) != 64 {
		t.Fatalf("images = %+v", p.Images)
	}
	// An apply token cannot approve a recipe change (D-201).
	tok := e.token(TierApply, "disruptive")
	if code, body := tok.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 403 || !strings.Contains(string(body), "human-required") {
		t.Fatalf("token approve: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	// The lane ran and shows in the Run with every step.
	run, _ := e.st.RunOfPlan(e.ctx, p.ID)
	code, body := adm.do("GET", "/api/v1/runs/"+itoa(run.ID), nil)
	var rv struct {
		Targets []any `json:"targets"`
		Images  []struct {
			Image   string `json:"image"`
			Version int    `json:"version"`
			Status  string `json:"status"`
			Steps   []struct {
				Resource string `json:"resource"`
			} `json:"steps"`
		} `json:"images"`
	}
	json.Unmarshal(body, &rv)
	if code != 200 || len(rv.Targets) != 0 || len(rv.Images) != 1 || rv.Images[0].Image != "web" || rv.Images[0].Version != 1 ||
		rv.Images[0].Status != "succeeded" || len(rv.Images[0].Steps) != 6 || rv.Images[0].Steps[0].Resource != "image:base" ||
		rv.Images[0].Steps[5].Resource != "image:retention" {
		t.Fatalf("run: %d %s", code, body)
	}
	// A Block change reaches the built Image: image-recipe again.
	p2, _ := e.plan(adm, map[string]any{"reason": "block", "put": builtImageDoc("web", "hello")[:1]}, 201)
	if len(p2.HumanOnly) != 1 || p2.HumanOnly[0] != plan.HumanOnlyImageRecipe || len(p2.Images) != 1 || p2.Images[0].Build == nil {
		t.Fatalf("block change: %+v", p2)
	}
}

func TestBuildPlanManual(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "setup", "put": append(builtImageDoc("web", "hi"), imageDoc("stock", "0"))})
	// v1 is ready; a manual build of v2 keeps it (keep 2); v3 removes v1.
	e.manualBuild(adm, "web", 2, nil)
	// Stock and unknown Images.
	if code, body := adm.do("POST", "/api/v1/images/stock/build", nil); code != 422 {
		t.Fatalf("stock: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/images/nope/build", nil); code != 404 {
		t.Fatalf("unknown: %d %s", code, body)
	}
	code, body := adm.do("POST", "/api/v1/images/web/build", map[string]any{"reason": "fresh packages"})
	var p planOut
	json.Unmarshal(body, &p)
	if code != 201 || p.Origin != "build" || len(p.HumanOnly) != 0 || len(p.Images) != 1 || p.Images[0].Build.Trigger != "manual" ||
		p.Images[0].Build.Version != 3 {
		t.Fatalf("build: %d %s", code, body)
	}
	// keep 2 and a new build: v1 goes.
	if rm := p.Images[0].Removes; len(rm) != 1 || rm[0].Name != "plumb-img-web-v1" || rm[0].Version == nil || *rm[0].Version != 1 {
		t.Fatalf("removes = %+v", rm)
	}
	// A head change that leaves the recipe alone keeps the build Plan.
	e.runPlanAnyStatus(adm, map[string]any{"reason": "other", "put": []state.Doc{imageDoc("other", "1")}})
	if got := e.getPlan(adm, p.ID); got.Status != "pending" {
		t.Fatalf("after an unrelated change: %+v", got)
	}
	// An apply token approves a manual build of an unchanged recipe.
	tok := e.token(TierApply, "reversible")
	if code, body := tok.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	got := e.getPlan(adm, p.ID)
	head, _ := e.st.Head(e.ctx)
	run, _ := e.st.RunOfPlan(e.ctx, p.ID)
	if got.Status != "approved" || got.Revision != nil || got.RunID == nil || run.RevisionID != head.ID {
		t.Fatalf("approved build plan = %+v, run %+v, head %d", got, run, head.ID)
	}
	if head.PlanID == p.ID {
		t.Fatal("a build Plan committed a revision")
	}
	// v3 is ready, v1 is removed by retention, v2 stays (keep 2).
	vs, _ := e.st.ImageVersions(e.ctx, "web")
	if len(vs) != 3 || vs[0].Status != store.ImageReady || vs[1].Status != store.ImageReady || vs[2].Status != store.ImageRemoved {
		t.Fatalf("versions after the third build: %+v", vs)
	}
	objs, _ := e.drv.ListImages(e.ctx, "web")
	n := 0
	for _, o := range objs {
		if o.Kind == driver.ImageVersionTemplate {
			n++
			if o.Version == 1 {
				t.Error("the template of v1 is still there")
			}
		}
	}
	if n != 2 {
		t.Errorf("version templates = %d, want 2", n)
	}
}

// manualBuild proposes and approves a manual build and checks its end.
func (e *env) manualBuild(c *client, image string, wantVersion int, wantStatus *string) {
	e.t.Helper()
	code, body := c.do("POST", "/api/v1/images/"+image+"/build", nil)
	var p planOut
	json.Unmarshal(body, &p)
	if code != 201 || p.Images[0].Build.Version != wantVersion {
		e.t.Fatalf("build: %d %s", code, body)
	}
	if code, body := c.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		e.t.Fatalf("approve build: %d %s", code, body)
	}
	e.eng.Wait()
	v, err := e.st.GetImageVersion(e.ctx, image, wantVersion)
	want := store.ImageReady
	if wantStatus != nil {
		want = *wantStatus
	}
	if err != nil || v.Status != want {
		steps := ""
		if run, err := e.st.RunOfPlan(e.ctx, p.ID); err == nil {
			ss, _ := e.st.Steps(e.ctx, run.ID)
			for _, s := range ss {
				steps += "\n" + s.Resource + " " + s.Status + " " + s.Output
			}
		}
		e.t.Fatalf("version %d = %+v %v, want %s%s", wantVersion, v, err, want, steps)
	}
}

func TestBuildPlanStaleByRecipe(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "setup", "put": builtImageDoc("web", "hi")})
	code, body := adm.do("POST", "/api/v1/images/web/build", nil)
	var p planOut
	json.Unmarshal(body, &p)
	if code != 201 {
		t.Fatalf("build: %d %s", code, body)
	}
	e.runPlanAnyStatus(adm, map[string]any{"reason": "recipe", "put": builtImageDoc("web", "changed")[:1]})
	got := e.getPlan(adm, p.ID)
	if got.Status != "stale" || !strings.Contains(strings.Join(got.Warnings, " "), "recipe of the Image changed") {
		t.Fatalf("after a recipe change: %+v", got)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 409 {
		t.Fatalf("approve stale: %d %s", code, body)
	}
}

func TestBuildRunning(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "setup", "put": builtImageDoc("web", "hi")})
	_, body := adm.do("POST", "/api/v1/images/web/build", nil)
	var p planOut
	json.Unmarshal(body, &p)
	e.addVersion("web", store.ImageBuilding) // a build of another Run
	code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil)
	if code != 409 || !strings.Contains(string(body), "build-running") {
		t.Fatalf("approve during a build: %d %s", code, body)
	}
	if got := e.getPlan(adm, p.ID); got.Status != "pending" {
		t.Fatalf("plan after build-running = %+v", got)
	}
}

func TestTargetOfBuiltImage(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	adm := e.admin()
	target := doc(state.KindTarget, "web1", `{"type":"vm","image":"web","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"}}`)
	// The same Plan builds the Image.
	_, body := e.plan(adm, map[string]any{"reason": "both", "put": append(builtImageDoc("web", "hi"), target)}, 422)
	if !strings.Contains(body, "approve the build first") {
		t.Fatalf("same plan: %s", body)
	}
	// A failed first build: no ready version.
	e.buildAgents(func(a *linktest.Agent) { a.FailSeal(wire.SealStepHostKeys) })
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
	_, body = e.plan(adm, map[string]any{"reason": "target", "put": []state.Doc{target}}, 422)
	if !strings.Contains(body, "build the Image first") {
		t.Fatalf("no ready version: %s", body)
	}
}

func TestImageStatus(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "setup", "put": append(builtImageDoc("web", "hi"), imageDoc("stock", "0"))})
	e.addVersion("web", store.ImageFailed)
	e.addVersion("web", store.ImageReady)
	e.addVersion("web", store.ImageBuilding)
	code, body := adm.do("GET", "/api/v1/images/web", nil)
	var v struct {
		Status *struct {
			Versions []struct {
				Version int    `json:"version"`
				Status  string `json:"status"`
			} `json:"versions"`
			Building  *int `json:"building"`
			LastBuild *struct {
				Version int `json:"version"`
			} `json:"last_build"`
			UnusedImports []any `json:"unused_imports"`
		} `json:"status"`
	}
	json.Unmarshal(body, &v)
	if code != 200 || v.Status == nil || len(v.Status.Versions) != 4 || v.Status.Versions[0].Version != 4 ||
		v.Status.Building == nil || *v.Status.Building != 4 || v.Status.LastBuild == nil || v.Status.LastBuild.Version != 3 ||
		v.Status.UnusedImports == nil {
		t.Fatalf("image status: %d %s", code, body)
	}
	code, body = adm.do("GET", "/api/v1/images/stock", nil)
	if code != 200 || strings.Contains(string(body), `"status"`) {
		t.Fatalf("stock image: %d %s", code, body)
	}
}

// runPlanAnyStatus plans, approves and waits; the Run may fail (an image
// lane fails in part A).
func (e *env) runPlanAnyStatus(c *client, req map[string]any) {
	e.t.Helper()
	code, body := c.do("POST", "/api/v1/plans", req)
	if code != 201 {
		e.t.Fatalf("%v: plan %d %s", req["reason"], code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code, body := c.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		e.t.Fatalf("%v: approve %d %s", req["reason"], code, body)
	}
	e.eng.Wait()
}

// The image parts of the API match api/openapi.yaml (T-058 contract).
func TestContractImages(t *testing.T) {
	k := loadContract(t)
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	var calls []recorded
	call := func(method, path, template string, body any) []byte {
		t.Helper()
		rec := adm.raw(method, path, body)
		ct, _, _ := strings.Cut(rec.Header().Get("Content-Type"), ";")
		calls = append(calls, recorded{method, template, rec.Code, ct, rec.Body.Bytes()})
		return rec.Body.Bytes()
	}
	id := func(body []byte) string {
		var v struct{ ID int64 }
		json.Unmarshal(body, &v)
		return itoa(v.ID)
	}
	put := map[string]any{"reason": "image", "put": append(builtImageDoc("web", "hi"), imageDoc("stock", "0"))}
	pl := call("POST", "/api/v1/plans", "/plans", put)                                  // 201 with images, image-recipe
	call("GET", "/api/v1/plans/"+id(pl), "/plans/{id}", nil)                            // 200
	run := call("POST", "/api/v1/plans/"+id(pl)+"/approve", "/plans/{id}/approve", nil) // 202
	e.eng.Wait()
	call("GET", "/api/v1/runs/"+id(run), "/runs/{id}", nil) // 200 with images
	e.addVersion("web", store.ImageReady)
	e.addVersion("web", store.ImageBuilding)
	call("GET", "/api/v1/images", "/images", nil)                                                          // 200 with status
	call("GET", "/api/v1/images/web", "/images/{name}", nil)                                               // 200 with status
	call("GET", "/api/v1/images/stock", "/images/{name}", nil)                                             // 200 without status
	call("POST", "/api/v1/images/stock/build", "/images/{name}/build", nil)                                // 422
	call("POST", "/api/v1/images/nope/build", "/images/{name}/build", nil)                                 // 404
	b := call("POST", "/api/v1/images/web/build", "/images/{name}/build", map[string]any{"reason": "now"}) // 201
	call("POST", "/api/v1/plans/"+id(b)+"/approve", "/plans/{id}/approve", nil)                            // 409 build-running
	for _, r := range calls {
		k.check(t, r)
	}
	want := []int{201, 200, 202, 200, 200, 200, 200, 422, 404, 201, 409}
	for i, r := range calls {
		if r.status != want[i] {
			t.Errorf("call %d %s %s: %d, want %d: %s", i, r.method, r.template, r.status, want[i], r.body)
		}
	}
}

// Review S1 of PR #128: the import file of a running build stays off the
// unused list, also when the document has another checksum now.
func TestUnusedImportsKeepRunningBuild(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "setup", "put": builtImageDoc("web", "hi")})
	old, other := strings.Repeat("b", 64), strings.Repeat("c", 64)
	for _, sum := range []string{builtSHA, old, other} {
		if _, err := e.drv.FetchImage(e.ctx, driver.ImageSpec{Name: "web", Kind: "vm", URL: "https://images.invalid/u.img", SHA256: sum}); err != nil {
			t.Fatal(err)
		}
	}
	head, _ := e.st.Head(e.ctx)
	run, _ := e.st.RunOfPlan(e.ctx, head.PlanID)
	running, err := e.st.AddImageVersion(e.ctx, store.ImageVersion{Image: "web", Trigger: store.TriggerManual, RunID: run.ID, RevisionID: head.ID,
		RecipeSHA256: builtSHA, BaseSHA256: old, DiskGB: 10}, func(int) string { return builtSHA })
	if err != nil {
		t.Fatal(err)
	}
	code, body := adm.do("GET", "/api/v1/images/web", nil)
	var v struct {
		Status struct {
			UnusedImports []struct {
				Volume  string `json:"volume"`
				Command string `json:"command"`
			} `json:"unused_imports"`
		} `json:"status"`
	}
	json.Unmarshal(body, &v)
	got := v.Status.UnusedImports
	want := "plumb-images:import/plumb-web-" + other[:12] + ".qcow2"
	if code != 200 || len(got) != 1 || got[0].Volume != want || got[0].Command != "pvesm free "+want {
		t.Fatalf("unused imports: %d %s", code, body)
	}
	// After the build ends, its old checksum is unused too.
	if err := e.st.FinishImageVersion(e.ctx, "web", running.Version, store.ImageFailed); err != nil {
		t.Fatal(err)
	}
	_, body = adm.do("GET", "/api/v1/images/web", nil)
	json.Unmarshal(body, &v)
	if len(v.Status.UnusedImports) != 2 {
		t.Fatalf("after the build: %s", body)
	}
}

// The build Plan and the Image status show the limit and the reserved
// bytes of the disk gate (T-074), not a constant.
func TestBuildPlanShowsDiskGate(t *testing.T) {
	e := newEnv(t, false, withBuilds, func(c *plan.Config) { c.DiskLimitPercent = 90 })
	adm := e.admin()
	_, body := e.plan(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")}, 201)
	if !strings.Contains(body, `"limit_percent":90`) || !strings.Contains(body, `"reserved_bytes":0`) {
		t.Fatalf("plan disk: %s", body)
	}
}
