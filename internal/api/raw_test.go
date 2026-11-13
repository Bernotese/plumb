package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// Raw mode (D-296, D-301): Issue.source and Issue.raw name the Block use;
// Accept writes an override that TargetView.raw lists by attribute name.
func TestRawMode(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	rc := &recorder{t: t}
	reader := e.token(TierRead, "")

	// No override yet: raw is [].
	_, body := rc.do(reader, "GET", "/api/v1/targets/web1", "/targets/{name}", nil)
	var tv struct {
		Raw *[]rawView `json:"raw"`
	}
	json.Unmarshal(body, &tv)
	if tv.Raw == nil || len(*tv.Raw) != 0 {
		t.Fatalf("raw before: %s", body)
	}

	e.motd("hand edit")
	id := e.openIssueID()
	if id == 0 {
		t.Fatal("no issue")
	}
	var iv struct {
		Raw    bool        `json:"raw"`
		Source *sourceView `json:"source"`
	}
	for _, c := range []*client{adm, reader} {
		code, body := rc.do(c, "GET", "/api/v1/issues/"+itoa(id), "/issues/{id}", nil)
		json.Unmarshal(body, &iv)
		if code != 200 || !iv.Raw || iv.Source == nil || *iv.Source != (sourceView{Block: "motd", Use: "motd"}) {
			t.Fatalf("issue: %d %s", code, body)
		}
	}
	code, body := rc.do(reader, "GET", "/api/v1/issues?target=web1", "/issues", nil)
	if code != 200 || !strings.Contains(string(body), `"raw":true,"source":{"block":"motd","use":"motd"}`) {
		t.Errorf("issue list: %d %s", code, body)
	}

	// Accept as raw: a normal Plan writes spec.overrides.
	code, body = rc.do(adm, "POST", "/api/v1/issues/"+itoa(id)+"/resolve", "/issues/{id}/resolve", resolveBody(issue.ActionAccept))
	var res struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(body, &res)
	if code != 201 || res.ID == 0 {
		t.Fatalf("accept: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(res.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	for _, c := range []*client{adm, reader} {
		_, body = rc.do(c, "GET", "/api/v1/targets/web1", "/targets/{name}", nil)
		tv.Raw = nil
		json.Unmarshal(body, &tv)
		if tv.Raw == nil || len(*tv.Raw) != 1 {
			t.Fatalf("raw after accept: %s", body)
		}
		r := (*tv.Raw)[0]
		if r.Resource != "file:/etc/motd" || r.Source == nil || *r.Source != (sourceView{Block: "motd", Use: "motd"}) ||
			!strings.Contains(strings.Join(r.Attrs, ","), "content") {
			t.Errorf("raw entry: %+v", r)
		}
		// Names only: the overridden value is in the document, never in raw.
		rawJSON, _ := json.Marshal(tv.Raw)
		if strings.Contains(string(rawJSON), "hand edit") {
			t.Errorf("raw holds a value: %s", rawJSON)
		}
	}
	// The list of Targets has raw too; other documents do not.
	_, body = rc.do(reader, "GET", "/api/v1/targets", "/targets", nil)
	if !strings.Contains(string(body), `"raw":[{"resource":"file:/etc/motd"`) {
		t.Errorf("target list: %s", body)
	}
	_, body = rc.do(reader, "GET", "/api/v1/blocks/motd", "/blocks/{name}", nil)
	if strings.Contains(string(body), `"raw"`) {
		t.Errorf("block view has raw: %s", body)
	}
	rc.check()
}

func TestToSourceView(t *testing.T) {
	src := map[string]state.Source{"file:/x": {Block: "b", Use: "u"}}
	if v := toSourceView(src, "file:/x"); v == nil || *v != (sourceView{Block: "b", Use: "u"}) {
		t.Errorf("file: %+v", v)
	}
	for _, id := range []string{wire.GuestConfigID, wire.GuestAddressID, wire.AgentBinaryID, "file:/unknown"} {
		if v := toSourceView(src, id); v != nil {
			t.Errorf("%s: %+v", id, v)
		}
	}
}

// Accept of a firewall Issue, also "accept as raw", can never adopt
// allow_no_ssh or drop the SSH rule from guest drift (D-300, review L2 of
// #219). The Issue comes from a report that a compromised agent could
// forge: the agent of T-116 never reports these attributes.
func TestRawAcceptFirewallSSH(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	rc := &recorder{t: t}
	before, _ := e.st.Head(e.ctx)
	for _, c := range []wire.Change{
		{Attr: "allow_no_ssh", From: true, To: false, Risk: wire.RiskDisruptive},
		{Attr: "ssh_port", From: "2222", To: "22", Risk: wire.RiskDisruptive},
		{Attr: "ssh_sources", From: "192.0.2.1/32", To: "", Risk: wire.RiskDisruptive},
		{Attr: "ssh", From: "closed (allow_no_ssh)", To: "tcp/22 from everywhere", Risk: wire.RiskDisruptive},
		{Attr: "table", From: "changed outside Plumb", To: "as Plumb wrote it", Risk: wire.RiskDisruptive},
	} {
		raw, _ := json.Marshal([]wire.Change{{Attr: "policy", From: "accept", To: "drop", Risk: wire.RiskDisruptive}, c})
		iss, _, err := e.st.UpsertIssue(e.ctx, "web1", "firewall:main", raw)
		if err != nil {
			t.Fatal(err)
		}
		_, body := rc.do(adm, "GET", "/api/v1/issues/"+itoa(iss.ID), "/issues/{id}", nil)
		var iv struct {
			Actions []string `json:"actions"`
			Raw     bool     `json:"raw"`
		}
		json.Unmarshal(body, &iv)
		if slices.Contains(iv.Actions, "accept") || iv.Raw {
			t.Errorf("%s: accept offered: %s", c.Attr, body)
		}
		code, body := rc.do(adm, "POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", "/issues/{id}/resolve", resolveBody(issue.ActionAccept))
		if code != 409 || problemType(body) != "action-not-allowed" {
			t.Errorf("%s: accept: %d %s", c.Attr, code, body)
		}
		if head, _ := e.st.Head(e.ctx); head.ID != before.ID {
			t.Fatalf("%s: the accept changed the document", c.Attr)
		}
		// Clean up for the next case: the Issue goes.
		if err := e.st.ResolveIssue(e.ctx, iss.ID, "gone", "system"); err != nil {
			t.Fatal(err)
		}
	}
	// A firewall change without the SSH rule can be accepted.
	raw, _ := json.Marshal([]wire.Change{{Attr: "tcp_ports", From: "80", To: "80,443", Risk: wire.RiskDisruptive}})
	iss, _, _ := e.st.UpsertIssue(e.ctx, "web1", "firewall:main", raw)
	_, body := rc.do(adm, "GET", "/api/v1/issues/"+itoa(iss.ID), "/issues/{id}", nil)
	if !strings.Contains(string(body), `"actions":["accept"`) {
		t.Errorf("tcp_ports: %s", body)
	}
	rc.check()
}
