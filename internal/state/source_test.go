package state

import (
	"reflect"
	"testing"
)

// Sources and Overrides of raw mode (D-301).
func TestSourcesAndOverrides(t *testing.T) {
	set, err := Validate([]Doc{
		doc(KindImage, "ubuntu", image),
		doc(KindBlock, "motd", motdB),
		doc(KindTarget, "web1", `{"type":"vm","image":"ubuntu",`+guest+`,"blocks":[`+
			`{"use":"motd"},{"use":"motd","id":"issue","params":{"path":"/etc/issue"}}],`+
			`"overrides":{"file:/etc/issue":{"mode":"0600","content":"x"},"guest:config":{"cores":2}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	src, err := set.Sources("web1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Source{"file:/etc/motd": {Block: "motd", Use: "motd"}, "file:/etc/issue": {Block: "motd", Use: "issue"}}
	if !reflect.DeepEqual(src, want) {
		t.Errorf("sources: %+v", src)
	}
	ov := set.Overrides("web1")
	wantOv := []Override{{Resource: "file:/etc/issue", Attrs: []string{"content", "mode"}}, {Resource: "guest:config", Attrs: []string{"cores"}}}
	if !reflect.DeepEqual(ov, wantOv) {
		t.Errorf("overrides: %+v", ov)
	}
	if _, err := set.Sources("nope"); err == nil {
		t.Error("unknown target")
	}
	if set.Overrides("nope") != nil {
		t.Error("overrides of an unknown target")
	}
}
