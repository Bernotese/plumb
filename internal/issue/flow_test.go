package issue_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// The slice flow with the fake agent: create target → approve → apply →
// change a file → Issue opens → revert closes it. The same test with the
// real agent waits for T-018.
func TestCreateApplyDriftRevert(t *testing.T) {
	e := newEnv(t)
	a := e.agent("web1")
	p := e.propose(plan.Proposal{Put: []state.Doc{
		doc(state.KindImage, "ubuntu", image), doc(state.KindBlock, "motd", motd), target("web1", "hi", 10)}})

	// A new target: guest:config is a create, the resources are unverified.
	tp := changes(t, p)["web1"]
	if tp.Guest != "create" || tp.Resources[0].Resource != wire.GuestConfigID || tp.Resources[0].Action != wire.ActionCreate {
		t.Fatalf("plan of a new target: %+v", tp)
	}
	if r := tp.Resources[1]; r.Resource != "file:/etc/motd" || !r.Unverified || r.Risk != wire.RiskDisruptive {
		t.Errorf("resource of a new target: %+v", r)
	}
	if p.Risk != string(wire.RiskDisruptive) || p.BaseRevision != 0 {
		t.Errorf("plan risk %s base %d", p.Risk, p.BaseRevision)
	}

	checked := false
	run, err := e.eng.Approve(e.ctx, p.ID, "user:admin", func(sp store.Plan) error { checked = sp.ID == p.ID; return nil })
	if err != nil {
		t.Fatal(err)
	}
	e.eng.Wait()
	if !checked {
		t.Error("the check function was not called")
	}
	run, _ = e.st.GetRun(e.ctx, run.ID)
	steps, _ := e.st.Steps(e.ctx, run.ID)
	if run.Status != "succeeded" || len(steps) != 2 || steps[0].Resource != wire.GuestConfigID ||
		steps[0].Status != "applied" || steps[1].Status != "applied" {
		t.Fatalf("run %s, steps %+v", run.Status, steps)
	}
	rev, _ := e.st.Head(e.ctx)
	if rev.PlanID != p.ID || rev.Author != "user:admin" || rev.Reason != "test" {
		t.Errorf("revision = %+v", rev)
	}
	if got := a.Guest("file:/etc/motd"); got["content"] != "hi" {
		t.Fatalf("file on the guest: %v", got)
	}
	eventually(t, "watch command", func() bool {
		for _, c := range a.Commands() {
			if c == wire.CommandWatch {
				return true
			}
		}
		return false
	})

	// Drift: someone edits the file.
	a.Change(e.ctx, "file:/etc/motd", map[string]any{"path": "/etc/motd", "content": "hacked", "mode": "0644"})
	iss := e.openIssue("web1", "file:/etc/motd")
	// A second report updates the same Issue.
	a.Change(e.ctx, "file:/etc/motd", map[string]any{"path": "/etc/motd", "content": "hacked again", "mode": "0644"})
	eventually(t, "issue update", func() bool {
		i, _ := e.st.GetIssue(e.ctx, iss.ID)
		return strings.Contains(string(i.Changes), "hacked again")
	})
	if all, _ := e.st.Issues(e.ctx, "web1"); len(all) != 1 {
		t.Errorf("issues = %+v; want one per target and resource", all)
	}

	rp, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	if rp.Origin != plan.OriginRevert || rp.IssueID != iss.ID {
		t.Errorf("revert plan = %+v", rp)
	}
	if c := changes(t, rp)["web1"]; len(c.Resources) != 1 || c.Resources[0].Changes[0].From != "hacked again" {
		t.Errorf("revert changes: %+v", c)
	}
	if got, _ := e.st.GetIssue(e.ctx, iss.ID); got.Status != store.IssueResolving {
		t.Errorf("issue status %s", got.Status)
	}
	if run := e.approve(rp.ID); run.Status != "succeeded" {
		t.Fatalf("revert run %s", run.Status)
	}
	got, _ := e.st.GetIssue(e.ctx, iss.ID)
	if got.Status != store.IssueResolved || got.Resolution != "revert" || got.ResolvedBy != "user:admin" {
		t.Errorf("issue after revert: %+v", got)
	}
	if g := a.Guest("file:/etc/motd"); g["content"] != "hi" {
		t.Errorf("file after revert: %v", g)
	}
}

