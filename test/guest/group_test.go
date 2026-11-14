//go:build guest

package guest_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

func TestGroup(t *testing.T) {
	const name = "plumb_group_test"
	const member = "plumb_group_member"
	t.Cleanup(func() {
		_ = exec.Command("userdel", "--", member).Run()
		_ = exec.Command("groupdel", "--", member).Run()
		_ = exec.Command("groupdel", "--", name).Run()
	})
	r := rsrc("group", name, map[string]any{"gid": 48151})
	converge(t, r)
	if got := sh(t, "getent", "group", name); !strings.Contains(got, ":48151:") {
		t.Fatal(got)
	}
	converge(t, rsrc("user", member, map[string]any{"groups": []string{name}}))
	if got := sh(t, "id", "-nG", member); !strings.Contains(got, name) {
		t.Fatal(got)
	}
	r.Attrs["gid"] = 48152
	changes := converge(t, r)
	if len(changes) != 1 || changes[0].Risk != wire.RiskDisruptive {
		t.Fatalf("gid risk: %+v", changes)
	}
	sh(t, "groupdel", "--", name)
	if changes := plan(t, build(t, r)); len(changes) == 0 {
		t.Fatal("manual removal has no drift")
	}
	converge(t, r)
	// A primary group may change only with destructive approval; removal fails.
	sh(t, "usermod", "--gid", name, "--", member)
	r.Attrs["gid"] = 48153
	changes = plan(t, build(t, r))
	if len(changes) != 1 || changes[0].Risk != wire.RiskDestructive {
		t.Fatalf("primary risk: %+v", changes)
	}
	r.Attrs["state"] = wire.StateAbsent
	rs := build(t, r)
	if err := rs.Apply(context.Background(), plan(t, rs)); err == nil {
		t.Fatal("removed a primary group")
	}
	sh(t, "usermod", "--gid", member, "--", member)
	converge(t, r)
	// System is create-only; an omitted gid never creates drift.
	r = rsrc("group", name, map[string]any{"system": true})
	converge(t, r)
	r.Attrs["system"] = false
	if changes := plan(t, build(t, r)); len(changes) != 0 {
		t.Fatalf("create-only drift: %+v", changes)
	}
	r.Attrs["gid"] = 48151
	rs = build(t, r)
	if err := rs.Apply(context.Background(), plan(t, rs)); err == nil {
		t.Fatal("changed a system gid")
	}
	delete(r.Attrs, "gid")
	r.Attrs["state"] = wire.StateAbsent
	rs = build(t, r)
	if err := rs.Apply(context.Background(), plan(t, rs)); err == nil {
		t.Fatal("removed a system group")
	}
}
