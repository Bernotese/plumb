package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// Adoption (D-373, D-378, D-386, D-391, D-392, ARCHITECTURE §19). A guest
// that a person put into the pool becomes a Target by a human-only Plan.
// Plumb never moves a guest and writes nothing to Proxmox to adopt one.

// OriginAdopt is the origin of an adopt Plan.
const OriginAdopt = "adopt"

// Errors of the adoption. The API maps them to 409 with these codes.
var (
	// ErrNotAdoptable: the guest has a reason against the adoption.
	ErrNotAdoptable = errors.New("not-adoptable")
	// ErrGuestChanged: the digest or the name differs from the request
	// (the guest changed, was renamed, or the VMID holds another guest).
	ErrGuestChanged = errors.New("guest-changed")
	// ErrGuestBound: a Target or an Image already holds the guest.
	ErrGuestBound = errors.New("guest-bound")
	// ErrNoAdopter: the driver cannot adopt (the fake without it, or a
	// driver that does not implement driver.Adopter).
	ErrNoAdopter = errors.New("plan: the driver cannot adopt guests")
)

// AdoptRequest names the guest of an adoption and the Target name.
type AdoptRequest struct {
	Target string
	Node   string
	VMID   int
	Digest string
}

func (e *Engine) adopter() (driver.Adopter, error) {
	a, ok := e.cfg.Driver.(driver.Adopter)
	if !ok {
		return nil, ErrNoAdopter
	}
	return a, nil
}

// boundRefs are the guest refs that any Target or Image binding holds,
// foreign marks included: such a guest is never unmanaged.
func (e *Engine) boundRefs(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	for _, kind := range []string{state.KindTarget, state.KindImage} {
		binds, err := e.st.Bindings(ctx, kind)
		if err != nil {
			return nil, err
		}
		for _, b := range binds {
			out[b.Ref] = true
		}
	}
	return out, nil
}