func TestAccept(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	a := e.agents["web1"]
	a.Change(e.ctx, "file:/etc/motd", map[string]any{"path": "/etc/motd", "content": "hello web1", "mode": "0600"})
	iss := e.openIssue("web1", "file:/etc/motd")
	p, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionAccept, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	var prop plan.Proposal
	json.Unmarshal(p.Proposal, &prop)
	if len(prop.Put) != 1 || !strings.Contains(string(prop.Put[0].Spec), `"overrides":{"file:/etc/motd":{"mode":"0600"}}`) {
		t.Fatalf("accept proposal: %s", prop.Put[0].Spec)
	}
	if run := e.approve(p.ID); run.Status != "succeeded" {
		t.Fatalf("accept run %s", run.Status)
	}
	if got, _ := e.st.GetIssue(e.ctx, iss.ID); got.Resolution != "accept" {
		t.Errorf("issue: %+v", got)
	}
	if g := a.Guest("file:/etc/motd"); g["mode"] != "0600" {
		t.Errorf("accept changed the guest: %v", g)
	}

	// Accept is refused for a sensitive change.
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: "file:/etc/motd",
		Status: wire.StatusDiffers, Changes: []wire.Change{{Attr: "content", Sensitive: true, Risk: wire.RiskReversible}}}}}); err != nil {
		t.Fatal(err)
	}
	iss = e.openIssue("web1", "file:/etc/motd")
	if _, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionAccept, "user:admin"); !errors.Is(err, issue.ErrAcceptSensitive) {
		t.Errorf("accept of a sensitive change: %v", err)
	}
}

func TestGoneDrift(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	a := e.agents["web1"]
	orig := a.Guest("file:/etc/motd")
	a.Change(e.ctx, "file:/etc/motd", map[string]any{"path": "/etc/motd", "content": "x"})
	iss := e.openIssue("web1", "file:/etc/motd")
	a.Change(e.ctx, "file:/etc/motd", orig)
	eventually(t, "gone", func() bool {
		got, _ := e.st.GetIssue(e.ctx, iss.ID)
		return got.Status == store.IssueResolved && got.Resolution == "gone" && got.ResolvedBy == "system"
	})
}

func TestOfflineAndCreateOnly(t *testing.T) {
	e := newEnv(t)
	e.seed("web1", "web2")
	e.stop("web2")
	e.clock.add(91 * time.Second) // web2 polls no more; web1 polls again
	e.online("web1")
	if e.link.Online("web2") {
		t.Fatal("web2 still online")
	}
	// Only web1 changes: the offline web2 does not matter.
	p := e.propose(plan.Proposal{Put: []state.Doc{target("web1", "new", 10)}})
	if c := changes(t, p); len(c) != 1 || c["web1"].Resources[0].Changes[0].To != "new" {
		t.Errorf("plan: %+v", c)
	}
	// web2 changes: refused with a clear reason.
	_, err := e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Put: []state.Doc{target("web2", "new", 10)}}, Author: "user:admin"})
	var pe *plan.ProposalError
	if !errors.As(err, &pe) || !errors.Is(err, plan.ErrOffline) || pe.Target != "web2" || !strings.Contains(err.Error(), "offline") {
		t.Errorf("offline target: %v", err)
	}
	// The disk grows only (D-299): a smaller disk is refused at save, and
	// a grow of a VM needs an agent with the command grow.
	_, err = e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Put: []state.Doc{target("web1", "hello web1", 5)}}, Author: "user:admin"})
	var se state.Errors
	if !errors.As(err, &se) || se[0].Path != "/spec/guest/disk" || !strings.Contains(err.Error(), "cannot shrink") {
		t.Errorf("shrink: %v", err)
	}
	_, err = e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Put: []state.Doc{target("web1", "hello web1", 40)}}, Author: "user:admin"})
	var ao *plan.AgentOutdatedError
	if !errors.As(err, &ao) || len(ao.Missing) != 1 || ao.Missing[0] != "command:grow" {
		t.Errorf("grow without the agent command: %v", err)
	}
	// An invalid document is refused before anything is observed.
	_, err = e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Put: []state.Doc{doc(state.KindBlock, "motd", `{"resources":[]}`)}}, Author: "user:admin"})
	if !errors.As(err, &pe) {
		t.Errorf("invalid document: %v", err)
	}
}

