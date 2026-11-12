package state

import (
	"strings"
	"testing"
)

func TestQASysctlParameterizedTargetKind(t *testing.T) {
	b := doc(KindBlock, "tune", `{"params":{"type":"object","properties":{"key":{"type":"string","default":"vm.swappiness"}}},"resources":[{"sysctl":{"name":"{{ .key }}","value":1}}]}`)
	for _, kind := range []string{"vm", "lxc"} {
		t.Run(kind, func(t *testing.T) {
			docs := append(fixture(target(`"blocks":[{"use":"tune"}]`)), b)
			name := "t1"
			if kind == "lxc" {
				docs = []Doc{lxcImage(""), b, doc(KindTarget, "c1", `{"type":"lxc","image":"ct","blocks":[{"use":"tune"}],"guest":{"cores":1,"memory":512,"bridge":"vmbr0","disk":2}}`)}
				name = "c1"
			}
			set, err := Validate(docs)
			if err == nil {
				_, err = set.Render(name)
			}
			if kind == "vm" && err != nil {
				t.Fatal(err)
			}
			if kind == "lxc" && (err == nil || !strings.Contains(err.Error(), "resource type sysctl does not support lxc targets")) {
				t.Fatalf("LXC sysctl: %v", err)
			}
		})
	}
}
