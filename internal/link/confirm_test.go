package link

import (
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// The round trip of a safe apply (D-300): 204 only for the open apply of
// this agent with the resource in it; nothing reaches the handler.
func TestConfirm(t *testing.T) {
	f := newFixture(t)
	cred := f.agent("web1")
	other := f.agent("web2")
	fw := wire.Resource{ID: "firewall:main", Type: "firewall", Block: "fw", Attrs: map[string]any{"name": "main"}}
	file := wire.Resource{ID: "file:/x", Type: "file", Block: "fw", Attrs: map[string]any{"path": "/x"}}
	apply, _ := f.link.Send("web1", wire.CommandApply, wire.ApplyBody{PlanID: 1, MaxRisk: wire.RiskDisruptive, Resources: []wire.Resource{file, fw}})
	noFw, _ := f.link.Send("web1", wire.CommandApply, wire.ApplyBody{PlanID: 1, MaxRisk: wire.RiskDisruptive, Resources: []wire.Resource{file}})
	observe, _ := f.link.Send("web1", wire.CommandObserve, wire.ObserveBody{Resources: []wire.Resource{fw}})
	confirm := func(cmd string) wire.Report {
		return wire.Report{Type: wire.ReportConfirm, CommandID: cmd, Items: []wire.Item{{Resource: "firewall:main", Status: wire.StatusRunning}}}
	}
	// Not taken yet: not open.
	if code, _ := f.do("POST", wire.PathReport, cred, confirm(apply)); code != 409 {
		t.Fatalf("queued apply: %d, want 409", code)
	}
	for i := 0; i < 3; i++ {
		f.do("GET", wire.PathPoll, cred, nil)
	}
	if code, _ := f.do("POST", wire.PathReport, cred, confirm(apply)); code != 204 {
		t.Fatalf("open apply: %d, want 204", code)
	}
	for name, c := range map[string]struct {
		cred string
		rep  wire.Report
		want int
	}{
		"apply without firewall": {cred, confirm(noFw), 409},
		"observe":                {cred, confirm(observe), 409},
		"unknown command":        {cred, confirm("nope"), 409},
		"other agent":            {other, confirm(apply), 409},
		"no command":             {cred, confirm(""), 400},
		"file item": {cred, wire.Report{Type: wire.ReportConfirm, CommandID: apply,
			Items: []wire.Item{{Resource: "file:/x", Status: wire.StatusRunning}}}, 400},
		"applied item": {cred, wire.Report{Type: wire.ReportConfirm, CommandID: apply,
			Items: []wire.Item{{Resource: "firewall:main", Status: wire.StatusApplied}}}, 400},
		"two items": {cred, wire.Report{Type: wire.ReportConfirm, CommandID: apply,
			Items: []wire.Item{{Resource: "firewall:main", Status: wire.StatusRunning}, {Resource: "firewall:main", Status: wire.StatusRunning}}}, 400},
		"with error": {cred, wire.Report{Type: wire.ReportConfirm, CommandID: apply, Error: "x",
			Items: []wire.Item{{Resource: "firewall:main", Status: wire.StatusRunning}}}, 400},
	} {
		if code, _ := f.do("POST", wire.PathReport, c.cred, c.rep); code != c.want {
			t.Errorf("%s: %d, want %d", name, code, c.want)
		}
	}
	if len(f.got) != 0 {
		t.Fatalf("a confirm reached the handler: %+v", f.got)
	}
	// rolled_back only on a failed safe-apply item.
	for name, it := range map[string]wire.Item{
		"applied": {Resource: "firewall:main", Status: wire.StatusApplied, RolledBack: true},
		"file":    {Resource: "file:/x", Status: wire.StatusFailed, RolledBack: true},
	} {
		rep := wire.Report{Type: wire.ReportProgress, CommandID: apply, Items: []wire.Item{it}}
		if code, _ := f.do("POST", wire.PathReport, cred, rep); code != 400 {
			t.Errorf("rolled_back on %s: %d, want 400", name, code)
		}
	}
	res := wire.Report{Type: wire.ReportResult, CommandID: apply, Items: []wire.Item{
		{Resource: "firewall:main", Status: wire.StatusFailed, RolledBack: true, Output: "no round trip"}}}
	if code, _ := f.do("POST", wire.PathReport, cred, res); code != 204 {
		t.Fatalf("result: %d", code)
	}
	if len(f.got) != 1 || !f.got[0].Report.Items[0].RolledBack {
		t.Fatalf("delivered: %+v", f.got)
	}
	// After the result the apply is not open any more.
	if code, _ := f.do("POST", wire.PathReport, cred, confirm(apply)); code != 409 {
		t.Errorf("after the result: %d, want 409", code)
	}
}

// An apply queued before the agent enrolls keeps its safe list.
func TestConfirmQueuedBeforeEnroll(t *testing.T) {
	f := newFixture(t)
	fw := wire.Resource{ID: "firewall:main", Type: "firewall", Block: "fw", Attrs: map[string]any{"name": "main"}}
	tok := f.token("web1")
	apply, _ := f.link.Send("web1", wire.CommandApply, wire.ApplyBody{PlanID: 1, MaxRisk: wire.RiskDisruptive, Resources: []wire.Resource{fw}})
	code, resp, body := f.enroll(tok)
	if code != 200 {
		t.Fatalf("enroll: %d %s", code, body)
	}
	f.do("GET", wire.PathPoll, resp.Credential, nil)
	rep := wire.Report{Type: wire.ReportConfirm, CommandID: apply, Items: []wire.Item{{Resource: "firewall:main", Status: wire.StatusRunning}}}
	if code, _ := f.do("POST", wire.PathReport, resp.Credential, rep); code != 204 {
		t.Fatalf("confirm: %d, want 204", code)
	}
}
