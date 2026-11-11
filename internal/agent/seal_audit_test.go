package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// Exercise the apply guard with aliases that exist only in the guest.
func TestSealAuditStateAliases(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "custom"}[custom], func(t *testing.T) {
			ca := newCA(t)
			ctl := newController(t, ca, "tok-1")
			target := "/var/lib/plumb-agent"
			if custom {
				target = "/srv/private-state"
			}
			ta := startAgentWith(t, ctl, ca, "tok-1", func(o *Options) {
				buildGuest(t, o.Root)
				o.Config.StateDir = filepath.Join(o.Root, target)
			})
			waitFor(t, "enrollment", func() bool { return len(ctl.reportsOf(wire.ReportHello)) > 0 })
			if err := os.MkdirAll(filepath.Join(ta.root, "opt"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(ta.root, "opt/state")); err != nil {
				t.Fatal(err)
			}
			r := fileRes("evil", "/opt/state/credential", map[string]any{"content": `{"credential":"cred-1","build":true,"enrolled":true}`})
			ctl.send(t, "alias", wire.CommandApply, wire.ApplyBody{PlanID: 1, Resources: []wire.Resource{r}, Planned: unverified(r), MaxRisk: wire.RiskDestructive})
			rep := ctl.result(t, "alias")
			if len(rep.Items) != 1 || rep.Items[0].Status != wire.StatusRefused {
				t.Fatalf("alias apply: %+v", rep.Items)
			}
			// mayNotSeal reads the marks from disk: had the forged file
			// landed, it would allow seal. (The file resource also refuses
			// a path through a symlink, so this is the second layer.)
			if why := ta.a.mayNotSeal(); why == "" {
				t.Fatal("alias forged the build mark")
			}
			// Control: the check bites. The same content written directly
			// makes this agent sealable.
			if err := ta.a.state.saveIdentity(identity{Credential: "cred-1", Build: true, Enrolled: true}); err != nil {
				t.Fatal(err)
			}
			if why := ta.a.mayNotSeal(); why != "" {
				t.Fatalf("control: a forged mark on disk must allow seal, got %q", why)
			}
		})
	}
}

// A remove step must unlink a directory symlink without visiting its target.
func TestSealAuditRemoveSymlink(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "etc/keep/secret"), "must stay")
	if err := os.MkdirAll(filepath.Join(root, "var/lib/systemd"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "etc/keep"), filepath.Join(root, "var/lib/systemd/random-seed")); err != nil {
		t.Fatal(err)
	}
	a := &Agent{opt: Options{Root: root}}
	if st, out := a.sealSeed(context.Background(), wire.SealBody{}); st != wire.StatusApplied {
		t.Fatalf("seed: %s %s", st, out)
	}
	if b, err := os.ReadFile(filepath.Join(root, "etc/keep/secret")); err != nil || string(b) != "must stay" {
		t.Fatalf("symlink target: %q %v", b, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "var/lib/systemd/random-seed")); !os.IsNotExist(err) {
		t.Fatalf("seed link remains: %v", err)
	}
}

// A completed seal must retain its identity until the result is accepted.
func TestSealAuditWaitsForResult(t *testing.T) {
	r := &sealRunner{}
	ta, ctl := startBuildAgent(t, true, r)
	ctl.mu.Lock()
	ctl.reportFail = 1000000
	ctl.mu.Unlock()
	ctl.send(t, "seal-wait", wire.CommandSeal, wire.SealBody{PlanID: 1})
	waitFor(t, "result retry", func() bool {
		ctl.mu.Lock()
		defer ctl.mu.Unlock()
		return ctl.reportFail < 1000000
	})
	if storedCred(t, ta.cfg.StateDir) == "" || !exists(ta.root, "/usr/local/bin/plumb-agent") {
		t.Fatal("agent removed before the result was accepted")
	}
	if slices.Contains(r.list(), "sync") || slices.Contains(r.list(), "systemctl poweroff") {
		t.Fatal("guest finalized before the result was accepted")
	}
	ctl.mu.Lock()
	ctl.reportFail = 0
	ctl.mu.Unlock()
	ctl.result(t, "seal-wait")
	waitFor(t, "poweroff after acceptance", func() bool { return slices.Contains(r.list(), "systemctl poweroff") })
}
