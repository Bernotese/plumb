//go:build guest

package resource

// SetFirewallRollbackAfter sets the delay of every rollback timer in
// seconds and returns the old one. Only guest test builds have it: the
// guest test lets the timer fire without waiting two minutes.
func SetFirewallRollbackAfter(sec int) int {
	old := rollbackAfter
	rollbackAfter = sec
	return old
}
