package api

import (
	"encoding/json"
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
