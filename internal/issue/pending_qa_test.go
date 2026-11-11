package issue_test

import (
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// A stopped guest can start after approval was planned. With restart
// permission off, both cold-start attributes must then wait safely.
func TestProtectedGuestStartedAfterPlanQA(t *testing.T) {
	for _, attr := range []string{driver.AttrCores, driver.AttrMemory} {
		t.Run(attr, func(t *testing.T) {
			e, id := pendingEnv(t, false)
			e.drv.StopOutside(id)
			target := guestTarget(2048, false)
			if attr == driver.AttrCores {
				target = guestTarget(1024, false)
				target.Spec = []byte(strings.Replace(string(target.Spec), `"cores":2`, `"cores":4`, 1))
			}
			p := e.propose(plan.Proposal{Put: []state.Doc{target}})
			if c := find(e.guestChanges(p).Changes, attr); c == nil || c.WaitsForRestart {
				t.Fatalf("stopped proposal: %+v", c)
			}
			e.drv.StartOutside(id)
			boots, power := e.drv.Boots(id), e.drv.Calls(fake.OpPower)
			run := e.approve(p.ID)
			c := find(stepChanges(t, e.guestStep(run)), attr)
			if run.Status != "succeeded" || run.WaitsForRestart != 1 || c == nil || !c.WaitsForRestart || c.Risk != wire.RiskDisruptive {
				t.Fatalf("run %s, waits %d, change %+v", run.Status, run.WaitsForRestart, c)
			}
			if e.drv.Calls(fake.OpPower) != power || e.drv.Boots(id) != boots {
				t.Fatal("the protected VM received a power action")
			}
			if w := e.waiting(); len(w) != 1 || w[0].Attr != attr {
				t.Fatalf("waiting: %+v", w)
			}
			e.check()
			if issues, err := e.st.Issues(e.ctx, "web1"); err != nil || len(issues) != 0 {
				t.Fatalf("waiting became drift: %+v, %v", issues, err)
			}
		})
	}
}
