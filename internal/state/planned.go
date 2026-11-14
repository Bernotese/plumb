package state

// plannedFields refuses the Target fields of the slice 7 contract
// (T-217) until the card that serves each one removes its line here: a
// document must never hold a field that Plumb then ignores without a word
// (a silent cap is a bypass). T-219 removes guest.nics, T-221 backup.
func plannedFields(t *targetSpec, errf func(path, format string, args ...any) *Error) Errors {
	var errs Errors
	if _, ok := t.Guest["nics"]; ok {
		errs = append(errs, errf("/spec/guest/nics", "more NICs are planned (T-219) and not served yet"))
	}
	if t.Backup != nil {
		errs = append(errs, errf("/spec/backup", "guest backups are planned (T-221) and not served yet"))
	}
	return errs
}
