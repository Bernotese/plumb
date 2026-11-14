package api

import (
	"strings"
	"testing"
)

// T-213: the text scan of noValue finds a short value where it is stored
// as text (a positive control: it cannot pass on an empty or wrong
// haystack). The name of an API token is plain text in its table.
func TestNoValueTextScanFindsShortValue(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	if where := e.textValuesHold([]string{"ⓩ"}); where != "" {
		t.Fatalf("the fresh database already holds the needle in %s", where)
	}
	code, body := adm.do("POST", "/api/v1/tokens", map[string]any{"name": "short ⓩ marker", "tier": "read"})
	if code != 201 {
		t.Fatalf("create token: %d %s", code, body)
	}
	if where := e.textValuesHold([]string{"ⓩ"}); !strings.HasSuffix(where, ".name") {
		t.Fatalf("the text scan did not find the token name: %q", where)
	}
	// rawScanMin splits the two values of TestSecretMaskedInAgentErrors:
	// the 3-byte one goes to the text scan, the 13-byte one stays raw.
	if len("ⓩ") >= rawScanMin || len("pw-canary-9x1") < rawScanMin {
		t.Fatal("rawScanMin does not split the two needles of TestSecretMaskedInAgentErrors")
	}
}
