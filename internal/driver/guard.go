package driver

import (
	"context"
	"errors"

	"github.com/Bernotese/plumb/internal/wire"
)

// ErrReadOnly: the driver refuses a write in restored mode (D-303).
var ErrReadOnly = errors.New("driver: restored mode: Plumb sends no Proxmox write until POST /restore/confirm")

// ReadOnlyWhen wraps a driver: while active() is true every method that
// writes to the hypervisor fails with ErrReadOnly, before any call; read
// methods pass. It is the second line of restored mode (D-303): the
// engine starts no Run there in the first place.
func ReadOnlyWhen(d Driver, active func() bool) Driver { return guard{d, active} }

type guard struct {
	d      Driver
	active func() bool
}

func (g guard) FetchImage(ctx context.Context, spec ImageSpec) (ImageRef, error) {
	if g.active() {
		return "", ErrReadOnly
	}
	return g.d.FetchImage(ctx, spec)
}

func (g guard) Create(ctx context.Context, spec GuestSpec, boot Bootstrap) (GuestID, error) {
	if g.active() {
		return "", ErrReadOnly
	}
	return g.d.Create(ctx, spec, boot)
}

func (g guard) Observe(ctx context.Context, id GuestID) (GuestState, error) { return g.d.Observe(ctx, id) }

func (g guard) Diff(desired GuestSpec, observed GuestState) ([]wire.Change, error) {
	return g.d.Diff(desired, observed)
}

func (g guard) Update(ctx context.Context, id GuestID, changes []wire.Change, boot *Bootstrap) error {
	if g.active() {
		return ErrReadOnly
	}
	return g.d.Update(ctx, id, changes, boot)
}

func (g guard) Power(ctx context.Context, id GuestID, action PowerAction) error {
	if g.active() {
		return ErrReadOnly
	}
	return g.d.Power(ctx, id, action)
}

func (g guard) ClearBootstrap(ctx context.Context, id GuestID) error {
	if g.active() {
		return ErrReadOnly
	}
	return g.d.ClearBootstrap(ctx, id)
}

func (g guard) Delete(ctx context.Context, id GuestID) error {
	if g.active() {
		return ErrReadOnly
	}
	return g.d.Delete(ctx, id)
}

func (g guard) CreateBuild(ctx context.Context, spec BuildSpec, boot Bootstrap) (GuestID, error) {
	if g.active() {
		return "", ErrReadOnly
	}
	return g.d.CreateBuild(ctx, spec, boot)
}

func (g guard) MakeImage(ctx context.Context, id GuestID, img BuiltImage) (ImageRef, error) {
	if g.active() {
		return "", ErrReadOnly
	}
	return g.d.MakeImage(ctx, id, img)
}

func (g guard) FindImage(ctx context.Context, image string, version int, digest string) (ImageRef, error) {
	return g.d.FindImage(ctx, image, version, digest)
}

func (g guard) ListImages(ctx context.Context, image string) ([]ImageObject, error) {
	return g.d.ListImages(ctx, image)
}

func (g guard) TemplateRef(spec ImageSpec) (ImageRef, error) { return g.d.TemplateRef(spec) }

func (g guard) RemoveImage(ctx context.Context, obj ImageObject) error {
	if g.active() {
		return ErrReadOnly
	}
	return g.d.RemoveImage(ctx, obj)
}

func (g guard) DiskSpace(ctx context.Context) (DiskSpace, error) { return g.d.DiskSpace(ctx) }

func (g guard) ListBuilds(ctx context.Context) ([]BuildGuest, error) { return g.d.ListBuilds(ctx) }

func (g guard) RemoveBuild(ctx context.Context, id GuestID, image string, version int) error {
	if g.active() {
		return ErrReadOnly
	}
	return g.d.RemoveBuild(ctx, id, image, version)
}

func (g guard) ListGuests(ctx context.Context) ([]PoolGuest, error) { return g.d.ListGuests(ctx) }
