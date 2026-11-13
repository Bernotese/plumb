package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// T-101: a Block that no Target uses is checked at save. The field error
// points into the Block of the request (D-265: /put/<i>/spec/...).
func TestBlockAttrsCheckedAtSave(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	bad := doc(state.KindBlock, "unused", `{"resources":[{"file":{"path":"/etc/x","mode":640}}]}`)
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "put": []state.Doc{doc(state.KindBlock, "fine", motdSpec), bad}})
	var p struct {
		Type   string `json:"type"`
		Errors []struct{ Pointer, Message string }
	}
	_ = json.Unmarshal(body, &p)
	if code != 422 || p.Type != "urn:plumb:problem:validation" || len(p.Errors) != 1 ||
		p.Errors[0].Pointer != "/put/1/spec/resources/0/file/mode" || !strings.Contains(p.Errors[0].Message, `Block "unused"`) {
		t.Fatalf("bad Block: %d %s", code, body)
	}

	// A stored Block with the same mistake (from before T-101) does not
	// block a proposal that leaves it alone; a change of it is checked.
	set, err := state.Validate([]state.Doc{bad})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := state.Rows(set)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.CommitRevision(e.ctx, store.NewRevision{Author: "user:admin", Reason: "old", Docs: rows}); err != nil {
		t.Fatal(err)
	}
	if code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "put": []state.Doc{doc(state.KindBlock, "fine", motdSpec)}}); code != 201 {
		t.Fatalf("a proposal next to a stored bad Block: %d %s", code, body)
	}
	worse := doc(state.KindBlock, "unused", `{"resources":[{"file":{"path":"/etc/y","mode":640}}]}`)
	if code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "put": []state.Doc{worse}}); code != 422 ||
		!strings.Contains(string(body), "/put/0/spec/resources/0/file/mode") {
		t.Fatalf("a change of the stored bad Block: %d %s", code, body)
	}
}

// T-134 F9: the 422 of a firewall with allow_no_ssh and ssh_port names
// the attributes and says the rule.
func TestFirewallNoSSHRuleMessage(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	fw := doc(state.KindBlock, "fw", `{"resources":[{"firewall":{"name":"main","policy":"drop","allow_no_ssh":true,"ssh_port":22}}]}`)
	web := doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"fw"}]}`)
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), fw, web}})
	if code != 422 || !strings.Contains(string(body), "allow_no_ssh is true: ssh_port and ssh_sources must then be missing") ||
		strings.Contains(string(body), "'not' failed") {
		t.Fatalf("firewall without SSH and with ssh_port: %d %s", code, body)
	}
}
