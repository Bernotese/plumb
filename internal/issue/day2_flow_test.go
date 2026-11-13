package issue_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-115: Proxmox day 2 (D-291, D-299): power, tags and the disk grow.

// day2Target is web1 with extra guest settings (`"power":"stopped"`, …),
// allow_restart and the motd text.
func day2Target(guest string, allow bool, text string) state.Doc {
	a := "false"
	if allow {
		a = "true"
	}
	return doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","allow_restart":`+a+`,"guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"`+
		guest+`},"blocks":[{"use":"motd","params":{"text":"`+text+`"}}]}`)
}

// day2Env seeds web1 with the guest settings and allow_restart true.
func day2Env(t *testing.T, guest string) (*env, driver.GuestID) {
	t.Helper()
	e := newEnv(t)
	e.agent("web1")
	put := []state.Doc{doc(state.KindImage, "ubuntu", image), doc(state.KindBlock, "motd", motd), day2Target(guest, true, "hello")}
	if run := e.approve(e.propose(plan.Proposal{Put: put}).ID); run.Status != "succeeded" {
		t.Fatalf("seed: %s %s", run.Status, stepNames(e.steps(run)))
	}
	e.online("web1")
	return e, e.guestID("web1")
}

func (e *env) stepOf(run store.Run, resource string) store.Step {
	e.t.Helper()
	for _, s := range e.steps(run) {
		if s.Resource == resource {
			return s
		}
	}
	e.t.Fatalf("no step %s in run %d", resource, run.ID)
	return store.Step{}
}

