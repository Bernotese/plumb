package fake

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"maps"
	"sync"

	"github.com/Bernotese/plumb/internal/driver"
)

// Adoption in the fake (D-386). The state lives beside the Driver (keyed
// by the *Driver), so fake.go keeps its fields as they are.
type adoptData struct {
	mu      sync.Mutex
	views   map[driver.GuestID]driver.Unmanaged
	adopted map[driver.GuestID]string
}

var adoptState sync.Map // *Driver → *adoptData

func (d *Driver) adoptData() *adoptData {
	v, _ := adoptState.LoadOrStore(d, &adoptData{views: map[driver.GuestID]driver.Unmanaged{}})
	return v.(*adoptData)
}

var _ driver.Adopter = (*Driver)(nil)

// AddUnmanaged puts a guest that a person made into the fake pool: it
// observes like any guest (spec, running), and InspectGuest shows u. The
// digest is derived from the name, so RenameGuest changes it.
func (d *Driver) AddUnmanaged(u driver.Unmanaged, spec driver.GuestSpec) driver.GuestID {
	d.mu.Lock()
	id := driver.GuestID(fmt.Sprint(d.nextID))
	d.nextID++
	spec.Name = u.Name
	d.guests[id] = &guest{spec: spec, next: spec, power: driver.PowerRunning}
	d.mu.Unlock()
	u.ID, u.Digest = id, fakeDigest(u.Name, 0)
	if u.Reasons == nil {
		u.Reasons = []string{}
	}
	a := d.adoptData()
	a.mu.Lock()
	a.views[id] = u
	a.mu.Unlock()
	return id
}

// RenameGuest renames a guest outside Plumb (or simulates another guest
// on the same VMID): name and digest change.
func (d *Driver) RenameGuest(id driver.GuestID, name string) {
	d.mu.Lock()
	if g, ok := d.guests[id]; ok {
		g.spec.Name, g.next.Name = name, name
		g.gen++
	}
	d.mu.Unlock()
	a := d.adoptData()
	a.mu.Lock()
	if u, ok := a.views[id]; ok {
		u.Name, u.Digest = name, fakeDigest(name, len(name))
		a.views[id] = u
	}
	a.mu.Unlock()
}

// Adopted returns the last SetAdopted set.
func (d *Driver) Adopted() map[driver.GuestID]string {
	a := d.adoptData()
	a.mu.Lock()
	defer a.mu.Unlock()
	return maps.Clone(a.adopted)
}

// InspectGuest implements driver.Adopter.
func (d *Driver) InspectGuest(ctx context.Context, id driver.GuestID) (driver.Unmanaged, error) {
	d.mu.Lock()
	_, inPool := d.guests[id]
	d.mu.Unlock()
	a := d.adoptData()
	a.mu.Lock()
	defer a.mu.Unlock()
	u, ok := a.views[id]
	if !inPool || !ok {
		return driver.Unmanaged{}, fmt.Errorf("guest %s: %w", id, driver.ErrNotInPool)
	}
	return u, nil
}

// SetAdopted implements driver.Adopter.
func (d *Driver) SetAdopted(refs map[driver.GuestID]string) {
	a := d.adoptData()
	a.mu.Lock()
	a.adopted = maps.Clone(refs)
	a.mu.Unlock()
}

func fakeDigest(name string, salt int) string {
	h := sha1.Sum([]byte(fmt.Sprint(name, salt)))
	return hex.EncodeToString(h[:])
}
