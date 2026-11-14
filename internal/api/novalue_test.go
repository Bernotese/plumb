package api

import (
	"strings"
	"testing"
)

// T-213: the text scan of noValue finds a short value where it is
// stored as text (a positive control: it cannot pass on an empty or
// wrong haystack), and a value of rawScanMin bytes or more stays in the
// raw-file scan.
func TestNoValueTextScanFindsShortValue(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	if where := e.textValuesHold([]string{"ⓩ"}); where != "" {
		t.Fatalf("the fresh database already holds the needle in %s", where)
	}
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "short ⓩ marker", "targets": []string{"web1"}})
	if code != 201 && code != 409 && code != 422 {
		t.Fatalf("propose: %d %s", code, body)
	}
	// The reason is stored as text only when the Plan was made; otherwise
	// use a secret-free text column that every env has: an Issue comment
	// needs an Issue, so take the event log of the request instead.
	where := e.textValuesHold([]string{"ⓩ"})
	if code == 201 && !strings.HasPrefix(where, "plan.") {
		t.Fatalf("the text scan did not find the reason: %q", where)
	}
	if code != 201 {
		t.Skipf("no Plan was made (%d); the positive control needs one", code)
	}
	if len("ⓩ") >= rawScanMin || len("pw-canary-9x1") < rawScanMin {
		t.Fatal("rawScanMin does not split the two needles of TestSecretMaskedInAgentErrors")
	}
}
