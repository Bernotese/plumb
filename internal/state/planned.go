package state

import "fmt"

// plannedFields refuses the Target fields of the slice 7 contract
// (T-217) until the card that serves each one removes its line here: a
// document must never hold a field that Plumb then ignores without a word
// (a silent cap is a bypass). T-221 removes backup; T-220 removes the
// address of a VM NIC.
func plannedFields(t *targetSpec, errf func(path, format string, args ...any) *Error) Errors {
	var errs Errors
	// A VM NIC gets its address through the agent's network resource
	// (D-403, T-220). A container NIC has it now: Proxmox sets it.
	if nics, _ := t.Guest["nics"].([]any); t.Type == "vm" {
		for i, item := range nics {
			m, _ := item.(map[string]any)
			for _, k := range []string{"ip", "gateway"} {
				if _, ok := m[k]; ok {
					errs = append(errs, errf(fmt.Sprintf("/spec/guest/nics/%d/%s", i, k),
						"the address of a VM NIC is planned (T-220) and not served yet; leave %s out for now (the NIC comes up without an address)", k))
				}
			}
		}
	}
	if t.Backup != nil {
		errs = append(errs, errf("/spec/backup", "guest backups are planned (T-221) and not served yet"))
	}
	return errs
}
