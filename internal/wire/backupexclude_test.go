package wire

import "testing"

// tar leaves out a matched directory with its content: BackupExcluded
// checks the parents of a path too.
func TestBackupExcludedParents(t *testing.T) {
	saved := LXCBackupExclude
	t.Cleanup(func() { LXCBackupExclude = saved })
	LXCBackupExclude = []string{"/var/lib/x/s*"}
	for p, want := range map[string]bool{
		"/var/lib/x/sub":         true,
		"/var/lib/x/sub/a/b":     true,
		"/var/lib/x/other":       false,
		"/var/lib/x":             false,
		"/var/lib/x/sub/../keep": false,
	} {
		if got := BackupExcluded(p); got != want {
			t.Errorf("%s: %v, want %v", p, got, want)
		}
	}
}
