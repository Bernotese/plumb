package state

import (
	"encoding/json"
	"strings"
	"testing"
)

func ruleDoc(name, target string, attrs ...string) Doc {
	spec := map[string]any{"action": "ignore", "target": target, "resource": "file:/etc/motd"}
	if len(attrs) > 0 {
		spec["attrs"] = attrs
	}
	raw, _ := json.Marshal(spec)
	return Doc{Kind: KindRule, Name: name, Spec: raw}
}

func TestRuleKind(t *testing.T) {
	docs := fixture(doc(KindTarget, "web1", `{"type":"vm","image":"img",`+guest+`,"blocks":[]}`),
		ruleDoc("one", "web1", "content"), ruleDoc("all", "*"))
	set, err := Validate(docs)
	if err != nil {
		t.Fatal(err)
	}
	rules := set.Rules()
	if len(rules) != 2 || rules[0].Name != "all" || rules[1].Name != "one" {
		t.Fatalf("rules: %+v", rules)
	}
	one, all := rules[1], rules[0]
	for _, c := range []struct {
		r                      Rule
		target, resource, attr string
		want                   bool
	}{
		{one, "web1", "file:/etc/motd", "content", true},
		{one, "web1", "file:/etc/motd", "mode", false},
		{one, "web2", "file:/etc/motd", "content", false},
		{one, "web1", "file:/etc/other", "content", false},
		{all, "web9", "file:/etc/motd", "mode", true},
		{all, "web9", "file:/etc/motdx", "mode", false},
	} {
		if got := c.r.Matches(c.target, c.resource, c.attr); got != c.want {
			t.Errorf("%s %s %s %s: %v", c.r.Name, c.target, c.resource, c.attr, got)
		}
	}
	// A rule names an existing Target or "*"; the schema refuses the rest.
	for _, bad := range []Doc{ruleDoc("x", "web9"), {Kind: KindRule, Name: "y", Spec: json.RawMessage(`{"action":"drop","target":"*","resource":"file:/a"}`)},
		{Kind: KindRule, Name: "z", Spec: json.RawMessage(`{"action":"ignore","target":"*","resource":"no-colon"}`)}} {
		if _, err := Validate(fixture(doc(KindTarget, "web1", `{"type":"vm","image":"img",`+guest+`,"blocks":[]}`), bad)); err == nil {
			t.Errorf("%s accepted", bad.Name)
		} else if bad.Name == "x" && !strings.Contains(err.Error(), "/spec/target") {
			t.Errorf("x: %v", err)
		}
	}
}
