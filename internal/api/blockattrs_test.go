package api

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

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

// Review S1 of #236: a 422 never repeats a value that failed a pattern:
// a private key in ssh_keys and a password in a URL, in a proposal (the
// Block check at save and the render of a Target) and in an import.
func TestSchemaErrorNoValueEcho(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	const privKey = `-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA-secret-key-body\n-----END OPENSSH PRIVATE KEY-----`
	const credURL = `ftp://deploy:hunter2-secret-pass@repo.example.invalid/debian`
	literal := doc(state.KindBlock, "keys", `{"resources":[{"user":{"name":"deploy","ssh_keys":["`+privKey+`"]}},`+
		`{"apt_repository":{"name":"r","uris":["`+credURL+`"],"suites":["s"],"key":"x"}}]}`)
	param := doc(state.KindBlock, "keys", `{"params":{"type":"object","properties":{"k":{"type":"string"},"u":{"type":"string"}}},`+
		`"resources":[{"user":{"name":"deploy","ssh_keys":["{{ .k }}"]}},{"apt_repository":{"name":"r","uris":["{{ .u }}"],"suites":["s"],"key":"x"}}]}`)
	web := doc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},`+
		`"blocks":[{"use":"keys","params":{"k":"`+privKey+`","u":"`+credURL+`"}}]}`)
	clean := func(what, want string, code int, body []byte) {
		t.Helper()
		if code != 422 || !strings.Contains(string(body), want) ||
			strings.Contains(string(body), "secret-key-body") || strings.Contains(string(body), "hunter2-secret-pass") {
			t.Errorf("%s: %d %s", what, code, body)
		}
	}
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "put": []state.Doc{literal}})
	clean("Block at save", "must be one public key line", code, body)
	clean("Block at save", "must be an http or https URL without spaces", code, body)
	code, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "x", "put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), param, web}})
	clean("render", "must be one public key line", code, body)

	var file bytes.Buffer
	docs := []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), literal}
	if err := state.Encode(&file, state.ExportHeader{Format: state.ExportFormat, ExportedAt: time.Now(), Secrets: []string{}}, docs, nil); err != nil {
		t.Fatal(err)
	}
	code, body = adm.do("POST", "/api/v1/import", map[string]any{"content": file.String(), "reason": "x", "mode": "merge"})
	clean("import", "must be one public key line", code, body)
}