func TestStaleAndCheck(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	p1 := e.propose(plan.Proposal{Put: []state.Doc{target("web1", "one", 10)}})
	p2 := e.propose(plan.Proposal{Put: []state.Doc{target("web1", "two", 10)}})
	deny := errors.New("token tier too low")
	if _, err := e.eng.Approve(e.ctx, p1.ID, "token:t1", func(store.Plan) error { return deny }); !errors.Is(err, deny) {
		t.Errorf("check: %v", err)
	}
	if got, _ := e.st.GetPlan(e.ctx, p1.ID); got.Status != store.PlanPending {
		t.Errorf("a denied plan changed: %s", got.Status)
	}
	e.approve(p1.ID)
	if _, err := e.eng.Approve(e.ctx, p2.ID, "user:admin", nil); !errors.Is(err, plan.ErrStale) && !errors.Is(err, store.ErrPlanNotPending) {
		t.Errorf("stale: %v", err)
	}
	if got, _ := e.st.GetPlan(e.ctx, p2.ID); got.Status != store.PlanStale {
		t.Errorf("p2 = %s", got.Status)
	}
	if _, err := e.eng.Approve(e.ctx, p1.ID, "user:admin", nil); !errors.Is(err, store.ErrPlanNotPending) {
		t.Errorf("approve twice: %v", err)
	}
}

func TestFailedTargetDoesNotStopOthers(t *testing.T) {
	e := newEnv(t)
	e.seed("web1", "web2")
	e.agents["web1"].FailApply("file:/etc/motd", "disk full")
	p := e.propose(plan.Proposal{Put: []state.Doc{target("web1", "a", 10), target("web2", "b", 10)}})
	run := e.approve(p.ID)
	if run.Status != "failed" {
		t.Errorf("run = %s", run.Status)
	}
	steps, _ := e.st.Steps(e.ctx, run.ID)
	got := map[string]string{}
	for _, s := range steps {
		got[s.Target+" "+s.Resource] = s.Status
	}
	if got["web1 file:/etc/motd"] != "failed" || got["web2 file:/etc/motd"] != "applied" {
		t.Errorf("steps = %v", got)
	}
	if g := e.agents["web2"].Guest("file:/etc/motd"); g["content"] != "b" {
		t.Errorf("web2 not applied: %v", g)
	}
	// Every step emitted events.
	ctx, cancel := context.WithCancel(e.ctx)
	sub := e.events.Subscribe(ctx, 0)
	defer func() {
		cancel()
		for range sub.C {
		}
	}()
	kinds := map[string]bool{}
	timeout := time.After(time.Second)
loop:
	for {
		select {
		case ev := <-sub.C:
			kinds[ev.Type] = true
		case <-timeout:
			break loop
		}
	}
	for _, k := range []string{"plan.created", "plan.approved", "run.started", "run.step", "target.status", "run.finished"} {
		if !kinds[k] {
			t.Errorf("no %s event; have %v", k, kinds)
		}
	}
}

// Restart changes (D-036) are part of the Plan.
func TestRestartInPlan(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	put := doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},`+
		`"blocks":[{"use":"motd","params":{"text":"hello web1"}},{"use":"web"}]}`)
	run := e.approve(e.propose(plan.Proposal{Put: []state.Doc{put}}).ID)
	if run.Status != "succeeded" {
		t.Fatal(run.Status)
	}
	put2 := doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},`+
		`"blocks":[{"use":"motd","params":{"text":"hello web1"}},{"use":"web"}],"overrides":{"file:/etc/nginx/site":{"content":"y"}}}`)
	p := e.propose(plan.Proposal{Put: []state.Doc{put2}})
	var restart bool
	for _, r := range changes(t, p)["web1"].Resources {
		if r.Resource == "service:nginx" && r.Action == wire.ActionRestart && r.Risk == wire.RiskDisruptive {
			restart = true
		}
	}
	if !restart || p.Risk != string(wire.RiskDisruptive) {
		t.Errorf("no restart change in the plan: %s", p.Changes)
	}
}

