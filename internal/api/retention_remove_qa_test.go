package api

import (
	"errors"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"strings"
	"testing"
)

// D-232: a removal error that is no "in use" refusal fails the retention
// step and the Run; v3 stays ready and usable, and the next build retries.
func TestRetentionRemoveErrorFailsRunAndRetriesQA(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
	e.manualBuild(adm, "web", 2, nil)
	e.drv.Fail(fake.OpRemoveImage, errors.New("retention remove unavailable"))
	e.manualBuild(adm, "web", 3, nil)
	v, _ := e.st.GetImageVersion(e.ctx, "web", 3)
	run, err := e.st.GetRun(e.ctx, v.RunID)
	if err != nil || run.Status != "failed" {
		t.Fatalf("run: %+v %v", run, err)
	}
	steps, err := e.st.Steps(e.ctx, v.RunID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range steps {
		if s.Resource == "image:retention" {
			found = true
			want := "could not remove plumb-img-web-v1: retention remove unavailable; v3 is ready and usable; old templates stay until the next build"
			if s.Status != "failed" || !strings.Contains(s.Output, want) {
				t.Fatalf("retention: %+v", s)
			}
		}
	}
	if !found {
		t.Fatal("missing retention step")
	}
	if got := e.versionTemplates("web"); len(got) != 3 {
		t.Fatalf("templates: %v", got)
	}
	e.noBuildLeft("web")
	e.agent("web1")
	target := doc(state.KindTarget, "web1", `{"type":"vm","image":"web","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"}}`)
	e.runPlan(adm, map[string]any{"reason": "use ready version", "put": []state.Doc{target}})
	if code, body := adm.do("GET", "/api/v1/targets/web1", nil); code != 200 || !strings.Contains(string(body), `"image_version":3`) {
		t.Fatalf("target: %d %s", code, body)
	}
	e.manualBuild(adm, "web", 4, nil)
	if status, s := e.retentionOf("web", 4); status != "succeeded" || s.Status != "applied" {
		t.Fatalf("v4: run %s, retention %+v", status, s)
	}
	for n := 1; n <= 4; n++ {
		v, err := e.st.GetImageVersion(e.ctx, "web", n)
		want := store.ImageReady
		if n < 3 {
			want = store.ImageRemoved
		}
		if err != nil || v.Status != want {
			t.Fatalf("v%d: %+v %v", n, v, err)
		}
	}
	e.noBuildLeft("web")
}