// Stop after the agent steps, skipped agent steps while stopped, a start
// that plans the resources unverified (D-299, D-308).
func TestDay2StopAndStart(t *testing.T) {
	e, id := day2Env(t, `,"power":"running"`)

	// Stop with a resource change: the file first, then the stop.
	p := e.propose(plan.Proposal{Put: []state.Doc{day2Target(`,"power":"stopped"`, true, "bye")}})
	gc := e.guestChanges(p)
	if c := find(gc.Changes, driver.AttrPower); c == nil || c.To != "stopped" || c.Risk != wire.RiskDisruptive || gc.Risk != wire.RiskDisruptive {
		t.Fatalf("stop plan: %+v", gc)
	}
	run := e.approve(p.ID)
	if run.Status != "succeeded" || stepNames(e.steps(run)) != "guest:config=applied file:/etc/motd=applied" {
		t.Fatalf("stop run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if st := e.observe("web1"); st.Power != driver.PowerStopped {
		t.Fatalf("power after the stop: %s", st.Power)
	}
	if c, _ := e.agents["web1"].Guest("file:/etc/motd")["content"].(string); !strings.Contains(c, "bye") {
		t.Errorf("the agent step did not run before the stop: %v", e.agents["web1"].Guest("file:/etc/motd"))
	}
	// The stop is no drift, and the status says stopped on purpose.
	e.check()
	if _, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID); err == nil {
		t.Error("the planned stop opened an Issue")
	}
	if l, _ := e.eng.Live(e.ctx, "web1"); l.DesiredPower != "stopped" || l.Power != "stopped" {
		t.Errorf("live: %+v", l)
	}

	// A change while stopped: no observe, the agent step is skipped.
	p = e.propose(plan.Proposal{Put: []state.Doc{day2Target(`,"power":"stopped"`, true, "later")}})
	if tp := changes(t, p)["web1"]; len(tp.Resources) != 0 {
		t.Errorf("plan of a stopped Target: %+v", tp)
	}
	run = e.approve(p.ID)
	if s := e.stepOf(run, "file:/etc/motd"); run.Status != "succeeded" || s.Status != "skipped" || s.Output != "Target stopped" {
		t.Errorf("stopped run %s: %+v", run.Status, s)
	}

	// Start: the resources are planned unverified, the Run starts the
	// guest, waits for the new hello and applies.
	boots := e.drv.Boots(id)
	p = e.propose(plan.Proposal{Put: []state.Doc{day2Target(`,"power":"running"`, true, "later")}})
	tp := changes(t, p)["web1"]
	if c := find(e.guestChanges(p).Changes, driver.AttrPower); c == nil || c.To != "running" || c.Risk != wire.RiskReversible {
		t.Fatalf("start plan: %+v", tp)
	}
	if len(tp.Resources) != 2 || !tp.Resources[1].Unverified {
		t.Errorf("start plan resources: %+v", tp.Resources)
	}
	if n := strings.Join(planNotes(t, p), " "); !strings.Contains(n, "web1 is stopped and the Run starts it") {
		t.Errorf("start note: %q", n)
	}
	run = e.approve(p.ID)
	if run.Status != "succeeded" || stepNames(e.steps(run)) != "guest:config=applied file:/etc/motd=applied" {
		t.Fatalf("start run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if st := e.observe("web1"); st.Power != driver.PowerRunning || e.drv.Boots(id) != boots+1 {
		t.Errorf("after the start: %s, boots %d", st.Power, e.drv.Boots(id)-boots)
	}
}

// A new Target with power: stopped is created, enrolled, applied, then
// stopped (D-299).
func TestDay2CreateStopped(t *testing.T) {
	e := newEnv(t)
	e.agent("web1")
	put := []state.Doc{doc(state.KindImage, "ubuntu", image), doc(state.KindBlock, "motd", motd), day2Target(`,"power":"stopped","tags":["web"]`, true, "hi")}
	p := e.propose(plan.Proposal{Put: put})
	gc := e.guestChanges(p)
	if find(gc.Changes, driver.AttrPower) == nil || find(gc.Changes, driver.AttrTags) == nil {
		t.Errorf("create plan: %+v", gc)
	}
	run := e.approve(p.ID)
	if run.Status != "succeeded" || stepNames(e.steps(run)) != "guest:config=applied file:/etc/motd=applied" {
		t.Fatalf("run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if st := e.observe("web1"); st.Power != driver.PowerStopped || len(st.Spec.Tags) != 1 {
		t.Errorf("after the create: %s %v", st.Power, st.Spec.Tags)
	}
}

// A stop needs allow_restart (refused at save, D-291); a failed agent step
// keeps the guest running.
func TestDay2StopRules(t *testing.T) {
	e, _ := day2Env(t, "")
	_, err := e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Put: []state.Doc{day2Target(`,"power":"stopped"`, false, "hello")}}, Author: "user:admin"})
	var se state.Errors
	if !errors.As(err, &se) || se[0].Path != "/spec/guest/power" {
		t.Errorf("stop without allow_restart: %v", err)
	}
	e.agents["web1"].FailApply("file:/etc/motd", "disk full")
	run := e.approve(e.propose(plan.Proposal{Put: []state.Doc{day2Target(`,"power":"stopped"`, true, "bye")}}).ID)
	g := e.guestStep(run)
	if run.Status != "failed" || g.Status != "failed" || !strings.Contains(g.Output, "not stopped") || find(stepChanges(t, g), driver.AttrPower) == nil {
		t.Errorf("run %s, guest step %+v", run.Status, g)
	}
	if st := e.observe("web1"); st.Power != driver.PowerRunning {
		t.Errorf("power after a failed agent step: %s", st.Power)
	}
}

// A start or stop outside Plumb is drift of a managed power (D-299, D-309);
// an unmanaged power is not.
func TestDay2PowerDrift(t *testing.T) {
	e, id := day2Env(t, `,"power":"running"`)
	e.check()
	if err := e.drv.StopOutside(id); err != nil {
		t.Fatal(err)
	}
	e.check()
	iss, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID)
	if err != nil || !strings.Contains(string(iss.Changes), `"attr":"power","from":"stopped","to":"running"`) {
		t.Fatalf("power drift: %+v %v", iss, err)
	}
	p, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	if run := e.approve(p.ID); run.Status != "succeeded" {
		t.Fatalf("revert run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if st := e.observe("web1"); st.Power != driver.PowerRunning {
		t.Errorf("after the revert: %s", st.Power)
	}

	// Not managed: a stop outside Plumb opens nothing.
	e2, id2 := day2Env(t, "")
	e2.check()
	e2.drv.StopOutside(id2)
	e2.check()
	if _, err := e2.st.OpenIssue(e2.ctx, "web1", wire.GuestConfigID); err == nil {
		t.Error("an unmanaged power opened an Issue")
	}
}

