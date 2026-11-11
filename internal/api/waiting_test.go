package api

import (
	"encoding/json"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
)

// T-056 (D-195): TargetStatus.waiting and the phase restart_needed; the
// list shows also when the phase is drifted; Run.waits_for_restart.
func TestWaitingStatus(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	status := func() (string, []map[string]any) {
		t.Helper()
		code, body := adm.do("GET", "/api/v1/targets/web1", nil)
		if code != 200 {
			t.Fatalf("get target: %d %s", code, body)
		}
		var v struct {
			Status struct {
				Phase   string           `json:"phase"`
				Waiting []map[string]any `json:"waiting"`
			} `json:"status"`
		}
		json.Unmarshal(body, &v)
		return v.Status.Phase, v.Status.Waiting
	}
	if phase, w := status(); phase != "ready" || w == nil || len(w) != 0 {
		t.Fatalf("before: %s %v", phase, w)
	}
	e.runPlan(adm, map[string]any{"reason": "cores", "put": []state.Doc{targetWith("web1", "ubuntu", 4)}})
	phase, w := status()
	if phase != "restart_needed" || len(w) != 1 || w[0]["attr"] != "cores" || w[0]["from"] != float64(2) || w[0]["to"] != float64(4) || w[0]["since"] == "" {
		t.Errorf("after the change: %s %v", phase, w)
	}
	code, body := adm.do("GET", "/api/v1/runs?limit=1", nil)
	var runs struct {
		Items []struct {
			WaitsForRestart int `json:"waits_for_restart"`
		} `json:"items"`
	}
	json.Unmarshal(body, &runs)
	if code != 200 || len(runs.Items) != 1 || runs.Items[0].WaitsForRestart != 1 {
		t.Errorf("runs: %d %s", code, body)
	}
	// An open Issue wins the phase; the list stays.
	e.drv.Tamper(e.guestID("web1"), func(s *driver.GuestSpec) { s.Bridge = "vmbr7" })
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Fatal(err)
	}
	if phase, w := status(); phase != "drifted" || len(w) != 1 {
		t.Errorf("drifted: %s %v", phase, w)
	}
}
