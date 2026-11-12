package state

import (
	"strings"
	"testing"
)

// T-093: sysctl is VM only; a unit resource cannot replace the agent unit.
func TestSlice3ResourceRules(t *testing.T) {
	sysctlB := doc(KindBlock, "fwd", `{"resources":[{"sysctl":{"name":"net.ipv4.ip_forward","value":1}}]}`)
	t.Run("sysctl on lxc refused", func(t *testing.T) {
		tgt := doc(KindTarget, "c1", `{"type":"lxc","image":"ct","blocks":[{"use":"fwd"}],"guest":{"cores":1,"memory":512,"bridge":"vmbr0","disk":2}}`)
		set, err := Validate([]Doc{lxcImage(""), sysctlB, tgt})
		if err == nil {
			_, err = set.Render("c1")
		}
		if err == nil || !strings.Contains(err.Error(), "resource type sysctl does not support lxc targets") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("sysctl on vm", func(t *testing.T) {
		set, err := Validate(append(fixture(target(`"blocks":[{"use":"fwd"}]`)), sysctlB))
		if err != nil {
			t.Fatal(err)
		}
		r, err := set.Render("t1")
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Resources) != 1 || r.Resources[0].ID != "sysctl:net.ipv4.ip_forward" {
			t.Fatalf("resources = %+v", r.Resources)
		}
	})
	t.Run("unit of the agent refused", func(t *testing.T) {
		b := doc(KindBlock, "u", `{"resources":[{"unit":{"name":"plumb-agent.service","content":"[Service]\nExecStart=/bin/true\n"}}]}`)
		set, err := Validate(append(fixture(target(`"blocks":[{"use":"u"}]`)), b))
		if err == nil {
			_, err = set.Render("t1")
		}
		if err == nil {
			t.Fatal("a unit resource on plumb-agent.service was accepted")
		}
	})
}