// Tags: written by the Run, drift when changed in Proxmox, a refusal names
// the datacenter option (D-299).
func TestDay2Tags(t *testing.T) {
	e, id := day2Env(t, `,"tags":["web"]`)
	if st := e.observe("web1"); len(st.Spec.Tags) != 1 || st.Spec.Tags[0] != "web" {
		t.Fatalf("tags after the create: %v", st.Spec.Tags)
	}
	run := e.approve(e.propose(plan.Proposal{Put: []state.Doc{day2Target(`,"tags":["web","prod"]`, true, "hello")}}).ID)
	if run.Status != "succeeded" || find(stepChanges(t, e.guestStep(run)), driver.AttrTags) == nil {
		t.Fatalf("tags run %s: %+v", run.Status, e.guestStep(run))
	}
	if st := e.observe("web1"); len(st.Spec.Tags) != 2 {
		t.Errorf("tags: %v", st.Spec.Tags)
	}
	e.check()
	e.drv.Tamper(id, func(s *driver.GuestSpec) { s.Tags = []string{"x"} })
	e.check()
	if iss, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID); err != nil || !strings.Contains(string(iss.Changes), `"tags"`) {
		t.Errorf("tags drift: %+v %v", iss, err)
	}
	e.drv.RefuseTags(errors.New("403 Forbidden: Permission check failed"))
	run = e.approve(e.propose(plan.Proposal{Put: []state.Doc{day2Target(`,"tags":["db"]`, true, "hello")}}).ID)
	if g := e.guestStep(run); run.Status != "failed" || !strings.Contains(g.Output, "403") {
		t.Errorf("refused tags: %s %+v", run.Status, g)
	}
}

// helloWithGrow makes the agent of web1 say that it runs the grow command.
func (e *env) helloWithGrow(item wire.Item) {
	e.t.Helper()
	a := e.agents["web1"]
	caps := wire.BaselineCapabilities()
	caps.Commands = append(caps.Commands, wire.CommandGrow)
	a.SetHello(wire.Hello{Capabilities: &caps})
	if code := a.Report(e.ctx, wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "test", Hostname: "fake", OS: "fake 1", Capabilities: &caps}}); code != 204 {
		e.t.Fatalf("hello: %d", code)
	}
	a.OnGrow(func(b wire.GrowBody) wire.Item {
		if b.MinBytes != plan.DiskBytes(20) {
			item = wire.Item{Resource: wire.GrowItemID, Status: wire.StatusFailed, Output: "wrong size"}
		}
		return item
	})
}

// A VM disk grows: the driver resizes, then the agent grows the root file
// system; a failed grow fails the guest step and the bigger disk stays.
func TestDay2VMGrow(t *testing.T) {
	e, _ := day2Env(t, "")
	grow := func() store.Run {
		return e.approve(e.propose(plan.Proposal{Put: []state.Doc{doc(state.KindTarget, "web1",
			`{"type":"vm","image":"ubuntu","allow_restart":true,"guest":{"cores":2,"memory":1024,"disk":20,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"hello"}}]}`)}}).ID)
	}
	e.helloWithGrow(wire.Item{Resource: wire.GrowItemID, Status: wire.StatusFailed, Output: "/ is on LVM"})
	run := grow()
	g := e.guestStep(run)
	if run.Status != "failed" || g.Status != "failed" || !strings.Contains(g.Output, "/ is on LVM") || !strings.Contains(g.Output, "20 GiB") {
		t.Fatalf("failed grow: %s %+v", run.Status, g)
	}
	if st := e.observe("web1"); st.Spec.DiskGB != 20 {
		t.Errorf("disk after a failed root grow: %d", st.Spec.DiskGB)
	}
	// The disk keeps 20 GiB, the root grow is pending (review M1 of #217):
	// after the fix in the guest, the next Plan grows the file system.
	if b, _ := e.st.GetBinding(e.ctx, state.KindTarget, "web1"); b.RootGrow != 20 || len(b.Waiting) != 0 {
		t.Fatalf("binding after a failed root grow: %+v", b)
	}
	e.helloWithGrow(wire.Item{Resource: wire.GrowItemID, Status: wire.StatusApplied})
	p := e.propose(plan.Proposal{Targets: []string{"web1"}})
	gc := e.guestChanges(p)
	if c := find(gc.Changes, plan.AttrRootFS); c == nil || c.To != "20 GiB" || gc.Risk != wire.RiskDisruptive || find(gc.Changes, driver.AttrDisk) != nil {
		t.Fatalf("retry plan: %+v", gc)
	}
	run = e.approve(p.ID)
	if run.Status != "succeeded" || find(stepChanges(t, e.guestStep(run)), plan.AttrRootFS) == nil {
		t.Fatalf("retry run %s: %+v", run.Status, e.guestStep(run))
	}
	if b, _ := e.st.GetBinding(e.ctx, state.KindTarget, "web1"); b.RootGrow != 0 {
		t.Errorf("the mark stays after the grow: %+v", b)
	}
	if gc := e.guestChanges(e.propose(plan.Proposal{Targets: []string{"web1"}})); len(gc.Changes) != 0 {
		t.Errorf("a Plan after the grow: %+v", gc)
	}
}

