package link

import (
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// T-115 (D-299): the grow command and its Item grow:root.
func TestGrowItems(t *testing.T) {
	item := func(id string, st wire.Status) wire.Item { return wire.Item{Resource: id, Status: st} }
	result := func(items ...wire.Item) wire.Report {
		return wire.Report{Type: wire.ReportResult, CommandID: "c1", Items: items}
	}
	if err := validateReport(result(item(wire.GrowItemID, wire.StatusApplied)), false); err != nil {
		t.Errorf("result: %v", err)
	}
	if err := validateReport(result(item(wire.GrowItemID, wire.StatusApplied)), true); err == nil {
		t.Error("build agent result with grow:root passed")
	}
	if err := validateReport(wire.Report{Type: wire.ReportDrift, Items: []wire.Item{item(wire.GrowItemID, wire.StatusDiffers)}}, false); err == nil {
		t.Error("drift with grow:root passed")
	}
	for _, tc := range []struct {
		rep  wire.Report
		cmd  wire.CommandType
		pass bool
	}{
		{result(item(wire.GrowItemID, wire.StatusApplied)), wire.CommandGrow, true},
		{result(item(wire.GrowItemID, wire.StatusUnchanged)), wire.CommandGrow, true},
		{result(item(wire.GrowItemID, wire.StatusFailed)), wire.CommandGrow, true},
		{result(), wire.CommandGrow, true}, // a bad body: error, no Item
		{result(item(wire.GrowItemID, wire.StatusRefused)), wire.CommandGrow, false},
		{result(item("file:/etc/motd", wire.StatusApplied)), wire.CommandGrow, false},
		{result(item(wire.AgentBinaryID, wire.StatusApplied)), wire.CommandGrow, false},
		{result(item(wire.GrowItemID, wire.StatusApplied), item(wire.GrowItemID, wire.StatusApplied)), wire.CommandGrow, false},
		{wire.Report{Type: wire.ReportProgress, CommandID: "c1"}, wire.CommandGrow, false},
		{result(item(wire.GrowItemID, wire.StatusApplied)), wire.CommandApply, false},
		{result(item(wire.GrowItemID, wire.StatusApplied)), wire.CommandUpdate, false},
		{result(item(wire.GrowItemID, wire.StatusApplied)), wire.CommandSeal, false},
	} {
		if err := checkItemKinds(tc.rep, tc.cmd); (err == nil) != tc.pass {
			t.Errorf("%s %+v: err %v", tc.cmd, tc.rep.Items, err)
		}
	}
	c := wire.Item{Resource: wire.GrowItemID, Status: wire.StatusApplied, Changes: []wire.Change{{Attr: "size", Risk: wire.RiskDisruptive}}}
	if checkItemKinds(result(c), wire.CommandGrow) == nil {
		t.Error("grow item with changes passed")
	}
}

func TestSendGrow(t *testing.T) {
	l := newFixture(t).link
	if _, err := l.Send("web1", wire.CommandGrow, wire.GrowBody{PlanID: 1, MinBytes: 1 << 30}); err != nil {
		t.Errorf("grow: %v", err)
	}
	if _, err := l.Send(wire.BuildLinkPrefix+"img", wire.CommandGrow, wire.GrowBody{}); err == nil {
		t.Error("grow to a build guest passed")
	}
	if _, err := l.Send("web1", wire.CommandGrow, wire.WatchBody{}); err == nil {
		t.Error("grow with another body passed")
	}
}