// Re-enrollment: a target whose token expired before its first boot.
func TestReenroll(t *testing.T) {
	e := newEnv(t)
	put := []state.Doc{doc(state.KindImage, "ubuntu", image), doc(state.KindBlock, "motd", motd), doc(state.KindBlock, "web", web), target("web1", "x", 10)}
	// No agent runs: the Run fails while it waits for enrollment.
	run := e.approve(e.propose(plan.Proposal{Put: put}).ID)
	if run.Status != "failed" {
		t.Fatalf("run without agent: %s", run.Status)
	}
	e.clock.add(16 * time.Minute) // the token expired
	a := e.agent("web1")
	time.Sleep(200 * time.Millisecond)
	if a.Enrolls() != 0 {
		t.Fatal("enrolled with an expired token")
	}
	_, err := e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Put: []state.Doc{target("web1", "y", 10)}}, Author: "user:admin"})
	if !errors.Is(err, plan.ErrOffline) {
		t.Errorf("a normal plan for the stuck target: %v", err)
	}
	// The guest runs and allow_restart is false: refused (D-194). Stopped
	// in Proxmox, the re-enrollment is a Plan, and the Run starts it.
	if _, err := e.eng.Propose(e.ctx, plan.Input{Proposal: plan.Proposal{Reenroll: []string{"web1"}}, Author: "user:admin"}); err == nil ||
		!strings.Contains(err.Error(), plan.TextReenrollRefused) {
		t.Fatalf("reenroll of a running guest: %v", err)
	}
	b, _ := e.st.GetBinding(e.ctx, state.KindTarget, "web1")
	e.drv.StopOutside(driver.GuestID(b.Ref))
	p := e.propose(plan.Proposal{Reenroll: []string{"web1"}})
	tp := changes(t, p)["web1"]
	if p.Risk != string(wire.RiskDisruptive) || tp.Guest != "reenroll" {
		t.Fatalf("reenroll plan: %+v risk %s", tp, p.Risk)
	}
	for _, c := range tp.Resources[0].Changes {
		if c.Attr == driver.AttrEnrollToken && (!c.Sensitive || c.To != nil) {
			t.Errorf("token change carries a value: %+v", c)
		}
	}
	run = e.approve(p.ID)
	steps, _ := e.st.Steps(e.ctx, run.ID)
	if run.Status != "succeeded" || len(steps) != 1 || steps[0].Resource != wire.GuestConfigID {
		t.Fatalf("reenroll run %s: %+v", run.Status, steps)
	}
	// D-075: the re-enrollment applies nothing. The new agent gets the
	// watch list, and what differs becomes an Issue.
	for _, c := range a.Commands() {
		if c == wire.CommandApply {
			t.Error("a re-enrollment sent an apply")
		}
	}
	if a.Enrolls() != 1 || a.Guest("file:/etc/motd") != nil {
		t.Errorf("after reenroll: enrolls %d, file %v", a.Enrolls(), a.Guest("file:/etc/motd"))
	}
	iss := e.openIssue("web1", "file:/etc/motd")
	rp, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	if run := e.approve(rp.ID); run.Status != "succeeded" || a.Guest("file:/etc/motd")["content"] != "x" {
		t.Errorf("revert after reenroll: %s, file %v", run.Status, a.Guest("file:/etc/motd"))
	}
}

