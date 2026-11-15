package state

// plannedFields refuses the Target fields of a contract until the card
// that serves each one removes its line here: a document must never hold
// a field that Plumb then ignores without a word (a silent cap is a
// bypass). None now: T-238 serves guest.disks.
func plannedFields(t *targetSpec, errf func(path, format string, args ...any) *Error) Errors {
	return nil
}
