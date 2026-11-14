package state

// Adopted is spec.adopted of a Target (D-373, D-386): the guest was made by
// a person and put into the pool; Plumb never created it.
type Adopted struct {
	Node        string `json:"node"`
	VMID        int    `json:"vmid"`
	DeleteGuest bool   `json:"delete_guest"`
}

// AdoptedOf returns spec.adopted of a Target, or false for a Target that
// Plumb made (or an unknown one).
func (s *Set) AdoptedOf(target string) (Adopted, bool) {
	t, ok := s.targets[target]
	if !ok || t.Adopted == nil {
		return Adopted{}, false
	}
	return *t.Adopted, true
}
