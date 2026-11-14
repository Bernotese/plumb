package api

import (
	"encoding/json"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
)

// TestValidationErrorsUnique (T-208, host finding F3): the override
// firewall:main {state: absent} on a Target whose firewall Block sets more
// attributes failed one schema rule once per attribute; the 422 listed the
// same error six times. Now each pointer and message is there once.
func TestValidationErrorsUnique(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	var fw state.Doc
	for _, it := range getCatalog(t, adm).Items {
		if it.Name == "firewall" {
			fw = catalogDoc(t, it)
		}
	}
	if fw.Name != "firewall" {
		t.Fatal("no firewall Block in the catalogue")
	}
	target := doc(state.KindTarget, "web1", `{"type":"lxc","image":"ct","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"},`+
		`"blocks":[{"use":"firewall","params":{"tcp_ports":"80"}}],"overrides":{"firewall:main":{"state":"absent"}}}`)
	ct := doc(state.KindImage, "ct", `{"type":"lxc","url":"http://download.invalid/ct.tar.zst","sha512":"`+zeros(128)+`"}`)
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "F3", "put": []state.Doc{ct, fw, target}})
	if code != 422 {
		t.Fatalf("want 422: %d %s", code, body)
	}
	var p struct {
		Errors []fieldError `json:"errors"`
	}
	if err := json.Unmarshal(body, &p); err != nil || len(p.Errors) == 0 {
		t.Fatalf("errors: %s", body)
	}
	seen := map[fieldError]bool{}
	for _, fe := range p.Errors {
		if seen[fe] {
			t.Fatalf("repeated error %+v in %s", fe, body)
		}
		seen[fe] = true
	}
}

func TestUniqueFieldErrors(t *testing.T) {
	a, b := fieldError{"/x", "m"}, fieldError{"/y", "m"}
	got := uniqueFieldErrors([]fieldError{a, a, b, a, b})
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("%v", got)
	}
	if uniqueFieldErrors(nil) != nil {
		t.Fatal("nil in, not nil out")
	}
}

func zeros(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '0'
	}
	return string(b)
}
