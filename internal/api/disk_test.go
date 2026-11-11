package api

// T-074: the disk gate at guest create, through the API and the engine
// with the fake driver. The fake counts each guest and each template with
// its whole disk; a base template is 3 GiB.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// diskRun plans the documents, approves the Plan and waits. It returns the
// Plan warnings, the Run and its steps.
func (e *env) diskRun(c *client, put ...state.Doc) ([]string, store.Run, []store.Step) {
	e.t.Helper()
	code, body := c.do("POST", "/api/v1/plans", map[string]any{"reason": "disk", "put": put})
	if code != 201 {
		e.t.Fatalf("plan: %d %s", code, body)
	}
	var p struct {
		ID       int64
		Warnings []string
	}
	json.Unmarshal(body, &p)
	if code, body := c.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		e.t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	run, err := e.st.RunOfPlan(e.ctx, p.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	steps, _ := e.st.Steps(e.ctx, run.ID)
	return p.Warnings, run, steps
}

func diskBase() []state.Doc {
	return []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), doc(state.KindBlock, "motd", motdSpec)}
}

func guestStepOutput(steps []store.Step, target string) string {
	for _, s := range steps {
		if s.Target == target && s.Resource == "guest:config" {
			return s.Status + ": " + s.Output
		}
	}
	return ""
}

// used 72 GiB + template 3 GiB + disk 10 GiB = exactly 85 %: the create runs.
func TestDiskGateCreateAtLimit(t *testing.T) {
	e := newEnv(t, true)
	e.drv.SetDiskSpace(100*fake.GiB, 72*fake.GiB, false)
	e.agent("web1")
	warns, run, steps := e.diskRun(e.admin(), append(diskBase(), targetDoc("web1", "hi"))...)
	if run.Status != "succeeded" {
		t.Fatalf("run %s: %s", run.Status, guestStepOutput(steps, "web1"))
	}
	for _, w := range warns {
		if strings.Contains(w, "full") {
			t.Errorf("warning at the limit: %q", w)
		}
	}
	if e.eng.Disk().Reserved() != 0 {
		t.Errorf("reserved after the Run: %d", e.eng.Disk().Reserved())
	}
}

// One byte more: the step fails with the numbers, no guest and no clone.
func TestDiskGateRefusesCreate(t *testing.T) {
	e := newEnv(t, true)
	e.drv.SetDiskSpace(100*fake.GiB, 72*fake.GiB+1, false)
	e.agent("web1")
	_, run, steps := e.diskRun(e.admin(), append(diskBase(), targetDoc("web1", "hi"))...)
	out := guestStepOutput(steps, "web1")
	if run.Status != "failed" {
		t.Fatalf("run %s: %s", run.Status, out)
	}
	for _, want := range []string{"local-lvm would be 85.1 % full", "the 10 GiB disk of web1", "limit 85 %", "nothing was made"} {
		if !strings.Contains(out, want) {
			t.Errorf("step %q misses %q", out, want)
		}
	}
	if n := e.drv.Calls(fake.OpCreate); n != 0 {
		t.Errorf("%d creates", n)
	}
	if len(e.drv.Guests()) != 0 {
		t.Error("a guest was made")
	}
	if _, err := e.st.GetBinding(e.ctx, state.KindTarget, "web1"); err == nil {
		t.Error("a binding was made")
	}
	if e.eng.Disk().Reserved() != 0 {
		t.Errorf("reserved after a refusal: %d", e.eng.Disk().Reserved())
	}
}

// Two creates in one Run fit alone (67 + 3 + 10 = 80 %) but not together
// (90 %): exactly one is made; the Plan warned before.
func TestDiskGateTwoCreatesCountTogether(t *testing.T) {
	e := newEnv(t, true)
	e.drv.SetDiskSpace(100*fake.GiB, 67*fake.GiB, false)
	e.agent("web1")
	e.agent("web2")
	warns, run, steps := e.diskRun(e.admin(), append(diskBase(), targetDoc("web1", "a"), targetDoc("web2", "b"))...)
	if run.Status != "failed" {
		t.Fatalf("run %s", run.Status)
	}
	if n := len(e.drv.Guests()); n != 1 {
		t.Fatalf("%d guests", n)
	}
	failed := 0
	for _, tgt := range []string{"web1", "web2"} {
		if strings.Contains(guestStepOutput(steps, tgt), "would be") {
			failed++
		}
	}
	if failed != 1 {
		t.Errorf("%d refused steps", failed)
	}
	// Propose time: 67 used + 20 = 87 % (the template is not made yet).
	found := false
	for _, w := range warns {
		if strings.Contains(w, "local-lvm would be 87.0 % full with the 20 GiB disks of the 2 new guests") && strings.Contains(w, "limit 85 %") {
			found = true
		}
	}
	if !found {
		t.Errorf("no disk warning: %q", warns)
	}
	if e.eng.Disk().Reserved() != 0 {
		t.Errorf("reserved after the Run: %d", e.eng.Disk().Reserved())
	}
}
