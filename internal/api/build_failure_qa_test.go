package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/link/linktest"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func TestBuildFailureCleanupQA(t *testing.T) {
	for _, tc := range []struct {
		name    string
		op      fake.Op
		command wire.CommandType
		step    string
		enroll  bool
	}{
		{name: "create error", op: fake.OpCreateBuild, step: wire.GuestConfigID},
		{name: "create timeout", op: fake.OpCreateBuild, step: wire.GuestConfigID},
		{name: "enroll timeout", step: "file:/etc/motd", enroll: true},
		{name: "apply timeout", command: wire.CommandApply, step: "file:/etc/motd"},
		{name: "seal timeout", command: wire.CommandSeal, step: "image:seal"},
		{name: "convert error", op: fake.OpMakeImage, step: "image:template"},
		{name: "convert timeout", op: fake.OpMakeImage, step: "image:template"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, false, withBuilds, func(c *plan.Config) {
				c.BuildEnrollTimeout = 100 * time.Millisecond
				c.ApplyTimeout = 200 * time.Millisecond
				c.SealTimeout = 200 * time.Millisecond
			})
			release := make(chan struct{})
			defer close(release)
			if !tc.enroll {
				e.buildAgents(func(a *linktest.Agent) {
					a.OnCommand(func(cmd wire.CommandType) {
						if cmd == tc.command {
							<-release
						}
					})
				})
			}
			if tc.op != "" {
				err := errors.New("injected driver error")
				if strings.Contains(tc.name, "timeout") {
					err = context.DeadlineExceeded
				}
				e.drv.Fail(tc.op, err)
			}
			adm := e.admin()
			p, _ := e.plan(adm, map[string]any{"reason": "failure QA", "put": builtImageDoc("web", "hi")}, 201)
			if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
				t.Fatalf("approve: %d %s", code, body)
			}
			e.eng.Wait()
			if v, err := e.st.GetImageVersion(e.ctx, "web", 1); err != nil || v.Status != store.ImageFailed {
				t.Fatalf("version: %+v %v", v, err)
			}
			if s := e.lastSteps(p.ID)[tc.step]; s.Status != "failed" {
				t.Fatalf("step: %+v", s)
			}
			e.noBuildLeft("web")
			if got := e.versionTemplates("web"); len(got) != 0 {
				t.Fatalf("templates: %v", got)
			}
			if got := e.eng.Disk().Reserved(); got != 0 {
				t.Fatalf("reserved: %d", got)
			}
		})
	}
}

func TestRetentionListFailureKeepsUsableVersionQA(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	e.buildAgents(nil)
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
	e.manualBuild(adm, "web", 2, nil)
	e.drv.Hook(fake.OpMakeImage, func() { e.drv.Fail(fake.OpListImages, errors.New("retention unavailable")) })
	e.manualBuild(adm, "web", 3, nil)
	v, err := e.st.GetImageVersion(e.ctx, "web", 3)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := e.st.Steps(e.ctx, v.RunID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range steps {
		if s.Resource == "image:retention" {
			found = true
			if s.Status != "failed" || !strings.Contains(s.Output, "retention unavailable") {
				t.Fatalf("retention: %+v", s)
			}
		}
	}
	if !found {
		t.Fatal("missing retention step")
	}
	run, err := e.st.GetRun(e.ctx, v.RunID)
	if err != nil || run.Status != "failed" {
		t.Fatalf("run: %+v %v", run, err)
	}
	if got := e.versionTemplates("web"); len(got) != 3 {
		t.Fatalf("templates after failure: %v", got)
	}
	e.noBuildLeft("web")
	e.agent("web1")
	target := doc(state.KindTarget, "web1", `{"type":"vm","image":"web","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"}}`)
	e.runPlan(adm, map[string]any{"reason": "use ready version", "put": []state.Doc{target}})
	if code, body := adm.do("GET", "/api/v1/targets/web1", nil); code != 200 || !strings.Contains(string(body), `"image_version":3`) {
		t.Fatalf("target: %d %s", code, body)
	}
	e.manualBuild(adm, "web", 4, nil)
	if got := e.versionTemplates("web"); len(got) != 2 {
		t.Fatalf("backlog remains: %v", got)
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
