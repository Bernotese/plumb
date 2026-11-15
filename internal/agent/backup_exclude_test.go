package agent

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-231: a container backup leaves out exactly the agent's secrets and
// keeps what a restored container needs to start and re-enroll.
func TestBackupExcludeSecretsOnly(t *testing.T) {
	state := func(name string) string { return path.Join(DefaultStateDir, name) }
	out := []string{
		state(credentialFile),
		state(desiredFile),
		state("." + credentialFile + "-123456"), // temp names of stateDir.write
		state("." + desiredFile + "-123456"),
		wire.RepairTokenFile,
	}
	for _, p := range out {
		if !wire.BackupExcluded(p) {
			t.Errorf("%s is in a container backup", p)
		}
	}
	keep := []string{
		DefaultConfigPath,         // agent.json: URL, CA, token_env
		"/etc/plumb-agent/ca.pem", // the first-boot installer's CA
		resource.FirewallBootFile, // the firewall at boot
		state(repairFile),         // the last key set (public keys)
		state(safeApplyMarker),    // a safe apply in progress
		state(updateMarkerFile),   // the update guard
		state("safe-apply/network-x.confirmed"),
		"/usr/local/bin/plumb-agent",
	}
	for _, p := range keep {
		if wire.BackupExcluded(p) {
			t.Errorf("%s is left out of a container backup", p)
		}
	}
}

// A container restored from such a backup (its state dir without the
// excluded files) enrolls with the token of the re-enroll Plan.
func TestRestoredStateReenrolls(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	waitFor(t, "first enrollment", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 1 })
	// A file that a restore keeps.
	write(t, filepath.Join(ta.cfg.StateDir, repairFile), "{}")
	ta.cancel()
	<-ta.done

	// The restore: the state dir as the backup holds it.
	err := filepath.WalkDir(ta.cfg.StateDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(ta.cfg.StateDir, p)
		if wire.BackupExcluded(path.Join(DefaultStateDir, filepath.ToSlash(rel))) {
			return os.Remove(p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ta.cfg.StateDir, credentialFile)); !os.IsNotExist(err) {
		t.Fatalf("credential after the restore: %v", err)
	}
	// The restore revokes the old credential; the re-enroll Plan sets a
	// new token.
	ctl.mu.Lock()
	delete(ctl.creds, "cred-1")
	ctl.tokens["tok-2"] = true
	ctl.mu.Unlock()
	tb := startAgentWith(t, ctl, ca, "tok-2", func(o *Options) { o.Config.StateDir = ta.cfg.StateDir })
	waitFor(t, "enrollment after the restore", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 2 })
	if got := storedCred(t, tb.cfg.StateDir); got != "cred-2" {
		t.Fatalf("credential file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(tb.cfg.StateDir, repairFile)); err != nil {
		t.Fatalf("a kept file: %v", err)
	}
}