// UnmanagedGuests lists the pool guests that no Target, Image binding,
// build or template holds, each as InspectGuest reads it (D-386).
func (e *Engine) UnmanagedGuests(ctx context.Context) ([]driver.Unmanaged, error) {
	a, err := e.adopter()
	if err != nil {
		return nil, err
	}
	pool, err := e.UnknownGuests(ctx)
	if err != nil {
		return nil, err
	}
	bound, err := e.boundRefs(ctx)
	if err != nil {
		return nil, err
	}
	out := []driver.Unmanaged{}
	for _, g := range pool {
		if g.Template || bound[string(g.ID)] {
			continue
		}
		u, err := a.InspectGuest(ctx, g.ID)
		if errors.Is(err, driver.ErrNotInPool) || errors.Is(err, driver.ErrNotFound) {
			continue // left the pool since the list
		}
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// checkAdoptable reads the guest again and checks it against the request:
// in the pool, adoptable, the same digest, and the Proxmox name equal to
// the Target name (D-392). It returns the fresh view.
func (e *Engine) checkAdoptable(ctx context.Context, req AdoptRequest) (driver.Unmanaged, error) {
	a, err := e.adopter()
	if err != nil {
		return driver.Unmanaged{}, err
	}
	bound, err := e.boundRefs(ctx)
	if err != nil {
		return driver.Unmanaged{}, err
	}
	ref := driver.GuestID(strconv.Itoa(req.VMID))
	if bound[string(ref)] {
		return driver.Unmanaged{}, fmt.Errorf("%w: VMID %d is held by a Target or an Image", ErrGuestBound, req.VMID)
	}
	u, err := a.InspectGuest(ctx, ref)
	if errors.Is(err, driver.ErrNotInPool) || errors.Is(err, driver.ErrNotFound) {
		return driver.Unmanaged{}, fmt.Errorf("%w: VMID %d is not in the pool", ErrGuestChanged, req.VMID)
	}
	if err != nil {
		return driver.Unmanaged{}, err
	}
	switch {
	case u.Node != req.Node:
		return driver.Unmanaged{}, fmt.Errorf("%w: the guest is on node %q, not %q", ErrGuestChanged, u.Node, req.Node)
	case u.Digest == "" || u.Digest != req.Digest:
		return driver.Unmanaged{}, fmt.Errorf("%w: the Proxmox config of VMID %d changed (digest)", ErrGuestChanged, req.VMID)
	case u.Name != req.Target:
		return driver.Unmanaged{}, fmt.Errorf("%w: the guest is named %q; the Target must have the same name (D-392)", ErrGuestChanged, u.Name)
	case !u.Adoptable():
		return driver.Unmanaged{}, fmt.Errorf("%w: %v", ErrNotAdoptable, u.Reasons)
	}
	return u, nil
}

// AdoptDoc checks the request and returns the Target document of the
// adoption: type, guest (never ssh_keys) and spec.adopted, no image
// (D-386). The adopt Plan puts it; the Run checks the guest again.
func (e *Engine) AdoptDoc(ctx context.Context, req AdoptRequest) (state.Doc, error) {
	u, err := e.checkAdoptable(ctx, req)
	if err != nil {
		return state.Doc{}, err
	}
	spec := map[string]any{
		"type":    string(u.Kind),
		"guest":   u.Guest,
		"adopted": map[string]any{"node": req.Node, "vmid": req.VMID, "delete_guest": false},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return state.Doc{}, err
	}
	return state.Doc{Kind: state.KindTarget, Name: req.Target, Spec: raw}, nil
}

// adoptBind is the Run step of an adopt Plan: it reads the guest again
// (node, VMID, name, digest; D-386) and binds it to the Target. It writes
// nothing to Proxmox. The driver learns the ref before the Observe, so
// own() accepts a VMID outside the Plumb range (D-392).
func (e *Engine) adoptBind(ctx context.Context, req AdoptRequest) error {
	if _, err := e.checkAdoptable(ctx, req); err != nil {
		return err
	}
	ref := driver.GuestID(strconv.Itoa(req.VMID))
	adoptMu.Lock()
	defer adoptMu.Unlock()
	if err := e.syncAdoptedLocked(ctx, map[driver.GuestID]string{ref: req.Target}); err != nil {
		return err
	}
	st, err := e.cfg.Driver.Observe(ctx, ref)
	if err == nil {
		err = e.st.PutBinding(ctx, store.Binding{Kind: state.KindTarget, Name: req.Target, Ref: string(ref), Digest: pollKey(st)})
	}
	// After a failure the extra ref goes again: only bound Targets count.
	if serr := e.syncAdoptedLocked(ctx, nil); err == nil {
		err = serr
	}
	return err
}

// adoptMu serializes the whole-set updates of SetAdopted.
var adoptMu sync.Mutex

// SyncAdopted tells the driver the guests of the bound adopted Targets
// of the head revision (D-392): only those may be outside the Plumb VMID
// range, and only under their Target's name. The engine calls it at the
// start and after every Run that changed a Target.
func (e *Engine) SyncAdopted(ctx context.Context) error {
	adoptMu.Lock()
	defer adoptMu.Unlock()
	return e.syncAdoptedLocked(ctx, nil)
}

func (e *Engine) syncAdoptedLocked(ctx context.Context, extra map[driver.GuestID]string) error {
	a, err := e.adopter()
	if err != nil {
		return nil // no adoption in this driver: nothing to tell
	}
	_, set, err := e.base(ctx)
	if err != nil {
		return err
	}
	binds, err := e.st.Bindings(ctx, state.KindTarget)
	if err != nil {
		return err
	}
	refs := map[driver.GuestID]string{}
	for _, b := range binds {
		ad, ok := set.AdoptedOf(b.Name)
		if !ok || strconv.Itoa(ad.VMID) != b.Ref {
			continue
		}
		refs[driver.GuestID(b.Ref)] = b.Name
	}
	for ref, name := range extra {
		refs[ref] = name
	}
	a.SetAdopted(refs)
	return nil
}