func TestDay2VMGrowPasses(t *testing.T) {
	e, _ := day2Env(t, "")
	e.helloWithGrow(wire.Item{Resource: wire.GrowItemID, Status: wire.StatusApplied})
	put := doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","allow_restart":true,"guest":{"cores":2,"memory":1024,"disk":20,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"hello"}}]}`)
	p := e.propose(plan.Proposal{Put: []state.Doc{put}})
	gc := e.guestChanges(p)
	if c := find(gc.Changes, driver.AttrDisk); c == nil || gc.Risk != wire.RiskDisruptive {
		t.Fatalf("grow plan: %+v", gc)
	}
	run := e.approve(p.ID)
	if run.Status != "succeeded" || e.guestStep(run).Status != "applied" {
		t.Fatalf("grow run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if st := e.observe("web1"); st.Spec.DiskGB != 20 {
		t.Errorf("disk %d", st.Spec.DiskGB)
	}
	n := 0
	for _, c := range e.agents["web1"].Commands() {
		if c == wire.CommandGrow {
			n++
		}
	}
	if n != 1 {
		t.Errorf("grow commands: %d", n)
	}
}

// A grow needs a running guest and passes the disk gate (D-219).
func TestDay2GrowRules(t *testing.T) {
	e, id := day2Env(t, "")
	e.helloWithGrow(wire.Item{Resource: wire.GrowItemID, Status: wire.StatusApplied})
	put := doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","allow_restart":true,"guest":{"cores":2,"memory":1024,"disk":20,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"hello"}}]}`)
	e.drv.StopOutside(id)
	if _, err := e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Put: []state.Doc{put}}, Author: "user:admin"}); err == nil || !strings.Contains(err.Error(), "running guest") {
		t.Errorf("grow of a stopped guest: %v", err)
	}
	e.drv.Power(e.ctx, id, driver.PowerStart)
	e.drv.SetDiskSpace(plan.DiskBytes(100), plan.DiskBytes(95), true)
	p := e.propose(plan.Proposal{Put: []state.Doc{put}})
	if !strings.Contains(strings.Join(planNotes(t, p), " "), "disk step will fail") {
		t.Errorf("no disk note: %v", planNotes(t, p))
	}
	run := e.approve(p.ID)
	if g := e.guestStep(run); run.Status != "failed" || !strings.Contains(g.Output, "not grown") {
		t.Errorf("grow over the disk gate: %s %+v", run.Status, g)
	}
	if st := e.observe("web1"); st.Spec.DiskGB != 10 {
		t.Errorf("disk after the refusal: %d", st.Spec.DiskGB)
	}
}

// A container grows rootfs with no agent command (D-299).
func TestDay2LXCGrow(t *testing.T) {
	e, _ := newCTEnv(t)
	e.agent("c1")
	if run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, false))}).ID); run.Status != "succeeded" {
		t.Fatalf("seed %s", run.Status)
	}
	e.online("c1")
	put := doc(state.KindTarget, "c1", `{"type":"lxc","image":"ct","guest":{"cores":1,"memory":512,"disk":8,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"hi"}}]}`)
	run := e.approve(e.propose(plan.Proposal{Put: []state.Doc{put}}).ID)
	if run.Status != "succeeded" || e.observe("c1").Spec.DiskGB != 8 {
		t.Fatalf("grow run %s: %s, disk %d", run.Status, stepNames(e.steps(run)), e.observe("c1").Spec.DiskGB)
	}
	for _, c := range e.agents["c1"].Commands() {
		if c == wire.CommandGrow {
			t.Error("a container got the grow command")
		}
	}
}

func planNotes(t *testing.T, p store.Plan) []string {
	t.Helper()
	var in plan.Proposal
	if err := json.Unmarshal(p.Proposal, &in); err != nil {
		t.Fatal(err)
	}
	return in.Notes
}
