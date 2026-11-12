package state

import (
	"encoding/json"
	"testing"
	"time"
)

func TestQACommandArgvSecretPositions(t *testing.T) {
	for _, argv := range []string{`["/usr/local/bin/{{ .db_password }}"]`, `["/bin/echo","--password={{ .db_password }}"]`, `["/bin/echo","{{ .db_password }}{{ .db_password }}"]`} {
		b := doc(KindBlock, "app", `{"params":{"type":"object","required":["db_password"],"properties":{"db_password":{"type":"string","writeOnly":true}}},"resources":[{"command":{"name":"qa","argv":`+argv+`,"creates":"/done"}}]}`)
		_, err := Validate([]Doc{{Kind: KindImage, Name: "ubuntu", Spec: json.RawMessage(secImage)}, b, secTarget(map[string]any{"db_password": ref("db")})})
		errAt(t, err, KindTarget, "/spec/blocks", "argv cannot hold a secret")
	}
}

func TestQACommandTimeRenderedSum(t *testing.T) {
	b := doc(KindBlock, "qa", `{"resources":[{"command":{"name":"a","argv":["/bin/true"],"creates":"/a","timeout":3600}},{"file":{"path":"/x","content":"x"}},{"command":{"name":"b","argv":["/bin/true"],"creates":"/b"}},{"command":{"name":"c","argv":["/bin/true"],"creates":"/c","timeout":1}}]}`)
	set, err := Validate(append(fixture(target(`"blocks":[{"use":"qa"}]`)), b))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got := CommandTime(rendered.Resources); got != 4201*time.Second {
		t.Fatalf("command budget = %v", got)
	}
}