// Proxmox level: an outside change of the guest opens an Issue on
// guest:config; revert brings it back.
func TestGuestDrift(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	b, _ := e.st.GetBinding(e.ctx, state.KindTarget, "web1")
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Fatal(err)
	}
	if all, _ := e.st.Issues(e.ctx, ""); len(all) != 0 {
		t.Fatalf("drift without a change: %+v", all)
	}
	e.drv.Tamper(driver.GuestID(b.Ref), func(s *driver.GuestSpec) { s.Cores = 8 })
	calls := e.drv.Calls("Observe")
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Fatal(err)
	}
	iss, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID)
	if err != nil || !strings.Contains(string(iss.Changes), `"from":8`) {
		t.Fatalf("guest issue: %+v %v", iss, err)
	}
	// Same digest: no new Diff, no update.
	before := iss.UpdatedAt
	e.eng.CheckGuests(e.ctx)
	if again, _ := e.st.GetIssue(e.ctx, iss.ID); !again.UpdatedAt.Equal(before) || e.drv.Calls("Observe") != calls+2 {
		t.Errorf("unchanged digest made a new report")
	}
	p, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	if run := e.approve(p.ID); run.Status != "succeeded" {
		t.Fatalf("revert run %s", run.Status)
	}
	// allow_restart is false: 2 is set and waits for a cold start (D-195).
	st, _ := e.drv.Observe(e.ctx, driver.GuestID(b.Ref))
	if st.Spec.Cores != 8 || st.Next().Cores != 2 {
		t.Errorf("cores = %d, next %d", st.Spec.Cores, st.Next().Cores)
	}
	if got, _ := e.st.GetIssue(e.ctx, iss.ID); got.Status != store.IssueResolved {
		t.Errorf("guest issue: %+v", got)
	}
	// A disk grown outside Plumb is drift too, risk disruptive (D-299);
	// no Plan shrinks it back.
	e.drv.Tamper(driver.GuestID(b.Ref), func(s *driver.GuestSpec) { s.DiskGB = 50 })
	e.eng.CheckGuests(e.ctx)
	iss, _ = e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID)
	if !strings.Contains(string(iss.Changes), `"disk"`) || !strings.Contains(string(iss.Changes), `"disruptive"`) {
		t.Errorf("disk drift: %s", iss.Changes)
	}
	if _, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin"); !errors.Is(err, plan.ErrShrink) {
		t.Errorf("revert of a grown disk: %v", err)
	}
}

// No value of a sensitive attribute reaches the database or the events,
// also from a buggy agent and also after a controller restart (D-062).
func TestNoSecretStored(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	a := e.agents["web1"]
	const secret = "s3cr3t-value-XYZ"
	leak := func(resource string) {
		t.Helper()
		code := a.Report(e.ctx, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: resource, Status: wire.StatusDiffers,
			Changes: []wire.Change{{Attr: "content", From: secret + "-old", To: secret, Sensitive: true, Risk: wire.RiskReversible}}}}})
		if code != 204 {
			t.Fatalf("report: %d", code)
		}
	}
	leak("file:/etc/motd")      // agent marks it, but sends values
	leak("file:/etc/not-known") // unknown resource: every change loses its values

	// Controller restart: new link and engine on the same store.
	e.eng.Close()
	e.start()
	a.SetHandler(e.link.Handler())
	leak("file:/etc/not-known-2")
	// A known resource keeps non-sensitive values after the restart (the
	// head revision says what is known).
	a.Report(e.ctx, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: "file:/etc/motd", Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "mode", From: "0600", To: "0644", Risk: wire.RiskReversible}}}}})
	iss := e.openIssue("web1", "file:/etc/motd")
	if !strings.Contains(string(iss.Changes), `"0600"`) {
		t.Errorf("known resource over-redacted after a restart: %s", iss.Changes)
	}
	if sens, known, err := e.eng.Sensitive(e.ctx, "web1", "file:/etc/motd"); !known || err != nil || len(sens) != 0 {
		t.Errorf("Sensitive after restart: %v %v %v", sens, known, err)
	}
	if _, known, _ := e.eng.Sensitive(e.ctx, "web1", "file:/nope"); known {
		t.Error("unknown resource reported as known")
	}

	// Search every byte of the database and every event.
	for _, name := range []string{"plumb.db", "plumb.db-wal"} {
		raw, err := os.ReadFile(filepath.Join(e.dir, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secret) {
			t.Errorf("%s holds the secret", name)
		}
	}
	sub := e.events.Subscribe(context.Background(), 0)
	timeout := time.After(time.Second)
	for {
		select {
		case ev := <-sub.C:
			if strings.Contains(string(ev.Data), secret) {
				t.Errorf("event %s holds the secret", ev.Type)
			}
		case <-timeout:
			return
		}
	}
}
