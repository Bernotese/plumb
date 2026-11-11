package link

import (
	"encoding/json"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// D-207, controller guard: seal goes only to the link of a build guest.
func TestSendSealOnlyToBuildLink(t *testing.T) {
	f := newFixture(t)
	for _, target := range []string{"web1", "build", "build:", "Build:web", "xbuild:web", ""} {
		if _, err := f.link.Send(target, wire.CommandSeal, wire.SealBody{PlanID: 1}); err == nil {
			t.Errorf("seal to %q was queued", target)
		}
	}
	if _, err := f.link.Send("build:debian", wire.CommandSeal, wire.ObserveBody{}); err == nil {
		t.Error("seal with an observe body was queued")
	}
	if _, err := f.link.Send("build:debian", wire.CommandSeal, map[string]any{"resources": []any{}}); err == nil {
		t.Error("seal with a free body was queued")
	}
	for _, body := range []any{wire.SealBody{PlanID: 1}, &wire.SealBody{PlanID: 2, Upgraded: true}} {
		if _, err := f.link.Send("build:debian", wire.CommandSeal, body); err != nil {
			t.Errorf("seal to build:debian: %v", err)
		}
	}
}

// pollCmd polls once and returns the command.
func (f *fixture) pollCmd(cred string) wire.Command {
	f.t.Helper()
	code, body := f.do("GET", wire.PathPoll, cred, nil)
	var cmd wire.Command
	if code != 200 || json.Unmarshal(body, &cmd) != nil {
		f.t.Fatalf("poll: %d %s", code, body)
	}
	return cmd
}

// Review note of #119: a seal result holds seal steps only; no other
// command may hold one.
func TestSealResultItems(t *testing.T) {
	f := newFixture(t)
	cred := f.agent("build:debian")
	step := func(s string, st wire.Status) wire.Item { return wire.Item{Resource: wire.SealItemID(s), Status: st} }

	send := func() string {
		id, err := f.link.Send("build:debian", wire.CommandSeal, wire.SealBody{PlanID: 1})
		if err != nil {
			t.Fatal(err)
		}
		if got := f.pollCmd(cred); got.ID != id || got.Type != wire.CommandSeal {
			t.Fatalf("polled %+v", got)
		}
		return id
	}
	id := send()
	bad := []struct {
		name string
		rep  wire.Report
	}{
		{"progress", wire.Report{Type: wire.ReportProgress, CommandID: id, Items: []wire.Item{step(wire.SealStepGuard, wire.StatusApplied)}}},
		{"resource item", wire.Report{Type: wire.ReportResult, CommandID: id, Items: []wire.Item{{Resource: "file:/etc/x", Status: wire.StatusApplied}}}},
		{"unknown step", wire.Report{Type: wire.ReportResult, CommandID: id, Items: []wire.Item{step("reboot", wire.StatusApplied)}}},
		{"step twice", wire.Report{Type: wire.ReportResult, CommandID: id, Items: []wire.Item{step(wire.SealStepTrim, wire.StatusApplied), step(wire.SealStepTrim, wire.StatusApplied)}}},
		{"differs", wire.Report{Type: wire.ReportResult, CommandID: id, Items: []wire.Item{step(wire.SealStepTrim, wire.StatusDiffers)}}},
		{"changes", wire.Report{Type: wire.ReportResult, CommandID: id, Items: []wire.Item{{Resource: wire.SealItemID(wire.SealStepLogs), Status: wire.StatusApplied,
			Changes: []wire.Change{{Attr: "x", Risk: wire.RiskReversible}}}}}},
	}
	for _, b := range bad {
		if code, body := f.do("POST", wire.PathReport, cred, b.rep); code != 400 {
			t.Errorf("%s: %d %s, want 400", b.name, code, body)
		}
	}
	var good []wire.Item
	for _, s := range wire.SealSteps {
		good = append(good, step(s, wire.StatusApplied))
	}
	if code, body := f.do("POST", wire.PathReport, cred, wire.Report{Type: wire.ReportResult, CommandID: id, Items: good}); code != 204 {
		t.Fatalf("seal result: %d %s", code, body)
	}
	if got := f.last(); len(got.Items) != len(wire.SealSteps) {
		t.Fatalf("delivered %+v", got)
	}
	// A refusal is one guard item.
	id = send()
	if code, body := f.do("POST", wire.PathReport, cred, wire.Report{Type: wire.ReportResult, CommandID: id,
		Items: []wire.Item{step(wire.SealStepGuard, wire.StatusRefused)}}); code != 204 {
		t.Fatalf("refusal: %d %s", code, body)
	}

	// A seal step in the answer of another command, and in drift.
	web := f.agent("web1")
	for _, typ := range []wire.CommandType{wire.CommandApply, wire.CommandObserve} {
		aid, _ := f.link.Send("web1", typ, wire.ApplyBody{})
		f.pollCmd(web)
		for _, rt := range []wire.ReportType{wire.ReportProgress, wire.ReportResult} {
			if code, _ := f.do("POST", wire.PathReport, web, wire.Report{Type: rt, CommandID: aid,
				Items: []wire.Item{step(wire.SealStepGuard, wire.StatusApplied)}}); code != 400 {
				t.Errorf("%s %s with a seal step: %d, want 400", typ, rt, code)
			}
		}
	}
	if code, _ := f.do("POST", wire.PathReport, web, wire.Report{Type: wire.ReportDrift,
		Items: []wire.Item{step(wire.SealStepGuard, wire.StatusDiffers)}}); code != 400 {
		t.Errorf("drift with a seal step: %d, want 400", code)
	}
}

// Review S1 of #134: the merged link rules of #133 and #134 together.
func TestSealLinkRulesCombined(t *testing.T) {
	f := newFixture(t)
	// #133: the build mark comes from the link prefix only.
	enroll := func(target string) (string, bool) {
		code, resp, body := f.enroll(f.token(target))
		if code != 200 {
			t.Fatalf("enroll %s: %d %s", target, code, body)
		}
		return resp.Credential, resp.Build
	}
	web, webBuild := enroll("web1")
	build, buildBuild := enroll("build:debian")
	if webBuild || !buildBuild {
		t.Fatalf("build mark: web1=%v build:debian=%v", webBuild, buildBuild)
	}
	// #133: no watch to a build link; #134: seal only to a non-empty build
	// link with a SealBody.
	if _, err := f.link.Send("build:debian", wire.CommandWatch, wire.WatchBody{}); err == nil {
		t.Error("watch to a build link was queued")
	}
	if _, err := f.link.Send("web1", wire.CommandWatch, wire.WatchBody{}); err != nil {
		t.Errorf("watch to web1: %v", err)
	}
	f.pollCmd(web)
	for _, c := range []struct {
		target string
		body   any
	}{{"web1", wire.SealBody{}}, {"build:", wire.SealBody{}}, {"build:debian", wire.ObserveBody{}}} {
		if _, err := f.link.Send(c.target, wire.CommandSeal, c.body); err == nil {
			t.Errorf("seal to %q with %T was queued", c.target, c.body)
		}
	}
	step := func(s string) wire.Item { return wire.Item{Resource: wire.SealItemID(s), Status: wire.StatusApplied} }
	post := func(cred string, rep wire.Report) int {
		code, _ := f.do("POST", wire.PathReport, cred, rep)
		return code
	}
	// #134: seal items only in the result of an outstanding seal command,
	// also for a build agent; #133: only from a build agent.
	aid, _ := f.link.Send("build:debian", wire.CommandApply, wire.ApplyBody{})
	f.pollCmd(build)
	if code := post(build, wire.Report{Type: wire.ReportResult, CommandID: aid, Items: []wire.Item{step(wire.SealStepGuard)}}); code != 400 {
		t.Errorf("seal item in the apply result of a build agent: %d", code)
	}
	if code := post(build, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{step(wire.SealStepGuard)}}); code != 400 {
		t.Errorf("seal item in drift of a build agent: %d", code)
	}
	wid, _ := f.link.Send("web1", wire.CommandApply, wire.ApplyBody{})
	f.pollCmd(web)
	if code := post(web, wire.Report{Type: wire.ReportResult, CommandID: wid, Items: []wire.Item{step(wire.SealStepGuard)}}); code != 400 {
		t.Errorf("seal item from a Target agent: %d", code)
	}
	sid, err := f.link.Send("build:debian", wire.CommandSeal, wire.SealBody{PlanID: 3})
	if err != nil {
		t.Fatal(err)
	}
	f.pollCmd(build)
	if code := post(build, wire.Report{Type: wire.ReportProgress, CommandID: sid, Items: []wire.Item{step(wire.SealStepGuard)}}); code != 400 {
		t.Errorf("seal progress: %d", code)
	}
	var items []wire.Item
	for _, s := range wire.SealSteps {
		items = append(items, step(s))
	}
	if code := post(build, wire.Report{Type: wire.ReportResult, CommandID: sid, Items: items}); code != 204 {
		t.Fatalf("seal result of the build agent: %d", code)
	}
	if got := f.last(); got.CommandID != sid || len(got.Items) != len(wire.SealSteps) {
		t.Fatalf("delivered %+v", got)
	}
}
