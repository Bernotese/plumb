package api

import (
	"encoding/json"
	"github.com/Bernotese/plumb/internal/link/linktest"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
	"strings"
	"testing"
)

func TestBuildDriftAndVersionEventsQA(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "ready"
		if fail {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, false, withBuilds)
			reports := make(chan int, 1)
			e.buildAgents(func(a *linktest.Agent) {
				if fail {
					a.FailApply("file:/etc/motd", "QA failure")
				}
				a.OnCommand(func(cmd wire.CommandType) {
					if cmd == wire.CommandApply {
						reports <- a.Report(e.ctx, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: "file:/etc/motd", Status: wire.StatusDiffers, Changes: []wire.Change{{Attr: "content", From: "outside", To: "hi", Risk: wire.RiskReversible}}}}})
					}
				})
			})
			e.runPlanAnyStatus(e.admin(), map[string]any{"reason": "image", "put": builtImageDoc("web", "hi")})
			if code := <-reports; code != 204 {
				t.Fatalf("build drift: %d", code)
			}
			issues, err := e.st.Issues(e.ctx, "build:web")
			if err != nil || len(issues) != 0 {
				t.Fatalf("build issues: %+v %v", issues, err)
			}
			evs, err := e.st.EventsAfter(e.ctx, 0, 10000)
			if err != nil {
				t.Fatal(err)
			}
			v, err := e.st.GetImageVersion(e.ctx, "web", 1)
			if err != nil {
				t.Fatal(err)
			}
			var statuses []string
			for _, ev := range evs {
				if strings.HasPrefix(ev.Type, "issue.") {
					t.Errorf("build drift event: %+v", ev)
				}
				if ev.Type != "image.version" {
					continue
				}
				var data struct {
					Version int    `json:"version"`
					Status  string `json:"status"`
					Run     int64  `json:"run"`
				}
				if err := json.Unmarshal(ev.Data, &data); err != nil {
					t.Fatal(err)
				}
				if ev.Subject != "image:web" || ev.Actor != "system" || data.Version != 1 || data.Run != v.RunID {
					t.Errorf("version event: %+v", ev)
				}
				statuses = append(statuses, data.Status)
			}
			if len(statuses) != 2 || statuses[0] != store.ImageBuilding || statuses[1] != name {
				t.Fatalf("version transitions: %v", statuses)
			}
		})
	}
}
