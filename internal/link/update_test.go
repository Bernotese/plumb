package link

import (
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// T-092: the update command and its Item agent:binary.
func TestUpdateItems(t *testing.T) {
	item := func(id string, st wire.Status) wire.Item { return wire.Item{Resource: id, Status: st} }
	result := func(items ...wire.Item) wire.Report {
		return wire.Report{Type: wire.ReportResult, CommandID: "c1", Items: items}
	}
	// The form check takes agent:binary only in a result of a normal agent.
	if err := validateReport(result(item(wire.AgentBinaryID, wire.StatusApplied)), false); err != nil {
		t.Errorf("result: %v", err)
	}
	if err := validateReport(result(item(wire.AgentBinaryID, wire.StatusApplied)), true); err == nil {
		t.Error("build agent result with agent:binary passed")
	}
	if err := validateReport(wire.Report{Type: wire.ReportDrift, Items: []wire.Item{item(wire.AgentBinaryID, wire.StatusDiffers)}}, false); err == nil {
		t.Error("drift with agent:binary passed")
	}
	for _, tc := range []struct {
		rep  wire.Report
		cmd  wire.CommandType
		pass bool
	}{
		{result(item(wire.AgentBinaryID, wire.StatusApplied)), wire.CommandUpdate, true},
		{result(item(wire.AgentBinaryID, wire.StatusUnchanged)), wire.CommandUpdate, true},
		{result(item(wire.AgentBinaryID, wire.StatusFailed)), wire.CommandUpdate, true},
		{result(), wire.CommandUpdate, true}, // a bad body: error, no Item
		{result(item(wire.AgentBinaryID, wire.StatusRefused)), wire.CommandUpdate, false},
		{result(item("file:/etc/motd", wire.StatusApplied)), wire.CommandUpdate, false},
		{result(item(wire.AgentBinaryID, wire.StatusApplied), item(wire.AgentBinaryID, wire.StatusApplied)), wire.CommandUpdate, false},
		{wire.Report{Type: wire.ReportProgress, CommandID: "c1"}, wire.CommandUpdate, false},
		{result(item(wire.AgentBinaryID, wire.StatusApplied)), wire.CommandApply, false},
		{result(item(wire.AgentBinaryID, wire.StatusApplied)), wire.CommandSeal, false},
	} {
		if err := checkItemKinds(tc.rep, tc.cmd); (err == nil) != tc.pass {
			t.Errorf("%s %+v: err %v", tc.cmd, tc.rep.Items, err)
		}
	}
	c := wire.Item{Resource: wire.AgentBinaryID, Status: wire.StatusApplied, Changes: []wire.Change{{Attr: "version", Risk: wire.RiskDisruptive}}}
	if checkItemKinds(result(c), wire.CommandUpdate) == nil {
		t.Error("update item with changes passed")
	}
}

func TestSendUpdate(t *testing.T) {
	l := newFixture(t).link
	if _, err := l.Send("web1", wire.CommandUpdate, wire.UpdateBody{PlanID: 1, SHA256: "x"}); err != nil {
		t.Errorf("update: %v", err)
	}
	if _, err := l.Send(wire.BuildLinkPrefix+"img", wire.CommandUpdate, wire.UpdateBody{}); err == nil {
		t.Error("update to a build guest passed")
	}
	if _, err := l.Send("web1", wire.CommandUpdate, wire.WatchBody{}); err == nil {
		t.Error("update with another body passed")
	}
}
