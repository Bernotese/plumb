package api

import (
	"slices"
	"testing"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-166 (host finding F2 of T-158, D-359): a rule added by hand in the
// guest shows only as "table changed outside Plumb", with no value. The
// whole type then counts as guest data: a Plan that adds any firewall
// value on any Target is human-only. An old agent reports the same text.

func (e *env) opaqueDrift(target, resource, attr string) int64 {
	e.t.Helper()
	if err := e.iss.Intake(e.ctx, target, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: resource, Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: attr, From: "changed outside Plumb", To: "as Plumb wrote it", Risk: wire.RiskDisruptive}}}}}); err != nil {
		e.t.Fatal(err)
	}
	iss, err := e.st.OpenIssue(e.ctx, target, resource)
	if err != nil {
		e.t.Fatal(err)
	}
	return iss.ID
}

func fwTarget(target, attrs string) []state.Doc {
	return []state.Doc{doc(state.KindBlock, "fw-"+target, `{"resources":[{"firewall":{"name":"main",`+attrs+`}}]}`),
		doc(state.KindTarget, target, `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"fw-`+target+`"}]}`)}
}

func hasMatch(p guestPlan, issue int64, target, attr, element string) bool {
	return slices.Contains(p.Matches, plan.GuestDataMatch{IssueID: issue, Target: target, Resource: "firewall:main", Attr: attr, Element: element})
}

func TestCopyRuleFirewallHandRule(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	// Before any hand change: a firewall Plan of a token is normal.
	p := e.proposeDocs(e.token(TierPlan, ""), fwTarget("web2", `"tcp_ports":"80"`)...)
	if len(p.HumanOnly) != 0 {
		t.Fatalf("no hand change yet: %+v", p)
	}
	if code, body := adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil); code >= 300 {
		t.Fatalf("drop: %d %s", code, body)
	}

	// The F2 case: tcp 8080 by hand on web1, reported as table only.
	id := e.opaqueDrift("web1", "firewall:main", "table")
	// The apply token ignores it once (as on the host); it still counts.
	if code, body := e.token(TierApply, wire.RiskDisruptive).do("POST", "/api/v1/issues/"+itoa(id)+"/resolve", resolveBody("ignore_once")); code != 200 {
		t.Fatalf("ignore once: %d %s", code, body)
	}
	for _, c := range []struct{ target, attrs, attr, el string }{
		{"web1", `"tcp_ports":"80,8080"`, "tcp_ports", "8080"},              // same Target
		{"web3", `"tcp_ports":"80,8080"`, "tcp_ports", "8080"},              // another Target
		{"web3", `"udp_ports":"5353"`, "udp_ports", "5353"},                 // UDP
		{"web3", `"sources":"203.0.113.0/24"`, "sources", "203.0.113.0/24"}, // a source network
		{"web3", `"ssh_sources":"198.51.100.7/32"`, "ssh_sources", "198.51.100.7/32"},
	} {
		docs := fwTarget(c.target, c.attrs)
		if c.target == "web1" {
			docs = []state.Doc{docs[0], doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"hello"}},{"use":"fw-web1"}]}`)}
		}
		p := e.proposeDocs(e.token(TierPlan, ""), docs...)
		if !slices.Contains(p.HumanOnly, plan.HumanOnlyAcceptGuestData) || !slices.Contains(p.Issues, id) || !hasMatch(p, id, c.target, c.attr, c.el) {
			t.Fatalf("%s %s: %+v", c.target, c.attrs, p)
		}
		e.tokenRefused(p.ID)
		if code, body := adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil); code >= 300 {
			t.Fatalf("drop: %d %s", code, body)
		}
	}
}

// A hand edit of the netplan file of a network resource is opaque too; it
// marks network values, not firewall values.
func TestCopyRuleNetworkFileOpaque(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	id := e.opaqueDrift("web1", "network:extra", "file")
	net := []state.Doc{doc(state.KindBlock, "net", `{"resources":[{"network":{"name":"lan2","addresses":"192.0.2.50/24"}}]}`),
		doc(state.KindTarget, "web3", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"net"}]}`)}
	p := e.proposeDocs(e.token(TierPlan, ""), net...)
	want := plan.GuestDataMatch{IssueID: id, Target: "web3", Resource: "network:lan2", Attr: "addresses", Element: "192.0.2.50/24"}
	if !slices.Contains(p.HumanOnly, plan.HumanOnlyAcceptGuestData) || !slices.Contains(p.Matches, want) {
		t.Fatalf("network copy: %+v", p)
	}
	for _, m := range p.Matches {
		if m.Attr == "name" {
			t.Fatalf("the key matched: %+v", m)
		}
	}
	if code, body := adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil); code >= 300 {
		t.Fatalf("drop: %d %s", code, body)
	}
	if q := e.proposeDocs(e.token(TierPlan, ""), fwTarget("web4", `"tcp_ports":"443"`)...); len(q.HumanOnly) != 0 {
		t.Fatalf("a network change marked firewall values: %+v", q)
	}
}

// Only additions count: a Plan that removes a port after a hand change is
// not human-only.
func TestCopyRuleOpaqueRemovalIsFree(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.agent("web3")
	e.runPlanAnyStatus(adm, map[string]any{"reason": "fw", "put": fwTarget("web3", `"tcp_ports":"80,443"`)})
	e.opaqueDrift("web1", "firewall:main", "table")
	if p := e.proposeDocs(e.token(TierPlan, ""), fwTarget("web3", `"tcp_ports":"80"`)...); len(p.HumanOnly) != 0 {
		t.Fatalf("a removal is human-only: %+v", p)
	}
}
