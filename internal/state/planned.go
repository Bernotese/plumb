package state

// plannedFields refuses the Target fields of a contract until the card
// that serves each one removes its line here: a document must never hold
// a field that Plumb then ignores without a word (a silent cap is a
// bypass). Slice 7 serves all its fields now: backup (T-221) and the
// address of a VM NIC (T-220, the agent's network resource, D-403).
func plannedFields(_ *targetSpec, _ func(path, format string, args ...any) *Error) Errors {
	return nil
}
