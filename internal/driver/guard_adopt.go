package driver

import "context"

// The guard forwards the optional Adopter (D-386). InspectGuest only
// reads, so it works in restored mode too; SetAdopted changes no guest.

var _ Adopter = guard{}

func (g guard) InspectGuest(ctx context.Context, id GuestID) (Unmanaged, error) {
	a, ok := g.d.(Adopter)
	if !ok {
		return Unmanaged{}, ErrNotImplemented
	}
	return a.InspectGuest(ctx, id)
}

func (g guard) SetAdopted(refs map[GuestID]string) {
	if a, ok := g.d.(Adopter); ok {
		a.SetAdopted(refs)
	}
}
