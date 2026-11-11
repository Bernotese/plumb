package api

import (
	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/link/linktest"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
	"testing"
)

// Check the content that reached the guest, not just its success report.
func TestBuildAppliesOnlyRecipeValues(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	agents := make(chan *linktest.Agent, 1)
	e.buildAgents(func(a *linktest.Agent) { agents <- a })
	e.runPlanAnyStatus(e.admin(), map[string]any{"reason": "image", "put": builtImageDoc("web", "public recipe")})
	a := <-agents
	if got := a.Guest("file:/etc/motd"); got["content"] != "public recipe" {
		t.Fatalf("build content = %v", got)
	}
	for _, cmd := range a.Commands() {
		if cmd == wire.CommandWatch {
			t.Error("build agent received watch")
		}
	}
}

// A failed row can still have a template after a late conversion (N1).
// Neither a failed row nor a building row may supply a Target.
func TestTargetSkipsNonReadyTemplates(t *testing.T) {
	for _, status := range []string{store.ImageFailed, store.ImageBuilding} {
		t.Run(status, func(t *testing.T) {
			e := newEnv(t, false, withBuilds)
			e.buildAgents(nil)
			adm := e.admin()
			e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
			n := e.addVersion("web", status)
			v, _ := e.st.GetImageVersion(e.ctx, "web", n)
			objs, _ := e.drv.ListImages(e.ctx, "web")
			var base driver.ImageRef
			for _, o := range objs {
				if o.Kind == driver.ImageBaseTemplate {
					base = o.Ref
				}
			}
			gid, err := e.drv.CreateBuild(e.ctx, driver.BuildSpec{Image: "web", Version: n, Base: base, Cores: 1, MemoryMB: 1024, DiskGB: 10, Bridge: "vmbr0", IP: "dhcp"}, driver.Bootstrap{Token: "unused"})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.drv.StopOutside(gid); err != nil {
				t.Fatal(err)
			}
			if _, err := e.drv.MakeImage(e.ctx, gid, driver.BuiltImage{Image: "web", Version: n, Digest: v.Digest}); err != nil {
				t.Fatal(err)
			}
			e.agent("web1")
			target := doc(state.KindTarget, "web1", `{"type":"vm","image":"web","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"}}`)
			e.runPlan(adm, map[string]any{"reason": "target", "put": []state.Doc{target}})
			b, _ := e.st.GetBinding(e.ctx, state.KindTarget, "web1")
			st, err := e.drv.Observe(e.ctx, driver.GuestID(b.Ref))
			if err != nil || st.Spec.ImageVersion != 1 {
				t.Fatalf("Target version = %d, err %v; want ready v1", st.Spec.ImageVersion, err)
			}
		})
	}
}
