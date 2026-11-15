package state

// plannedFields refuses the Target fields of a contract until the card
// that serves each one removes its line here: a document must never hold
// a field that Plumb then ignores without a word (a silent cap is a
// bypass). Slice 8 (T-237): guest.disks until T-238 serves it.
func plannedFields(t *targetSpec, errf func(path, format string, args ...any) *Error) Errors {
	var errs Errors
	if _, ok := t.Guest["disks"]; ok {
		errs = append(errs, errf("/spec/guest/disks", "more disks are planned (T-238) and not served yet"))
	}
	return errs
}
